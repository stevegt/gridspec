package source

import (
	"testing"

	"github.com/promisegrid/specdot/internal/diag"
)

func TestDOTFileContainsExactlyOneGraph(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "empty", data: "", want: 0},
		{name: "one", data: `digraph G { graph [specdot_version="1", specdot_kind="algorithm", specdot_id="graph.one"]; }`, want: 1},
		{name: "two", data: "digraph A {}\ndigraph B {}", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graphs, diagnostics := parseDOT(File{Rel: "test.dot", Kind: "dot", Data: []byte(tt.data)})
			if len(graphs) != tt.want {
				t.Fatalf("len(graphs) = %d, want %d", len(graphs), tt.want)
			}
			if tt.name == "one" {
				if hasDiagnostic(diagnostics, "SDV002") {
					t.Fatalf("unexpected SDV002: %#v", diagnostics)
				}
			} else if !hasDiagnostic(diagnostics, "SDV002") {
				t.Fatalf("expected SDV002, got %#v", diagnostics)
			}
		})
	}
}

func hasDiagnostic(ds []diag.Diagnostic, rule string) bool {
	for _, d := range ds {
		if d.Rule == rule {
			return true
		}
	}
	return false
}

func TestDOTParserPreservesRepeatedDeclarations(t *testing.T) {
	data := []byte(`digraph G {
  graph [specdot_version="1", specdot_kind="algorithm", specdot_id="graph.test"];
  n [specdot_id="node.one"];
  n [specdot_id="node.two"];
}`)
	graphs, diagnostics := parseDOT(File{Rel: "test.dot", Kind: "dot", Data: data})
	if len(graphs) != 1 || len(graphs[0].Nodes) != 2 {
		t.Fatalf("graphs = %#v", graphs)
	}
	found := false
	for _, d := range diagnostics {
		if d.Rule == "SDV021" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected repeated-node diagnostic, got %#v", diagnostics)
	}
}

func TestChainedEdgeNeedsSegmentIDs(t *testing.T) {
	data := []byte(`digraph G {
  graph [specdot_version="1", specdot_kind="algorithm", specdot_id="graph.test"];
  a [specdot_id="node.a"];
  b [specdot_id="node.b"];
  c [specdot_id="node.c"];
  a -> b -> c [specdot_id="edge.chain"];
}`)
	_, diagnostics := parseDOT(File{Rel: "test.dot", Kind: "dot", Data: data})
	found := false
	for _, d := range diagnostics {
		if d.Rule == "SDV025" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected chained-edge diagnostic, got %#v", diagnostics)
	}
}
