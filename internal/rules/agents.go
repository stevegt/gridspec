package rules

import (
	"fmt"

	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/expr"
)

func checkAgentsAndSignals(ctx Context, idx Index) []diag.Diagnostic {
	var ds []diag.Diagnostic
	env, _ := expr.New(nil)
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		element, exists := idx.Elements[id]
		loc := contract.Location
		if loc.File == "" && exists {
			loc = element.Location
		}
		if agent := contract.Agent; agent != nil {
			if !exists || (element.Kind != "agent" && element.Kind != "superagent") {
				ds = append(ds, diagnostic("SDV180", diag.SeverityError, loc, "agent contract does not extend an agent node", "set specdot_kind=agent or superagent", id, false))
			}
			for _, resource := range agent.Controls {
				target, ok := idx.Contracts[resource]
				if !ok || target.Data == nil {
					ds = append(ds, diagnostic("SDV181", diag.SeverityError, loc, fmt.Sprintf("controlled resource %q is not declared", resource), "reference a data resource", id, false))
				} else if target.Data.Authority != "" && target.Data.Authority != id {
					ds = append(ds, diagnostic("SDV182", diag.SeverityError, loc, fmt.Sprintf("agent claims control of %q but its authority is %q", resource, target.Data.Authority), "make local control ownership consistent", id, false))
				}
			}
			for _, capability := range agent.Capabilities {
				if _, ok := idx.Elements[capability]; !ok {
					ds = append(ds, diagnostic("SDV183", diag.SeverityError, loc, fmt.Sprintf("capability %q is not declared", capability), "reference an effect, promise, or channel", id, false))
				}
			}
			for _, machine := range agent.Machines {
				target, ok := idx.Graphs[machine]
				if !ok || target.Kind != "machine" {
					ds = append(ds, diagnostic("SDV184", diag.SeverityError, loc, fmt.Sprintf("machine %q is not declared", machine), "reference a machine graph", id, false))
				}
			}
			if element.Kind == "superagent" {
				if len(agent.Members) < 2 {
					ds = append(ds, diagnostic("SDV185", diag.SeverityError, loc, "superagent has fewer than two member agents", "declare the cooperating autonomous members", id, false))
				}
				for _, member := range agent.Members {
					target, ok := idx.Elements[member]
					if !ok || (target.Kind != "agent" && target.Kind != "superagent") || member == id {
						ds = append(ds, diagnostic("SDV186", diag.SeverityError, loc, fmt.Sprintf("invalid superagent member %q", member), "reference another declared agent", id, false))
					}
				}
			}
		}
		if signal := contract.Signal; signal != nil {
			if !exists || (element.Kind != "signal" && element.Kind != "imposition") {
				ds = append(ds, diagnostic("SDV187", diag.SeverityError, loc, "signal contract does not extend a signal/imposition edge", "use an explicitly typed communication edge", id, false))
			}
			if signal.Mode != "signal" && signal.Mode != "imposition" {
				ds = append(ds, diagnostic("SDV188", diag.SeverityError, loc, fmt.Sprintf("invalid communication mode %q", signal.Mode), "use signal or imposition", id, false))
			}
			for _, party := range []string{signal.Sender, signal.Recipient} {
				target, ok := idx.Elements[party]
				if !ok || (target.Kind != "agent" && target.Kind != "superagent") {
					ds = append(ds, diagnostic("SDV189", diag.SeverityError, loc, fmt.Sprintf("communication party %q is not an agent", party), "reference autonomous agents", id, false))
				}
			}
			if !knownType(signal.Body.Type, idx) {
				ds = append(ds, diagnostic("SDV190", diag.SeverityError, loc, fmt.Sprintf("communication body type %q is not declared", signal.Body.Type), "declare a nominal type", id, false))
			}
			if element.Attrs["specdot_polarity"] != "" {
				ds = append(ds, diagnostic("SDV191", diag.SeverityError, loc, "signal/imposition carries promise polarity", "model autonomous offer/accept promises separately", id, false))
			}
			if err := env.CompileBool(signal.Condition); err != nil {
				ds = append(ds, diagnostic("SDV192", diag.SeverityError, loc, "invalid signal condition: "+err.Error(), "use restricted CEL", id, false))
			}
		}
		if assessment := contract.Assessment; assessment != nil {
			observer, ok := idx.Elements[assessment.Observer]
			if !ok || (observer.Kind != "agent" && observer.Kind != "superagent") {
				ds = append(ds, diagnostic("SDV193", diag.SeverityError, loc, fmt.Sprintf("assessment observer %q is not an agent", assessment.Observer), "name the observer whose local state contains the judgment", id, false))
			}
			if _, ok := idx.Elements[assessment.Subject]; !ok {
				if _, ok := idx.Graphs[assessment.Subject]; !ok {
					ds = append(ds, diagnostic("SDV194", diag.SeverityError, loc, fmt.Sprintf("assessment subject %q does not exist", assessment.Subject), "reference a semantic declaration", id, false))
				}
			}
			if assessment.Evidence == "" {
				ds = append(ds, diagnostic("SDV195", diag.SeverityError, loc, "assessment has no evidence contract", "state what the observer can actually inspect", id, false))
			}
			if err := env.CompileBool(assessment.Predicate); err != nil {
				ds = append(ds, diagnostic("SDV196", diag.SeverityError, loc, "invalid assessment predicate: "+err.Error(), "use restricted CEL", id, false))
			}
		}
	}
	return ds
}
