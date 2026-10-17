package source

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/model"
	"gonum.org/v1/gonum/graph/formats/dot"
	dotast "gonum.org/v1/gonum/graph/formats/dot/ast"
)

func parseDOT(f File) ([]model.Graph, []diag.Diagnostic) {
	file, err := dot.Parse(bytes.NewReader(f.Data))
	if err != nil {
		return nil, []diag.Diagnostic{{
			Rule: "SDV001", Severity: diag.SeverityError,
			Message:  "invalid DOT syntax: " + err.Error(),
			Location: diag.Location{File: f.Rel, Line: 1, Column: 1}, Waivable: false,
		}}
	}
	idx := newLineIndex(f.Rel, f.Data)
	graphs := make([]model.Graph, 0, len(file.Graphs))
	var ds []diag.Diagnostic
	for ordinal, graphAST := range file.Graphs {
		graph, graphDS := compileGraph(graphAST, f.Rel, ordinal, idx)
		graphs = append(graphs, graph)
		ds = append(ds, graphDS...)
	}
	return graphs, ds
}

type dotDefaults struct {
	node map[string]string
	edge map[string]string
}

type dotGraphBuilder struct {
	graph       model.Graph
	idx         lineIndex
	dotToID     map[string]string
	declaredDot map[string]model.Location
	pending     []pendingEdge
	ds          []diag.Diagnostic
}

type pendingEdge struct {
	element model.Element
	fromDot string
	toDot   string
}

func compileGraph(g *dotast.Graph, path string, ordinal int, idx lineIndex) (model.Graph, []diag.Diagnostic) {
	attrs := make(map[string]string)
	collectGraphAttrs(g.Stmts, attrs)
	dotID := unquoteDOT(g.ID)
	id := semanticID(attrs)
	if id == "" {
		id = fmt.Sprintf("missing.graph.%d", ordinal+1)
	}
	kind := attrs["specdot_kind"]
	status := defaultString(attrs["specdot_status"], "active")
	loc := idx.find(attrs["specdot_id"], attrs["id"], dotID)
	b := &dotGraphBuilder{
		graph: model.Graph{ID: id, DotID: dotID, Kind: kind, Status: status, Directed: g.Directed, Strict: g.Strict, URL: attrs["URL"], Attrs: attrs, Location: loc},
		idx:   idx, dotToID: make(map[string]string), declaredDot: make(map[string]model.Location),
	}
	if semanticID(attrs) == "" {
		b.add("SDV010", diag.SeverityError, loc, "graph is missing a semantic id attribute", "add graph [specdot_id=\"graph.<name>\"]", "", false)
	}
	if attrs["specdot_version"] != "1" {
		b.add("SDV011", diag.SeverityError, loc, "graph must declare specdot_version=1", "add graph [specdot_version=\"1\"]", id, false)
	}
	if kind == "" {
		b.add("SDV012", diag.SeverityError, loc, "graph is missing specdot_kind", "choose promise, machine, algorithm, data, scenario, or decision", id, false)
	} else if !validGraphKind(kind) {
		b.add("SDV013", diag.SeverityError, loc, fmt.Sprintf("unknown graph kind %q", kind), "choose promise, machine, algorithm, data, scenario, or decision", id, false)
	}
	if (kind == "machine" || kind == "algorithm" || kind == "promise" || kind == "scenario") && !g.Directed {
		b.add("SDV014", diag.SeverityError, loc, "executable and promise graphs must be directed", "use digraph", id, false)
	}
	b.walk(g.Stmts, dotDefaults{node: map[string]string{}, edge: map[string]string{}})
	for i := range b.pending {
		p := &b.pending[i]
		from, fromOK := b.dotToID[p.fromDot]
		to, toOK := b.dotToID[p.toDot]
		if !fromOK {
			b.add("SDV020", diag.SeverityError, p.element.Location, fmt.Sprintf("edge endpoint %q has no explicit node declaration", p.fromDot), "declare the node and give it a semantic id", p.element.ID, false)
			from = "missing.node." + p.fromDot
		}
		if !toOK {
			b.add("SDV020", diag.SeverityError, p.element.Location, fmt.Sprintf("edge endpoint %q has no explicit node declaration", p.toDot), "declare the node and give it a semantic id", p.element.ID, false)
			to = "missing.node." + p.toDot
		}
		p.element.From, p.element.To = from, to
		b.graph.Edges = append(b.graph.Edges, p.element)
	}
	return b.graph, b.ds
}

func collectGraphAttrs(stmts []dotast.Stmt, attrs map[string]string) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dotast.AttrStmt:
			if strings.EqualFold(fmt.Sprint(s.Kind), "graph") {
				mergeAttrs(attrs, s.Attrs)
			}
		case *dotast.Attr:
			attrs[unquoteDOT(s.Key)] = unquoteDOT(s.Val)
		}
	}
}

func (b *dotGraphBuilder) walk(stmts []dotast.Stmt, defaults dotDefaults) {
	local := dotDefaults{node: cloneMap(defaults.node), edge: cloneMap(defaults.edge)}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dotast.AttrStmt:
			switch strings.ToLower(fmt.Sprint(s.Kind)) {
			case "node":
				mergeAttrs(local.node, s.Attrs)
			case "edge":
				mergeAttrs(local.edge, s.Attrs)
			}
		case *dotast.NodeStmt:
			b.node(s, local.node)
		case *dotast.EdgeStmt:
			b.edge(s, local.edge)
		case *dotast.Subgraph:
			b.walk(s.Stmts, local)
		}
	}
}

