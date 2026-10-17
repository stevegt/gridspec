package rules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/expr"
	"github.com/promisegrid/specdot/internal/model"
)

func checkMachines(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	sends, receives := make(map[string]bool), make(map[string]bool)
	for _, graph := range ctx.IR.Graphs {
		if graph.Kind != "machine" {
			continue
		}
		contract := idx.Contracts[graph.ID]
		var machine model.MachineContract
		if contract.Machine != nil {
			machine = *contract.Machine
		}
		if owner, ok := idx.Elements[machine.Agent]; machine.Agent == "" || !ok || (owner.Kind != "agent" && owner.Kind != "superagent") {
			ds = append(ds, diagnostic("SDV348", diag.SeverityError, contract.Location, fmt.Sprintf("machine owner %q is not a declared agent", machine.Agent), "set machine.agent to an agent in the Promise graph", graph.ID, false))
		}
		env, err := expr.New(machine.Variables)
		if err != nil {
			ds = append(ds, diagnostic("SDV320", diag.SeverityError, contract.Location, "invalid machine variables: "+err.Error(), "use unique CEL identifiers and supported types", graph.ID, false))
			env, _ = expr.New(nil)
		}
		if machine.Semantics != "" && machine.Semantics != "run-to-completion" {
			ds = append(ds, diagnostic("SDV321", diag.SeverityError, contract.Location, fmt.Sprintf("unsupported machine semantics %q", machine.Semantics), "v1 statecharts use run-to-completion", graph.ID, false))
		}
		for _, variable := range machine.Variables {
			if err := validateVariable(variable); err != nil {
				ds = append(ds, diagnostic("SDV322", diag.SeverityError, contract.Location, err.Error(), "give checker variables finite, type-correct domains", graph.ID, false))
			}
		}
		nodes := make(map[string]model.Element)
		outgoing, incoming := make(map[string][]model.Element), make(map[string][]model.Element)
		regions := make(map[string][]model.Element)
		var initials, finals []string
		for _, state := range graph.Nodes {
			if state.Kind != "state" && state.Kind != "final" && state.Kind != "history" && state.Kind != "fork" && state.Kind != "join" {
				ds = append(ds, diagnostic("SDV323", diag.SeverityError, state.Location, fmt.Sprintf("node kind %q is not a statechart pseudostate", state.Kind), "use state, final, history, fork, or join", state.ID, false))
			}
			nodes[state.ID] = state
			parent := state.Attrs["specdot_parent"]
			region := state.Attrs["specdot_region"]
			regions[parent+"\x00"+region] = append(regions[parent+"\x00"+region], state)
			if truthy(state.Attrs["specdot_initial"]) {
				initials = append(initials, state.ID)
			}
			if truthy(state.Attrs["specdot_final"]) || state.Kind == "final" {
				finals = append(finals, state.ID)
			}
			for _, key := range []string{"specdot_entry", "specdot_exit"} {
				if _, err := env.CompileAction(state.Attrs[key]); err != nil {
					ds = append(ds, diagnostic("SDV339", diag.SeverityError, state.Location, fmt.Sprintf("invalid %s action: %v", strings.TrimPrefix(key, "specdot_"), err), "use semicolon-separated declared-variable assignments", state.ID, false))
				}
			}
		}
		for _, state := range graph.Nodes {
			if parent := state.Attrs["specdot_parent"]; parent != "" {
				if _, ok := nodes[parent]; !ok {
					ds = append(ds, diagnostic("SDV324", diag.SeverityError, state.Location, fmt.Sprintf("parent state %q does not exist", parent), "reference a state in the same machine", state.ID, false))
				} else if hierarchyCycle(state.ID, nodes) {
					ds = append(ds, diagnostic("SDV325", diag.SeverityError, state.Location, "state hierarchy contains a parent cycle", "make containment acyclic", state.ID, false))
				}
			}
		}
		for key, states := range regions {
			count := 0
			for _, state := range states {
				if truthy(state.Attrs["specdot_initial"]) {
					count++
				}
			}
			if count != 1 {
				parent, region, _ := strings.Cut(key, "\x00")
				ds = append(ds, diagnostic("SDV326", diag.SeverityError, graph.Location, fmt.Sprintf("state region parent=%q region=%q has %d initial states", parent, region, count), "declare exactly one initial state per region", graph.ID, false))
			}
		}
		declaredEvents := make(map[string]model.EventContract)
		for _, event := range machine.Events {
			if event.Name == "" || declaredEvents[event.Name].Name != "" {
				ds = append(ds, diagnostic("SDV327", diag.SeverityError, contract.Location, fmt.Sprintf("event %q is empty or duplicated", event.Name), "declare each accepted event once", graph.ID, false))
			}
			if event.Policy != "handled" && event.Policy != "deferred" && event.Policy != "refused" && event.Policy != "ignored" {
				ds = append(ds, diagnostic("SDV328", diag.SeverityError, contract.Location, fmt.Sprintf("event %q has invalid policy %q", event.Name, event.Policy), "use handled, deferred, refused, or ignored", graph.ID, false))
			}
			declaredEvents[event.Name] = event
		}
		for _, transition := range graph.Edges {
			if transition.Kind != "transition" {
				ds = append(ds, diagnostic("SDV329", diag.SeverityError, transition.Location, fmt.Sprintf("machine edge kind %q is not transition", transition.Kind), "set specdot_kind=transition", transition.ID, false))
			}
			outgoing[transition.From] = append(outgoing[transition.From], transition)
			incoming[transition.To] = append(incoming[transition.To], transition)
			event := transition.Attrs["specdot_event"]
			receive := transition.Attrs["specdot_receive"]
			if receive != "" && (event != "" || transition.Attrs["specdot_after"] != "") {
				ds = append(ds, diagnostic("SDV352", diag.SeverityError, transition.Location, "receive transition also declares an event or timer", "specdot_receive is the transition trigger", transition.ID, false))
			}
			if receive != "" {
				receives[receive] = true
				channel, ok := idx.Contracts[receive]
				if !ok || channel.Channel == nil {
					ds = append(ds, diagnostic("SDV353", diag.SeverityError, transition.Location, fmt.Sprintf("receive channel %q is not declared", receive), "reference a channel edge", transition.ID, false))
				} else if channel.Channel.To != machine.Agent {
					ds = append(ds, diagnostic("SDV354", diag.SeverityError, transition.Location, fmt.Sprintf("machine agent %q receives channel addressed to %q", machine.Agent, channel.Channel.To), "put the receive transition in the recipient agent's machine", transition.ID, false))
				}
			}
			for _, send := range split(transition.Attrs["specdot_send"]) {
				sends[send] = true
				channel, ok := idx.Contracts[send]
				if !ok || channel.Channel == nil {
					ds = append(ds, diagnostic("SDV353", diag.SeverityError, transition.Location, fmt.Sprintf("send channel %q is not declared", send), "reference a channel edge", transition.ID, false))
				} else if channel.Channel.From != machine.Agent {
					ds = append(ds, diagnostic("SDV355", diag.SeverityError, transition.Location, fmt.Sprintf("machine agent %q sends channel owned by %q", machine.Agent, channel.Channel.From), "put the send transition in the sender agent's machine", transition.ID, false))
				}
			}
			if event != "" {
				if _, ok := declaredEvents[event]; !ok {
					ds = append(ds, diagnostic("SDV330", diag.SeverityError, transition.Location, fmt.Sprintf("transition event %q is not declared by the machine", event), "add the event to the machine contract", transition.ID, false))
				}
			}
			if after := transition.Attrs["specdot_after"]; after != "" {
				if _, err := time.ParseDuration(after); err != nil {
					ds = append(ds, diagnostic("SDV331", diag.SeverityError, transition.Location, fmt.Sprintf("invalid timer %q: %v", after, err), "use a Go duration such as 500ms or 5s", transition.ID, false))
				}
			}
			if err := env.CompileBool(transition.Attrs["specdot_guard"]); err != nil {
				ds = append(ds, diagnostic("SDV332", diag.SeverityError, transition.Location, "invalid transition guard: "+err.Error(), "use restricted CEL over declared variables", transition.ID, false))
			}
			if _, err := env.CompileAction(transition.Attrs["specdot_action"]); err != nil {
				ds = append(ds, diagnostic("SDV333", diag.SeverityError, transition.Location, "invalid transition action: "+err.Error(), "use semicolon-separated declared-variable assignments", transition.ID, false))
			}
		}
		for stateID, transitions := range outgoing {
			byEvent := make(map[string][]model.Element)
			for _, transition := range transitions {
				key := transition.Attrs["specdot_event"] + "\x00" + transition.Attrs["specdot_after"] + "\x00" + transition.Attrs["specdot_receive"]
				byEvent[key] = append(byEvent[key], transition)
			}
			for _, conflicts := range byEvent {
				if len(conflicts) < 2 {
					continue
				}
				priorities := make(map[int]bool)
				allNondeterministic := true
				valid := true
				for _, transition := range conflicts {
					if !truthy(transition.Attrs["specdot_nondeterministic"]) {
						allNondeterministic = false
					}
					priority, err := strconv.Atoi(transition.Attrs["specdot_priority"])
					if err != nil || priorities[priority] {
						valid = false
					}
					priorities[priority] = true
				}
				if !allNondeterministic && !valid {
					ds = append(ds, diagnostic("SDV334", diag.SeverityError, nodes[stateID].Location, "conflicting transitions lack unique numeric priorities or explicit nondeterminism", "assign specdot_priority or mark every alternative specdot_nondeterministic=true", stateID, false))
				}
			}
		}
		for _, state := range graph.Nodes {
			switch state.Kind {
			case "fork":
				if len(outgoing[state.ID]) < 2 {
					ds = append(ds, diagnostic("SDV340", diag.SeverityError, state.Location, "fork has fewer than two outgoing branches", "add transitions into independent regions", state.ID, false))
				}
				regions := make(map[string]bool)
				for _, edge := range outgoing[state.ID] {
					target := nodes[edge.To]
					key := target.Attrs["specdot_parent"] + "\x00" + target.Attrs["specdot_region"]
					if regions[key] {
						ds = append(ds, diagnostic("SDV350", diag.SeverityError, edge.Location, "fork sends more than one branch into the same region", "fork targets must be orthogonal regions", edge.ID, false))
					}
					regions[key] = true
				}
			case "join":
				if len(incoming[state.ID]) < 2 {
					ds = append(ds, diagnostic("SDV341", diag.SeverityError, state.Location, "join has fewer than two incoming branches", "join at least two parallel regions", state.ID, false))
				}
				regions := make(map[string]bool)
				for _, edge := range incoming[state.ID] {
					source := nodes[edge.From]
					key := source.Attrs["specdot_parent"] + "\x00" + source.Attrs["specdot_region"]
					if regions[key] {
						ds = append(ds, diagnostic("SDV351", diag.SeverityError, edge.Location, "join receives more than one branch from the same region", "join sources must be orthogonal regions", edge.ID, false))
					}
					regions[key] = true
				}
			case "history":
				if state.Attrs["specdot_parent"] == "" {
					ds = append(ds, diagnostic("SDV342", diag.SeverityError, state.Location, "history pseudostate has no parent composite state", "place history inside the state whose configuration it restores", state.ID, false))
				}
			}
		}
		for _, claim := range contract.Claims {
			ds = append(ds, validateClaim(claim, contract.Location, graph.ID, nodes, env)...)
		}
		reachable := graphReachable(initials, outgoing)
		coreachable := reverseReachable(finals, incoming)
		for _, state := range graph.Nodes {
			if !reachable[state.ID] {
				ds = append(ds, diagnostic("SDV335", diag.SeverityError, state.Location, "state is unreachable", "connect it from an initial configuration or remove it", state.ID, true))
			}
			if len(finals) > 0 && !coreachable[state.ID] && !truthy(state.Attrs["specdot_nonterminating"]) {
				ds = append(ds, diagnostic("SDV336", diag.SeverityWarning, state.Location, "state cannot reach a final state", "add a terminal path or mark an intentional nonterminating service state", state.ID, true))
			}
			for name, event := range declaredEvents {
				handled := false
				for _, transition := range outgoing[state.ID] {
					if transition.Attrs["specdot_event"] == name {
						handled = true
					}
				}
				accounted := handled || hasString(split(state.Attrs["specdot_defer"]), name) || hasString(split(state.Attrs["specdot_refuse"]), name) || hasString(split(state.Attrs["specdot_ignore"]), name) || event.Policy != "handled"
				if !accounted {
					ds = append(ds, diagnostic("SDV337", diag.SeverityError, state.Location, fmt.Sprintf("accepted event %q is neither handled, deferred, refused, nor ignored in this state", name), "add a transition or explicit state policy", state.ID, false))
				}
			}
		}
		ds = append(ds, eventlessCycles(graph, outgoing)...)
	}
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		if contract.Channel == nil || contract.Channel.External {
			continue
		}
		if !sends[id] || !receives[id] {
			ds = append(ds, diagnostic("SDV356", diag.SeverityError, contract.Location, fmt.Sprintf("channel has send=%t receive=%t machine endpoints", sends[id], receives[id]), "account for both channel ends in agent machines or mark an environment channel external", id, false))
		}
	}
	return ds
}

