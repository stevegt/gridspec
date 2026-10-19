// Package view derives human-facing text and Graphviz projections from an
// accepted GridSpec state. Views are never authoritative inputs.
package view

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

// Text renders a deterministic full-state transaction projection.
func Text(state specstate.State) ([]byte, error) {
	module, profile := state.Module, state.Profile
	keys := make([]string, 0, len(state.Entities))
	for key := range state.Entities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	program := txnlang.Program{Module: &module, Profile: &profile}
	for _, key := range keys {
		entity := state.Entities[key]
		program.Ops = append(program.Ops, txnlang.Operation{
			Action: "put", Kind: entity.Kind, ID: entity.ID, Fields: entity.Fields,
		})
	}
	return txnlang.Format(program)
}

// DOT renders either a compact module overview or one selected graph. The
// projection deliberately excludes contracts and most attributes.
func DOT(state specstate.State, selectGraph string) ([]byte, error) {
	ir, _, _, err := state.SemanticView()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.WriteString("digraph gridspec_view {\n")
	output.WriteString("  graph [label=")
	output.WriteString(strconv.Quote(state.Module + " (generated view)"))
	output.WriteString(", labelloc=t];\n  node [shape=box];\n")
	if selectGraph == "" {
		for _, graph := range ir.Graphs {
			label := fmt.Sprintf("%s\\n%s\\n%d nodes, %d edges", graph.ID, graph.Kind, len(graph.Nodes), len(graph.Edges))
			fmt.Fprintf(&output, "  %s [label=%s];\n", dotID(graph.ID), strconv.Quote(label))
		}
		for _, graph := range ir.Graphs {
			for _, dependency := range splitList(graph.Attrs["specdot_depends_on"]) {
				fmt.Fprintf(&output, "  %s -> %s [style=dashed,label=\"depends\"];\n", dotID(graph.ID), dotID(dependency))
			}
		}
	} else {
		found := false
		for _, graph := range ir.Graphs {
			if graph.ID != selectGraph {
				continue
			}
			found = true
			for _, node := range graph.Nodes {
				label := node.ID
				if node.Kind != "" {
					label += "\\n" + node.Kind
				}
				fmt.Fprintf(&output, "  %s [label=%s];\n", dotID(node.ID), strconv.Quote(label))
			}
			for _, edge := range graph.Edges {
				fmt.Fprintf(&output, "  %s -> %s [label=%s];\n", dotID(edge.From), dotID(edge.To), strconv.Quote(edge.ID))
			}
		}
		if !found {
			return nil, fmt.Errorf("graph %q does not exist", selectGraph)
		}
	}
	output.WriteString("}\n")
	return output.Bytes(), nil
}

func dotID(value string) string { return strconv.Quote("id:" + value) }

func splitList(value string) []string {
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
