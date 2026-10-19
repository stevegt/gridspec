// Package view derives human-facing text and Graphviz projections from an
// accepted GridSpec state. Views are never authoritative inputs.
package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/promisegrid/gridspec/internal/model"
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

type DOTOptions struct {
	Graph       string
	LabelFormat string
}

// DOT renders either a compact module overview or one selected graph. The
// projection deliberately excludes contracts and most attributes.
func DOT(state specstate.State, selectGraph string) ([]byte, error) {
	return DOTWithOptions(state, DOTOptions{Graph: selectGraph})
}

// DOTWithOptions renders a DOT projection with a deterministic label template.
// The empty template is equivalent to {id}.
func DOTWithOptions(state specstate.State, options DOTOptions) ([]byte, error) {
	template, err := parseLabelTemplate(options.LabelFormat)
	if err != nil {
		return nil, err
	}
	ir, _, _, err := state.SemanticView()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.WriteString("digraph gridspec_view {\n")
	output.WriteString("  graph [label=")
	output.WriteString(strconv.Quote(state.Module + " (generated view)"))
	output.WriteString(", labelloc=t];\n  node [shape=plaintext];\n")
	if options.Graph == "" {
		for _, graph := range ir.Graphs {
			label := template.render(graph.ID, entityFields(state, "graph", graph.ID, map[string]any{
				"dot_id": graph.DotID, "title": graph.Title, "summary": graph.Summary, "kind": graph.Kind,
				"status": graph.Status, "directed": graph.Directed, "strict": graph.Strict, "url": graph.URL, "attrs": graph.Attrs,
			}))
			writeNode(&output, graph.ID, label, graph.ID, graph.Summary, graph.URL, shapeForKind(graph.Kind))
		}
		for _, graph := range ir.Graphs {
			for _, dependency := range splitList(graph.Attrs["specdot_depends_on"]) {
				fmt.Fprintf(&output, "  %s -> %s [style=dashed,label=\"depends\"];\n", dotID(graph.ID), dotID(dependency))
			}
		}
	} else {
		found := false
		for _, graph := range ir.Graphs {
			if graph.ID != options.Graph {
				continue
			}
			found = true
			for _, node := range graph.Nodes {
				label := template.render(node.ID, elementFields(state, node))
				writeNode(&output, node.ID, label, node.ID, node.Summary, node.URL, shapeForKind(node.Kind))
			}
			for _, edge := range graph.Edges {
				label := template.render(edge.ID, elementFields(state, edge))
				writeEdge(&output, edge.From, edge.To, label, edge.ID, edge.Summary, edge.URL)
			}
		}
		if !found {
			return nil, fmt.Errorf("graph %q does not exist", options.Graph)
		}
	}
	output.WriteString("}\n")
	return output.Bytes(), nil
}

func dotID(value string) string { return strconv.Quote("id:" + value) }

func writeNode(output *bytes.Buffer, id, label, semanticID, summary, rawURL, shape string) {
	fmt.Fprintf(output, "  %s [label=%s,shape=%s,tooltip=%s", dotID(id), strconv.Quote(label), shape, strconv.Quote(tooltip(semanticID, summary)))
	if rawURL != "" {
		fmt.Fprintf(output, ",URL=%s", strconv.Quote(rawURL))
	}
	output.WriteString("];\n")
}

func writeEdge(output *bytes.Buffer, from, to, label, semanticID, summary, rawURL string) {
	fmt.Fprintf(output, "  %s -> %s [label=%s,tooltip=%s", dotID(from), dotID(to), strconv.Quote(label), strconv.Quote(tooltip(semanticID, summary)))
	if rawURL != "" {
		fmt.Fprintf(output, ",URL=%s", strconv.Quote(rawURL))
	}
	output.WriteString("];\n")
}

func tooltip(id, summary string) string {
	if summary == "" {
		return id
	}
	return id + ": " + summary
}

func elementFields(state specstate.State, element model.Element) map[string]any {
	return entityFields(state, element.Element, element.ID, map[string]any{
		"dot_id": element.DotID, "title": element.Title, "summary": element.Summary,
		"graph_id": element.GraphID, "element": element.Element, "kind": element.Kind, "status": element.Status,
		"from": element.From, "to": element.To, "url": element.URL, "attrs": element.Attrs,
	})
}

func entityFields(state specstate.State, kind, id string, canonical map[string]any) map[string]any {
	result := make(map[string]any)
	if entity, ok := state.Entities[specstate.Key(kind, id)]; ok {
		for key, value := range entity.Fields {
			result[key] = value
		}
	}
	for key, value := range canonical {
		result[key] = value
	}
	result["id"] = id
	return result
}

type labelPart struct {
	literal string
	path    []string
}

type labelTemplate []labelPart

// ValidateLabelFormat checks label template syntax without rendering a state.
func ValidateLabelFormat(format string) error {
	_, err := parseLabelTemplate(format)
	return err
}

func parseLabelTemplate(format string) (labelTemplate, error) {
	if format == "" {
		format = "{id}"
	}
	var result labelTemplate
	var literal strings.Builder
	flush := func() {
		if literal.Len() != 0 {
			result = append(result, labelPart{literal: literal.String()})
			literal.Reset()
		}
	}
	for index := 0; index < len(format); {
		switch format[index] {
		case '{':
			if index+1 < len(format) && format[index+1] == '{' {
				literal.WriteByte('{')
				index += 2
				continue
			}
			close := strings.IndexByte(format[index+1:], '}')
			if close < 0 {
				return nil, fmt.Errorf("label format has an unmatched '{'")
			}
			close += index + 1
			path := format[index+1 : close]
			if path == "" || strings.TrimSpace(path) != path || strings.ContainsAny(path, "{}") {
				return nil, fmt.Errorf("invalid label field %q", path)
			}
			parts := strings.Split(path, ".")
			for _, part := range parts {
				if part == "" {
					return nil, fmt.Errorf("invalid label field %q", path)
				}
			}
			flush()
			result = append(result, labelPart{path: parts})
			index = close + 1
		case '}':
			if index+1 < len(format) && format[index+1] == '}' {
				literal.WriteByte('}')
				index += 2
				continue
			}
			return nil, fmt.Errorf("label format has an unmatched '}'")
		default:
			literal.WriteByte(format[index])
			index++
		}
	}
	flush()
	return result, nil
}

func (template labelTemplate) render(id string, fields map[string]any) string {
	var result strings.Builder
	for _, part := range template {
		if part.path == nil {
			result.WriteString(part.literal)
			continue
		}
		value, ok := fieldValue(fields, part.path)
		if !ok {
			return id
		}
		rendered, ok := renderValue(value)
		if !ok || rendered == "" {
			return id
		}
		result.WriteString(rendered)
	}
	if result.Len() == 0 {
		return id
	}
	return result.String()
}

func fieldValue(fields map[string]any, path []string) (any, bool) {
	var current any = fields
	for _, key := range path {
		switch values := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = values[key]
			if !ok {
				return nil, false
			}
		case map[string]string:
			value, ok := values[key]
			if !ok {
				return nil, false
			}
			current = value
		default:
			return nil, false
		}
	}
	return current, current != nil
}

func renderValue(value any) (string, bool) {
	if text, ok := value.(string); ok {
		return text, text != ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(encoded), len(encoded) != 0 && string(encoded) != "null"
}

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
