// Package describe turns untrusted model suggestions into deterministic
// description-only GridSpec transactions.
package describe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
	"github.com/promisegrid/gridspec/internal/view"
)

const auditSchema = "gridspec.description-audit/v1"

type Suggestion struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type Result struct {
	Program  []byte
	Request  provider.Request
	Response provider.Response
	Count    int
	Noop     bool
}

type target struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	MissingTitle   bool   `json:"missing_title"`
	MissingSummary bool   `json:"missing_summary"`
	fields         map[string]any
}

// Pending reports how many active declarations still need a title or summary.
func Pending(state specstate.State, graphID string) (int, error) {
	targets, err := selectTargets(state, graphID)
	return len(targets), err
}

// Generate asks a model for bounded descriptions, validates the complete
// response, and constructs full replacement entities without trusting model
// transaction text.
func Generate(ctx context.Context, generator provider.Provider, state specstate.State, graphID string) (Result, error) {
	targets, err := selectTargets(state, graphID)
	if err != nil {
		return Result{}, err
	}
	if len(targets) == 0 {
		return Result{Noop: true}, nil
	}
	if generator == nil {
		return Result{}, fmt.Errorf("model provider is required")
	}
	stateText, err := view.Text(state)
	if err != nil {
		return Result{}, err
	}
	inputTargets := make([]target, len(targets))
	for index, current := range targets {
		inputTargets[index] = current
		inputTargets[index].fields = nil
	}
	input, err := json.Marshal(struct {
		StateCID string   `json:"state_cid"`
		Module   string   `json:"module"`
		Targets  []target `json:"targets"`
		State    string   `json:"state"`
	}{state.CID.String(), state.Module, inputTargets, string(stateText)})
	if err != nil {
		return Result{}, err
	}
	request := provider.Request{
		Operation:    "gridspec_describe",
		Instructions: fmt.Sprintf("Return only one strict JSON object of the form {\"descriptions\":[{\"kind\":\"graph|node|edge\",\"id\":\"...\",\"title\":\"...\",\"summary\":\"...\"}]}. Return exactly one entry for every requested target and no others. Titles must be trimmed, single-line, human-readable, and at most %d characters. Summaries must be trimmed, single-line, and at most %d characters. Explain behavior, not identifier spelling. Do not use Markdown fences.", model.TitleMaxRunes, model.SummaryMaxRunes),
		Input:        string(input), MaxOutputTokens: 32_768,
	}
	response, err := generator.Generate(ctx, request)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	suggestions, err := parseSuggestions(response.Text)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	byKey := make(map[string]Suggestion, len(suggestions))
	for _, suggestion := range suggestions {
		key := specstate.Key(suggestion.Kind, suggestion.ID)
		if _, duplicate := byKey[key]; duplicate {
			return Result{Request: request, Response: response}, fmt.Errorf("provider returned duplicate description for %s %s", suggestion.Kind, suggestion.ID)
		}
		if !model.ValidTitle(suggestion.Title) {
			return Result{Request: request, Response: response}, fmt.Errorf("provider returned invalid title for %s %s", suggestion.Kind, suggestion.ID)
		}
		if suggestion.Summary == "" || !model.ValidSummary(suggestion.Summary) {
			return Result{Request: request, Response: response}, fmt.Errorf("provider returned invalid summary for %s %s", suggestion.Kind, suggestion.ID)
		}
		byKey[key] = suggestion
	}
	if len(byKey) != len(targets) {
		return Result{Request: request, Response: response}, fmt.Errorf("provider returned %d descriptions for %d targets", len(byKey), len(targets))
	}
	program := txnlang.Program{}
	for _, current := range targets {
		key := specstate.Key(current.Kind, current.ID)
		suggestion, ok := byKey[key]
		if !ok {
			return Result{Request: request, Response: response}, fmt.Errorf("provider omitted description for %s %s", current.Kind, current.ID)
		}
		fields := cloneFields(current.fields)
		if current.MissingTitle {
			fields["title"] = suggestion.Title
		}
		if current.MissingSummary {
			fields["summary"] = suggestion.Summary
		}
		program.Ops = append(program.Ops,
			txnlang.Operation{Action: "assert_exists", Kind: current.Kind, ID: current.ID},
			txnlang.Operation{Action: "put", Kind: current.Kind, ID: current.ID, Fields: fields},
		)
		delete(byKey, key)
	}
	if len(byKey) != 0 {
		return Result{Request: request, Response: response}, fmt.Errorf("provider returned a description for an entity that was not requested")
	}
	canonical, err := txnlang.Format(program)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	return Result{Program: canonical, Request: request, Response: response, Count: len(targets)}, nil
}

func selectTargets(state specstate.State, graphID string) ([]target, error) {
	if graphID != "" {
		if _, ok := state.Entities[specstate.Key("graph", graphID)]; !ok {
			return nil, fmt.Errorf("graph %q does not exist", graphID)
		}
	}
	keys := make([]string, 0, len(state.Entities))
	for key := range state.Entities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []target
	for _, key := range keys {
		entity := state.Entities[key]
		if entity.Kind != "graph" && entity.Kind != "node" && entity.Kind != "edge" {
			continue
		}
		status, _ := entity.Fields["status"].(string)
		if status != "active" {
			continue
		}
		if graphID != "" {
			selected := entity.Kind == "graph" && entity.ID == graphID
			if entity.Kind != "graph" {
				selected = entity.Fields["graph_id"] == graphID
			}
			if !selected {
				continue
			}
		}
		title, titleOK := entity.Fields["title"].(string)
		summary, summaryOK := entity.Fields["summary"].(string)
		missingTitle := !titleOK || title == ""
		missingSummary := !summaryOK || summary == ""
		if !missingTitle && !missingSummary {
			continue
		}
		result = append(result, target{
			Kind: entity.Kind, ID: entity.ID, MissingTitle: missingTitle, MissingSummary: missingSummary, fields: entity.Fields,
		})
	}
	return result, nil
}

func parseSuggestions(text string) ([]Suggestion, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var result struct {
		Descriptions []Suggestion `json:"descriptions"`
	}
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode provider descriptions: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing JSON value")
		}
		return nil, fmt.Errorf("decode provider descriptions: %w", err)
	}
	return result.Descriptions, nil
}

func cloneFields(fields map[string]any) map[string]any {
	result := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		result[key] = value
	}
	return result
}

// Audit encodes the exact model request and response for storage as an opaque
// required CAS link on the resulting proposal event.
func Audit(state specstate.State, result Result) ([]byte, error) {
	record := struct {
		Schema          string `json:"schema"`
		StateCID        string `json:"state_cid"`
		Operation       string `json:"operation"`
		Instructions    string `json:"instructions"`
		Input           string `json:"input"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		Model           string `json:"model"`
		ResponseID      string `json:"response_id"`
		Response        string `json:"response"`
	}{auditSchema, state.CID.String(), result.Request.Operation, result.Request.Instructions, result.Request.Input, result.Request.MaxOutputTokens, result.Response.Model, result.Response.ID, result.Response.Text}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
