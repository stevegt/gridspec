package describe

import (
	"context"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

func TestGenerateBuildsDescriptionOnlyTransaction(t *testing.T) {
	ctx := context.Background()
	state := builtState(t, map[string]specstate.Entity{
		specstate.Key("graph", "graph.test"): {
			Kind: "graph", ID: "graph.test",
			Fields: map[string]any{"kind": "algorithm", "status": "active", "directed": true, "summary": "Existing graph summary.", "attrs": map[string]any{"owner": "team"}},
		},
		specstate.Key("node", "node.start"): {
			Kind: "node", ID: "node.start",
			Fields: map[string]any{"graph_id": "graph.test", "kind": "start", "status": "active", "title": "Existing title"},
		},
		specstate.Key("node", "node.draft"): {
			Kind: "node", ID: "node.draft",
			Fields: map[string]any{"graph_id": "graph.test", "kind": "step", "status": "draft"},
		},
	})
	response := `{"descriptions":[` +
		`{"kind":"graph","id":"graph.test","title":"Test workflow","summary":"Exercises description generation."},` +
		`{"kind":"node","id":"node.start","title":"Replacement title","summary":"Begins the workflow."}` +
		`]}`
	result, err := Generate(ctx, provider.Static{Text: response}, state, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 || result.Noop {
		t.Fatalf("result = %#v", result)
	}
	program, err := txnlang.Parse(result.Program)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := specstate.Apply(state, program)
	if err != nil {
		t.Fatal(err)
	}
	graph := updated.Entities[specstate.Key("graph", "graph.test")]
	if graph.Fields["title"] != "Test workflow" || graph.Fields["summary"] != "Existing graph summary." {
		t.Fatalf("graph fields = %#v", graph.Fields)
	}
	if graph.Fields["attrs"].(map[string]any)["owner"] != "team" {
		t.Fatalf("unrelated graph fields were changed: %#v", graph.Fields)
	}
	node := updated.Entities[specstate.Key("node", "node.start")]
	if node.Fields["title"] != "Existing title" || node.Fields["summary"] != "Begins the workflow." {
		t.Fatalf("existing title was not preserved: %#v", node.Fields)
	}
	if _, described := updated.Entities[specstate.Key("node", "node.draft")].Fields["title"]; described {
		t.Fatal("draft node was described")
	}
	audit, err := Audit(state, result)
	if err != nil || !strings.Contains(string(audit), `"operation":"gridspec_describe"`) || !strings.Contains(string(audit), "Test workflow") {
		t.Fatalf("audit=%s err=%v", audit, err)
	}
}

func TestGenerateRejectsIncompleteOrUnsafeResponses(t *testing.T) {
	state := builtState(t, map[string]specstate.Entity{
		specstate.Key("graph", "graph.test"): {
			Kind: "graph", ID: "graph.test", Fields: map[string]any{"kind": "algorithm", "status": "active"},
		},
	})
	tests := map[string]string{
		"omitted":       `{"descriptions":[]}`,
		"extra":         `{"descriptions":[{"kind":"graph","id":"other","title":"Other","summary":"Not requested."}]}`,
		"duplicate":     `{"descriptions":[{"kind":"graph","id":"graph.test","title":"One","summary":"First."},{"kind":"graph","id":"graph.test","title":"Two","summary":"Second."}]}`,
		"long title":    `{"descriptions":[{"kind":"graph","id":"graph.test","title":"This title is deliberately longer than forty characters","summary":"Short."}]}`,
		"unknown field": `{"descriptions":[{"kind":"graph","id":"graph.test","title":"Test","summary":"Short.","other":true}]}`,
		"trailing":      `{"descriptions":[]} {}`,
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Generate(context.Background(), provider.Static{Text: response}, state, ""); err == nil {
				t.Fatal("Generate succeeded")
			}
		})
	}
}

func TestGenerateNoopDoesNotCallProvider(t *testing.T) {
	state := builtState(t, map[string]specstate.Entity{
		specstate.Key("graph", "graph.test"): {
			Kind: "graph", ID: "graph.test",
			Fields: map[string]any{"kind": "algorithm", "status": "active", "title": "Test", "summary": "Already described."},
		},
	})
	result, err := Generate(context.Background(), nil, state, "")
	if err != nil || !result.Noop {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, err := Generate(context.Background(), nil, state, "missing"); err == nil {
		t.Fatal("missing graph succeeded")
	}
}

func builtState(t *testing.T, entities map[string]specstate.Entity) specstate.State {
	t.Helper()
	state, err := specstate.Build(context.Background(), cas.NewMemory(), specstate.State{
		Module: "example.test/descriptions", Profile: "default", Entities: entities,
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}
