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
				Fields: map[string]any{"kind": "algorithm", "status": "active", "directed": true},
			},
			specstate.Key("node", "verify.input"): {
				Kind: "node", ID: "verify.input",
				Fields: map[string]any{"graph_id": "graph.verify", "kind": "start", "status": "active"},
			},
		},
	}

	for name, test := range map[string]struct {
		graph string
		want  string
	}{
		"overview": {want: `"id:graph.verify" [label="graph.verify",shape=box3d]`},
		"detail":   {graph: "graph.verify", want: `"id:verify.input" [label="verify.input",shape=circle]`},
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