func (b *dotGraphBuilder) node(n *dotast.NodeStmt, defaults map[string]string) {
	attrs := cloneMap(defaults)
	mergeAttrs(attrs, n.Attrs)
	dotID := unquoteDOT(n.Node.ID)
	id := semanticID(attrs)
	loc := b.idx.find(attrs["specdot_id"], attrs["id"], dotID)
	if _, exists := b.declaredDot[dotID]; exists {
		b.add("SDV021", diag.SeverityError, loc, fmt.Sprintf("DOT node %q is declared more than once", dotID), "merge attributes into one node declaration", id, false)
	} else {
		b.declaredDot[dotID] = loc
	}
	if id == "" {
		id = "missing.node." + dotID
		b.add("SDV022", diag.SeverityError, loc, fmt.Sprintf("node %q is missing a semantic id", dotID), "add specdot_id (or id) with a module-wide unique value", "", false)
	}
	kind := attrs["specdot_kind"]
	if kind == "" {
		kind = inferredElementKind(b.graph.Kind, "node")
	}
	e := model.Element{
		ID: id, DotID: dotID, GraphID: b.graph.ID, Element: "node", Kind: kind,
		Status: defaultString(attrs["specdot_status"], b.graph.Status), URL: attrs["URL"], Attrs: attrs, Location: loc,
	}
	b.dotToID[dotID] = id
	b.graph.Nodes = append(b.graph.Nodes, e)
}

func (b *dotGraphBuilder) edge(e *dotast.EdgeStmt, defaults map[string]string) {
	attrs := cloneMap(defaults)
	mergeAttrs(attrs, e.Attrs)
	id := semanticID(attrs)
	loc := b.idx.find(attrs["specdot_id"], attrs["id"])
	if id == "" {
		id = fmt.Sprintf("missing.edge.%s.%d", b.graph.ID, len(b.pending)+1)
		b.add("SDV023", diag.SeverityError, loc, "edge is missing a semantic id", "add specdot_id (or id) with a module-wide unique value", "", false)
	}
	from, ok := vertexNodeID(e.From)
	if !ok {
		b.add("SDV024", diag.SeverityError, loc, "subgraph edge endpoints are not semantic enough for specdot", "connect explicitly declared nodes", id, false)
		return
	}
	segments := 0
	for next := e.To; next != nil; next = next.To {
		to, ok := vertexNodeID(next.Vertex)
		if !ok {
			b.add("SDV024", diag.SeverityError, loc, "subgraph edge endpoints are not semantic enough for specdot", "connect explicitly declared nodes", id, false)
			return
		}
		segments++
		segmentID := id
		if segments > 1 {
			segmentID = fmt.Sprintf("%s#%d", id, segments)
		}
		kind := attrs["specdot_kind"]
		if kind == "" {
			kind = inferredElementKind(b.graph.Kind, "edge")
		}
		b.pending = append(b.pending, pendingEdge{
			element: model.Element{
				ID: segmentID, GraphID: b.graph.ID, Element: "edge", Kind: kind,
				Status: defaultString(attrs["specdot_status"], b.graph.Status), URL: attrs["URL"], Attrs: cloneMap(attrs), Location: loc,
			},
			fromDot: from, toDot: to,
		})
		from = to
	}
	if segments > 1 {
		b.add("SDV025", diag.SeverityError, loc, "a chained edge cannot share one semantic edge id", "split the chain into separately identified edge statements", id, false)
	}
}

func vertexNodeID(v dotast.Vertex) (string, bool) {
	switch n := v.(type) {
	case *dotast.Node:
		return unquoteDOT(n.ID), true
	default:
		return "", false
	}
}

func mergeAttrs(dst map[string]string, attrs []*dotast.Attr) {
	for _, attr := range attrs {
		dst[unquoteDOT(attr.Key)] = unquoteDOT(attr.Val)
	}
}

func cloneMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func semanticID(attrs map[string]string) string {
	if id := attrs["specdot_id"]; id != "" {
		return id
	}
	return attrs["id"]
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func validGraphKind(kind string) bool {
	switch kind {
	case "promise", "machine", "algorithm", "data", "scenario", "decision":
		return true
	default:
		return false
	}
}

func inferredElementKind(graphKind, element string) string {
	if element == "edge" {
		switch graphKind {
		case "promise":
			return "promise"
		case "machine":
			return "transition"
		case "algorithm":
			return "flow"
		case "data":
			return "relationship"
		case "scenario":
			return "step"
		default:
			return "edge"
		}
	}
	switch graphKind {
	case "promise":
		return "agent"
	case "machine":
		return "state"
	case "algorithm":
		return "step"
	case "data":
		return "type"
	case "scenario":
		return "state"
	default:
		return "node"
	}
}

func (b *dotGraphBuilder) add(rule string, severity diag.Severity, loc model.Location, message, help, id string, waivable bool) {
	b.ds = append(b.ds, diag.Diagnostic{
		Rule: rule, Severity: severity, Message: message, Help: help, ElementID: id, Waivable: waivable,
		Location: diag.Location{File: loc.File, Line: loc.Line, Column: loc.Column},
	})
}
