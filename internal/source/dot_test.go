package source

import (
	"testing"
)

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
