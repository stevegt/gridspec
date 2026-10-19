// Package report renders diagnostics without changing their stable semantics.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/promisegrid/gridspec/internal/diag"
)

type Summary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Notes    int `json:"notes"`
}

func Count(ds []diag.Diagnostic) Summary {
	var s Summary
	for _, d := range ds {
		switch d.Severity {
		case diag.SeverityError:
			s.Errors++
		case diag.SeverityWarning:
			s.Warnings++
		default:
			s.Notes++
		}
	}
	return s
}

func Write(w io.Writer, format, module, digest string, ds []diag.Diagnostic) error {
	diag.Sort(ds)
	switch format {
	case "text", "":
		for _, d := range ds {
			if _, err := fmt.Fprintln(w, d.String()); err != nil {
				return err
			}
		}
		s := Count(ds)
		_, err := fmt.Fprintf(w, "%s: %d error(s), %d warning(s), %d note(s) [%s]\n", module, s.Errors, s.Warnings, s.Notes, digest)
		return err
	case "json":
		return writeJSON(w, module, digest, ds)
	case "sarif":
		return writeSARIF(w, module, ds)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
}

func writeJSON(w io.Writer, module, digest string, ds []diag.Diagnostic) error {
	payload := struct {
		Schema      string            `json:"schema"`
		Module      string            `json:"module"`
		Digest      string            `json:"digest"`
		Summary     Summary           `json:"summary"`
		Diagnostics []diag.Diagnostic `json:"diagnostics"`
	}{"gridspec.diagnostics/v1", module, digest, Count(ds), ds}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

type sarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID string `json:"id"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}
type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}
type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region,omitempty"`
}
type sarifArtifact struct {
	URI string `json:"uri"`
}
type sarifRegion struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
}

func writeSARIF(w io.Writer, module string, ds []diag.Diagnostic) error {
	rules := make(map[string]bool)
	var results []sarifResult
	for _, d := range ds {
		rules[d.Rule] = true
		level := "note"
		if d.Severity == diag.SeverityError {
			level = "error"
		} else if d.Severity == diag.SeverityWarning {
			level = "warning"
		}
		result := sarifResult{RuleID: d.Rule, Level: level, Message: sarifMessage{Text: d.Message}}
		if d.Location.File != "" {
			result.Locations = []sarifLocation{{PhysicalLocation: sarifPhysical{ArtifactLocation: sarifArtifact{URI: d.Location.File}, Region: sarifRegion{StartLine: d.Location.Line, StartColumn: d.Location.Column}}}}
		}
		results = append(results, result)
	}
	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sarifRules := make([]sarifRule, len(ids))
	for i, id := range ids {
		sarifRules[i] = sarifRule{ID: id}
	}
	log := sarifLog{Version: "2.1.0", Schema: "https://json.schemastore.org/sarif-2.1.0.json", Runs: []sarifRun{{Tool: sarifTool{Driver: sarifDriver{Name: "gridspec", Rules: sarifRules}}, Results: results}}}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(log)
}
