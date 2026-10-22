package specdiff

import (
	"bytes"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/specstate"
)

func TestCompareRecursesAndSortsJSONPointerPaths(t *testing.T) {
	accepted := specstate.State{Module: "example.test", Profile: "default", Entities: map[string]specstate.Entity{
		specstate.Key("graph", "graph/a"): {Kind: "graph", ID: "graph/a", Fields: map[string]any{"status": "active", "attrs": map[string]any{"z": "old"}}},
		specstate.Key("node", "node.old"): {Kind: "node", ID: "node.old", Fields: map[string]any{"status": "active"}},
	}}
	proposed := accepted.Clone()
	graph := proposed.Entities[specstate.Key("graph", "graph/a")]
	graph.Fields = map[string]any{"status": "active", "attrs": map[string]any{"a": "new", "z": "changed"}}
	proposed.Entities[specstate.Key("graph", "graph/a")] = graph
	delete(proposed.Entities, specstate.Key("node", "node.old"))
	proposed.Entities[specstate.Key("node", "node.new")] = specstate.Entity{Kind: "node", ID: "node.new", Fields: map[string]any{"status": "active"}}
	report, err := Compare(accepted, proposed)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(report.Changes))
	for i, change := range report.Changes {
		paths[i] = change.Path
	}
	want := []string{
		"/entities/graph/graph~1a/fields/attrs/a",
		"/entities/graph/graph~1a/fields/attrs/z",
		"/entities/node/node.new",
		"/entities/node/node.old",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths=%v", paths)
	}
	var text bytes.Buffer
	if err := Write(&text, "text", report); err != nil || !strings.Contains(text.String(), "~ /entities/graph/graph~1a/fields/attrs/z") {
		t.Fatalf("text=%q err=%v", text.String(), err)
	}
	var encoded bytes.Buffer
	if err := Write(&encoded, "json", report); err != nil || !strings.Contains(encoded.String(), `"schema": "gridspec.diff/v1"`) {
		t.Fatalf("json=%q err=%v", encoded.String(), err)
	}
}
