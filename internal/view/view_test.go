package view

import (
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/specstate"
)

func TestDOTUsesIDLabelsAndKindShapes(t *testing.T) {
	state := specstate.State{
		Module:  "example.test/view",
		Profile: "default",
		Entities: map[string]specstate.Entity{
			specstate.Key("graph", "graph.verify"): {
				Kind: "graph", ID: "graph.verify",
				Fields: map[string]any{"title": "Verify specifications", "summary": "Checks the accepted specification.", "kind": "algorithm", "status": "active", "directed": true},
			},
			specstate.Key("node", "verify.input"): {
				Kind: "node", ID: "verify.input",
				Fields: map[string]any{"title": "Accepted state", "summary": "The selected CAS state.", "graph_id": "graph.verify", "kind": "start", "status": "active", "url": "docs/verify.md#input", "attrs": map[string]any{"specdot_role": "input"}},
			},
		},
	}

	for name, test := range map[string]struct {
		graph string
		want  string
	}{
		"overview": {want: `"id:graph.verify" [label="graph.verify",shape=box3d,tooltip="graph.verify: Checks the accepted specification."]`},
		"detail":   {graph: "graph.verify", want: `"id:verify.input" [label="verify.input",shape=circle,tooltip="verify.input: The selected CAS state.",URL="docs/verify.md#input"]`},
	} {
		t.Run(name, func(t *testing.T) {
			dot, err := DOT(state, test.graph)
			if err != nil {
				t.Fatal(err)
			}
			text := string(dot)
			if !strings.Contains(text, test.want) {
				t.Fatalf("DOT does not use the ID label and kind shape %q:\n%s", test.want, text)
			}
			if strings.Contains(text, `\\n`) || strings.Contains(text, "shape=record") {
				t.Fatalf("DOT retains the old compound label format:\n%s", text)
			}
		})
	}
}

func TestDOTLabelFormatSelectsEntityFields(t *testing.T) {
	state := specstate.State{
		Module: "example.test/view", Profile: "default",
		Entities: map[string]specstate.Entity{
			specstate.Key("graph", "graph.verify"): {
				Kind: "graph", ID: "graph.verify",
				Fields: map[string]any{"title": "Verify specifications", "kind": "algorithm", "status": "active", "directed": true},
			},
			specstate.Key("node", "verify.input"): {
				Kind: "node", ID: "verify.input",
				Fields: map[string]any{"title": "Accepted state", "graph_id": "graph.verify", "kind": "start", "status": "active", "attrs": map[string]any{"specdot_role": "input"}},
			},
		},
	}

	tests := map[string]struct {
		format string
		want   string
	}{
		"title":   {format: "{title}", want: `label="Accepted state"`},
		"mixed":   {format: "{title} [{id}]", want: `label="Accepted state [verify.input]"`},
		"nested":  {format: "{attrs.specdot_role}", want: `label="input"`},
		"missing": {format: "{attrs.absent}", want: `label="verify.input"`},
		"literal": {format: "{{{kind}}}", want: `label="{start}"`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dot, err := DOTWithOptions(state, DOTOptions{Graph: "graph.verify", LabelFormat: test.format})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(dot), test.want) {
				t.Fatalf("DOT does not contain %q:\n%s", test.want, dot)
			}
		})
	}
}

func TestDOTRejectsMalformedLabelFormat(t *testing.T) {
	for _, format := range []string{"{title", "title}", "{}", "{attrs..role}"} {
		if err := ValidateLabelFormat(format); err == nil {
			t.Errorf("ValidateLabelFormat(%q) succeeded", format)
		}
	}
}

func TestKnownKindsHaveDistinctShapes(t *testing.T) {
	seen := make(map[string]string, len(shapesByKind))
	for kind, shape := range shapesByKind {
		if previous := seen[shape]; previous != "" {
			t.Errorf("kinds %q and %q both use shape %q", previous, kind, shape)
		}
		seen[shape] = kind
	}
	if shape := shapeForKind("future-kind"); shape != "plaintext" {
		t.Fatalf("unknown kind shape = %q", shape)
	}
}