func validateClaim(claim model.Claim, loc model.Location, graphID string, states map[string]model.Element, env *expr.Environment) []diag.Diagnostic {
	var ds []diag.Diagnostic
	valid := map[string]bool{"invariant": true, "always": true, "never": true, "forbidden": true, "reachable": true, "deadlock_free": true, "transition_coverage": true, "eventually": true, "leads_to": true}
	if !valid[claim.Kind] {
		ds = append(ds, diagnostic("SDV343", diag.SeverityError, loc, fmt.Sprintf("claim %q has unsupported kind %q", claim.ID, claim.Kind), "use an implemented finite claim kind", graphID, false))
	}
	if claim.State != "" {
		if _, ok := states[claim.State]; !ok {
			ds = append(ds, diagnostic("SDV344", diag.SeverityError, loc, fmt.Sprintf("claim %q references unknown state %q", claim.ID, claim.State), "reference a state in this machine", graphID, false))
		}
	}
	if claim.From != "" {
		if _, ok := states[claim.From]; !ok {
			ds = append(ds, diagnostic("SDV344", diag.SeverityError, loc, fmt.Sprintf("claim %q references unknown from state %q", claim.ID, claim.From), "reference a state in this machine", graphID, false))
		}
	}
	if claim.To != "" {
		if _, ok := states[claim.To]; !ok {
			ds = append(ds, diagnostic("SDV344", diag.SeverityError, loc, fmt.Sprintf("claim %q references unknown to state %q", claim.ID, claim.To), "reference a state in this machine", graphID, false))
		}
	}
	if claim.Expression != "" {
		if err := env.CompileBool(claim.Expression); err != nil {
			ds = append(ds, diagnostic("SDV345", diag.SeverityError, loc, fmt.Sprintf("claim %q has invalid CEL: %v", claim.ID, err), "use restricted CEL over machine variables", graphID, false))
		}
	}
	if claim.Kind == "leads_to" && (claim.From == "" || claim.To == "") {
		ds = append(ds, diagnostic("SDV346", diag.SeverityError, loc, fmt.Sprintf("leads_to claim %q needs from and to states", claim.ID), "declare both endpoints", graphID, false))
	}
	if (claim.Kind == "eventually" || claim.Kind == "leads_to") && len(claim.Assumptions) == 0 {
		ds = append(ds, diagnostic("SDV349", diag.SeverityError, loc, fmt.Sprintf("liveness claim %q has no scheduler/environment assumptions", claim.ID), "state the fairness or progress assumptions under which liveness is evaluated", graphID, false))
	}
	needsPredicate := claim.Kind == "invariant" || claim.Kind == "always" || claim.Kind == "never" || claim.Kind == "forbidden" || claim.Kind == "reachable" || claim.Kind == "eventually"
	if needsPredicate && claim.Expression == "" && claim.State == "" {
		ds = append(ds, diagnostic("SDV347", diag.SeverityError, loc, fmt.Sprintf("claim %q has neither state nor expression", claim.ID), "declare what the checker must evaluate", graphID, false))
	}
	return ds
}

