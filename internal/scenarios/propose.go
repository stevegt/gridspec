package scenarios

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

const auditSchema = "gridspec.scenario-audit/v1"

type Suggestion struct {
	ID       string                  `json:"id"`
	Title    string                  `json:"title,omitempty"`
	Summary  string                  `json:"summary,omitempty"`
	URL      string                  `json:"url,omitempty"`
	Scenario *model.ScenarioContract `json:"scenario"`
}

type Result struct {
	Program  []byte
	Request  provider.Request
	Response provider.Response
	Count    int
	Noop     bool
}

type proposalTarget struct {
	ID       string         `json:"id,omitempty"`
	Create   bool           `json:"create"`
	Scope    string         `json:"scope,omitempty"`
	Graph    map[string]any `json:"graph,omitempty"`
	Contract map[string]any `json:"contract,omitempty"`
}

// Generate asks a model for the complete missing command-oracle payload and
// constructs a field-preserving transaction. Model-authored transaction text
// is never executed.
func Generate(ctx context.Context, generator provider.Provider, state specstate.State, graphID string) (Result, error) {
	missing, err := Missing(state, graphID)
	if err != nil {
		return Result{}, err
	}
	if len(missing) == 0 {
		return Result{Noop: true}, nil
	}
	if generator == nil {
		return Result{}, fmt.Errorf("model provider is required")
	}
	targets := make([]proposalTarget, 0, len(missing))
	for _, id := range missing {
		if id == "" {
			targets = append(targets, proposalTarget{Create: true, Scope: graphID})
			continue
		}
		target := proposalTarget{ID: id}
		target.Graph = cloneFields(state.Entities[specstate.Key("graph", id)].Fields)
		if contract, ok := state.Entities[specstate.Key("contract", id)]; ok {
			target.Contract = cloneFields(contract.Fields)
		}
		targets = append(targets, target)
	}
	stateText, err := view.Text(state)
	if err != nil {
		return Result{}, err
	}
	input, err := json.Marshal(struct {
		StateCID string           `json:"state_cid"`
		Module   string           `json:"module"`
		Targets  []proposalTarget `json:"targets"`
		State    string           `json:"state"`
	}{state.CID.String(), state.Module, targets, string(stateText)})
	if err != nil {
		return Result{}, err
	}
	request := provider.Request{
		Operation:    "gridspec_scenarios",
		Instructions: fmt.Sprintf("Return only strict JSON {\"scenarios\":[{\"id\":string,\"title\":string,\"summary\":string,\"url\":string,\"scenario\":{\"artifact\":string,\"covers\":[string],\"repeat\":integer,\"fixtures\":[{\"path\":string,\"content\":string}],\"steps\":[{\"id\":string,\"args\":[string],\"stdin\":string,\"timeout\":string,\"expect\":{\"exit_code\":integer,\"stdout\":string,\"stderr\":string,\"files\":[{\"path\":string,\"exists\":boolean,\"text\":string or \"json\":string}]}}]}}]}. Fill each existing target exactly once; for a create target propose at least one new scenario graph with a suitable existing Markdown URL when the profile is build-ready. Steps run as separate processes in order in one workspace. Expectations must be exact; runtime paths in streams are normalized to <WORK>, <MODULE>, and <BINARY>. Keep at most %d steps, repeat 1-%d, fixture bytes at most %d, stream values at most %d, and timeouts at most %s. Titles are single-line and at most %d characters; summaries are single-line and at most %d. Use semantic IDs from the supplied state for covers. Do not use Markdown fences.", MaxSteps, MaxRepeat, MaxFixtureBytes, MaxStreamBytes, MaxTimeout, model.TitleMaxRunes, model.SummaryMaxRunes),
		Input:        string(input), MaxOutputTokens: 65_536,
	}
	response, err := generator.Generate(ctx, request)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	suggestions, err := parseSuggestions(response.Text)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	program, err := proposalProgram(state, targets, suggestions)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	canonical, err := txnlang.Format(program)
	if err != nil {
		return Result{Request: request, Response: response}, err
	}
	return Result{Program: canonical, Request: request, Response: response, Count: len(suggestions)}, nil
}

