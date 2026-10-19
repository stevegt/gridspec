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
	output.WriteString(", labelloc=t];\n  node [shape=plaintext];\n")
	if selectGraph == "" {
		for _, graph := range ir.Graphs {
			fmt.Fprintf(&output, "  %s [label=%s,shape=%s];\n", dotID(graph.ID), strconv.Quote(graph.ID), shapeForKind(graph.Kind))
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
				fmt.Fprintf(&output, "  %s [label=%s,shape=%s];\n", dotID(node.ID), strconv.Quote(node.ID), shapeForKind(node.Kind))
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

var shapesByKind = map[string]string{
	"agent":     "hexagon",
	"algorithm": "box3d",
	"data":      "folder",
	"decision":  "diamond",
	"effect":    "parallelogram",
	"final":     "doublecircle",
	"machine":   "component",
	"outcome":   "octagon",
	"process":   "box",
	"promise":   "house",
	"resource":  "cylinder",
	"scenario":  "note",
	"start":     "circle",
	"state":     "ellipse",
	"step":      "trapezium",
	"type":      "tab",
}

func shapeForKind(kind string) string {
	if shape := shapesByKind[kind]; shape != "" {
		return shape
	}
	return "plaintext"
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