func validateVariable(variable model.Variable) error {
	switch variable.Type {
	case "bool", "string":
		if len(variable.Values) == 0 && variable.Type == "string" {
			return fmt.Errorf("string variable %q has no finite values", variable.Name)
		}
	case "int", "int64":
		if variable.Min == nil || variable.Max == nil || *variable.Min > *variable.Max {
			return fmt.Errorf("integer variable %q needs a valid min and max", variable.Name)
		}
	default:
		if len(variable.Values) == 0 {
			return fmt.Errorf("variable %q of type %q has no finite values", variable.Name, variable.Type)
		}
	}
	return nil
}

func hierarchyCycle(id string, nodes map[string]model.Element) bool {
	seen := map[string]bool{id: true}
	for parent := nodes[id].Attrs["specdot_parent"]; parent != ""; parent = nodes[parent].Attrs["specdot_parent"] {
		if seen[parent] {
			return true
		}
		seen[parent] = true
		if _, ok := nodes[parent]; !ok {
			return false
		}
	}
	return false
}

func eventlessCycles(graph model.Graph, outgoing map[string][]model.Element) []diag.Diagnostic {
	adj := make(map[string][]model.Element)
	for from, transitions := range outgoing {
		for _, transition := range transitions {
			if transition.Attrs["specdot_event"] == "" && transition.Attrs["specdot_after"] == "" && transition.Attrs["specdot_receive"] == "" {
				adj[from] = append(adj[from], transition)
			}
		}
	}
	state := make(map[string]int)
	stack := make(map[string]model.Element)
	var ds []diag.Diagnostic
	var visit func(string)
	visit = func(id string) {
		if state[id] == 2 {
			return
		}
		state[id] = 1
		for _, edge := range adj[id] {
			if state[edge.To] == 1 {
				if !truthy(edge.Attrs["specdot_bounded"]) {
					ds = append(ds, diagnostic("SDV338", diag.SeverityError, edge.Location, "eventless transition cycle can livelock one run-to-completion step", "break the cycle or prove and mark a bounded variant", edge.ID, false))
				}
				continue
			}
			stack[id] = edge
			visit(edge.To)
		}
		state[id] = 2
	}
	ids := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		ids = append(ids, node.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if state[id] == 0 {
			visit(id)
		}
	}
	_ = stack
	return ds
}