func proposalProgram(state specstate.State, targets []proposalTarget, suggestions []Suggestion) (txnlang.Program, error) {
	create := len(targets) == 1 && targets[0].Create
	if len(suggestions) == 0 || !create && len(suggestions) != len(targets) {
		return txnlang.Program{}, fmt.Errorf("provider returned %d scenarios for %d target(s)", len(suggestions), len(targets))
	}
	byID := make(map[string]Suggestion, len(suggestions))
	for _, suggestion := range suggestions {
		if !txnlang.IsIdentifier(suggestion.ID) || suggestion.Scenario == nil {
			return txnlang.Program{}, fmt.Errorf("provider returned an invalid scenario ID or omitted its contract")
		}
		if _, duplicate := byID[suggestion.ID]; duplicate {
			return txnlang.Program{}, fmt.Errorf("provider returned duplicate scenario %s", suggestion.ID)
		}
		byID[suggestion.ID] = suggestion
	}
	if !create {
		for _, target := range targets {
			if _, ok := byID[target.ID]; !ok {
				return txnlang.Program{}, fmt.Errorf("provider omitted scenario %s", target.ID)
			}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	program := txnlang.Program{}
	for _, id := range ids {
		suggestion := byID[id]
		if create && targets[0].Scope != "" && !contains(suggestion.Scenario.Covers, targets[0].Scope) {
			return txnlang.Program{}, fmt.Errorf("new scenario %s does not cover requested graph %s", id, targets[0].Scope)
		}
		graph, exists := state.Entities[specstate.Key("graph", id)]
		if create {
			if exists || !model.ValidTitle(suggestion.Title) || suggestion.Summary == "" || !model.ValidSummary(suggestion.Summary) {
				return txnlang.Program{}, fmt.Errorf("new scenario %s has an existing ID or invalid title/summary", id)
			}
			graphFields := map[string]any{
				"title": suggestion.Title, "summary": suggestion.Summary,
				"kind": "scenario", "status": "active", "directed": true, "strict": false,
				"attrs": map[string]any{},
			}
			if suggestion.URL != "" {
				graphFields["url"] = suggestion.URL
			}
			program.Ops = append(program.Ops,
				txnlang.Operation{Action: "assert_absent", Kind: "graph", ID: id},
				txnlang.Operation{Action: "put", Kind: "graph", ID: id, Fields: graphFields},
			)
		} else {
			if !exists || graph.Fields["kind"] != "scenario" {
				return txnlang.Program{}, fmt.Errorf("provider returned scenario %s outside the requested targets", id)
			}
			program.Ops = append(program.Ops, txnlang.Operation{Action: "assert_exists", Kind: "graph", ID: id})
		}
		contractFields := map[string]any{
			"schema": "gridspec.contract/v1", "for": id, "kind": "scenario", "status": "active",
		}
		if existing, ok := state.Entities[specstate.Key("contract", id)]; ok {
			contractFields = cloneFields(existing.Fields)
			program.Ops = append(program.Ops, txnlang.Operation{Action: "assert_exists", Kind: "contract", ID: id})
		} else {
			program.Ops = append(program.Ops, txnlang.Operation{Action: "assert_absent", Kind: "contract", ID: id})
		}
		contractFields["scenario"] = scenarioFields(*suggestion.Scenario)
		program.Ops = append(program.Ops, txnlang.Operation{Action: "put", Kind: "contract", ID: id, Fields: contractFields})
	}
	candidate, err := specstate.Apply(state, program)
	if err != nil {
		return txnlang.Program{}, err
	}
	ir, _, _, err := candidate.SemanticView()
	if err != nil {
		return txnlang.Program{}, err
	}
	if problems := Validate(ir); len(problems) != 0 {
		return txnlang.Program{}, fmt.Errorf("provider scenarios are invalid: %s: %s", problems[0].ScenarioID, problems[0].Message)
	}
	return program, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func parseSuggestions(text string) ([]Suggestion, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var payload struct {
		Scenarios []Suggestion `json:"scenarios"`
	}
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode provider scenarios: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing JSON value")
		}
		return nil, fmt.Errorf("decode provider scenarios: %w", err)
	}
	return payload.Scenarios, nil
}

func scenarioFields(value model.ScenarioContract) map[string]any {
	fixtures := make([]any, len(value.Fixtures))
	for i, fixture := range value.Fixtures {
		fixtures[i] = map[string]any{"path": fixture.Path, "content": fixture.Content}
	}
	steps := make([]any, len(value.Steps))
	for i, step := range value.Steps {
		args := make([]any, len(step.Args))
		for j, arg := range step.Args {
			args[j] = arg
		}
		files := make([]any, len(step.Expect.Files))
		for j, file := range step.Expect.Files {
			fields := map[string]any{"path": file.Path}
			if file.Exists != nil {
				fields["exists"] = *file.Exists
			}
			if file.Text != nil {
				fields["text"] = *file.Text
			}
			if file.JSON != nil {
				fields["json"] = *file.JSON
			}
			files[j] = fields
		}
		expect := map[string]any{"files": files}
		if step.Expect.ExitCode != nil {
			expect["exit_code"] = *step.Expect.ExitCode
		}
		if step.Expect.Stdout != nil {
			expect["stdout"] = *step.Expect.Stdout
		}
		if step.Expect.Stderr != nil {
			expect["stderr"] = *step.Expect.Stderr
		}
		steps[i] = map[string]any{
			"id": step.ID, "args": args, "stdin": step.Stdin,
			"timeout": step.Timeout, "expect": expect,
		}
	}
	covers := make([]any, len(value.Covers))
	for i, covered := range value.Covers {
		covers[i] = covered
	}
	return map[string]any{
		"artifact": value.Artifact, "covers": covers, "repeat": value.Repeat,
		"fixtures": fixtures, "steps": steps,
	}
}

func cloneFields(fields map[string]any) map[string]any {
	result := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		result[key] = value
	}
	return result
}

// Audit returns exact provider request/response bytes for a required CAS link.
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
