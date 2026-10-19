package rules

import (
	"fmt"

	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
)

func checkFlowGraphs(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	for _, graph := range ctx.IR.Graphs {
		if graph.Kind != "algorithm" && graph.Kind != "scenario" {
			continue
		}
		nodes := make(map[string]model.Element)
		incoming, outgoing := make(map[string][]model.Element), make(map[string][]model.Element)
		var starts, finals []string
		for _, node := range graph.Nodes {
			nodes[node.ID] = node
			if truthy(node.Attrs["specdot_initial"]) || node.Attrs["specdot_role"] == "input" || node.Kind == "start" {
				starts = append(starts, node.ID)
			}
			if truthy(node.Attrs["specdot_final"]) || node.Attrs["specdot_role"] == "output" || node.Kind == "final" {
				finals = append(finals, node.ID)
			}
		}
		for _, edge := range graph.Edges {
			if edge.Status != "active" {
				continue
			}
			outgoing[edge.From] = append(outgoing[edge.From], edge)
			incoming[edge.To] = append(incoming[edge.To], edge)
		}
		if len(starts) == 0 {
			ds = append(ds, diagnostic("SDV300", diag.SeverityError, graph.Location, "flow graph has no initial/input node", "mark at least one node specdot_initial=true or specdot_role=input", graph.ID, false))
		}
		if len(finals) == 0 {
			ds = append(ds, diagnostic("SDV301", diag.SeverityError, graph.Location, "flow graph has no final/output node", "mark at least one terminal node specdot_final=true or specdot_role=output", graph.ID, false))
		}
		reachable := graphReachable(starts, outgoing)
		coreachable := reverseReachable(finals, incoming)
		for _, node := range graph.Nodes {
			if node.Status != "active" {
				continue
			}
			if !reachable[node.ID] {
				ds = append(ds, diagnostic("SDV302", diag.SeverityError, node.Location, "node is unreachable from every initial/input node", "connect it or remove it", node.ID, true))
			}
			if !coreachable[node.ID] {
				ds = append(ds, diagnostic("SDV303", diag.SeverityError, node.Location, "node has no path to a final/output node", "add success/failure termination paths or mark an intentional loop", node.ID, true))
			}
			isFinal := hasString(finals, node.ID)
			if !isFinal && len(outgoing[node.ID]) == 0 {
				ds = append(ds, diagnostic("SDV304", diag.SeverityError, node.Location, "non-final flow node has no outgoing edge", "account for success, failure, refusal, and unknown outcomes", node.ID, false))
			}
			if isFinal && len(outgoing[node.ID]) != 0 && !truthy(node.Attrs["specdot_restart"]) {
				ds = append(ds, diagnostic("SDV305", diag.SeverityWarning, node.Location, "final node has outgoing flow", "mark specdot_restart=true when this is an intentional restart", node.ID, true))
			}
			if node.Kind == "decision" {
				if len(outgoing[node.ID]) < 2 {
					ds = append(ds, diagnostic("SDV306", diag.SeverityError, node.Location, "decision has fewer than two outcomes", "add identified branches", node.ID, false))
				}
				branches := make(map[string]bool)
				for _, edge := range outgoing[node.ID] {
					branch := edge.Attrs["specdot_branch"]
					if branch == "" {
						ds = append(ds, diagnostic("SDV307", diag.SeverityError, edge.Location, "decision edge has no specdot_branch", "name every outcome; do not rely on label or color", edge.ID, false))
					} else if branches[branch] {
						ds = append(ds, diagnostic("SDV308", diag.SeverityError, edge.Location, fmt.Sprintf("decision branch %q is duplicated", branch), "make branch outcomes mutually distinguishable", edge.ID, false))
					}
					branches[branch] = true
				}
			}
		}
		_ = nodes
	}
	return ds
}

func graphReachable(starts []string, outgoing map[string][]model.Element) map[string]bool {
	seen := make(map[string]bool)
	queue := append([]string(nil), starts...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		for _, edge := range outgoing[current] {
			queue = append(queue, edge.To)
		}
	}
	return seen
}

func reverseReachable(finals []string, incoming map[string][]model.Element) map[string]bool {
	seen := make(map[string]bool)
	queue := append([]string(nil), finals...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		for _, edge := range incoming[current] {
			queue = append(queue, edge.From)
		}
	}
	return seen
}
