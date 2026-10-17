package rules

import (
	"fmt"
	"strings"

	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/expr"
	"github.com/promisegrid/specdot/internal/model"
)

type namedPromise struct {
	id       string
	contract model.PromiseContract
	loc      model.Location
	status   string
}

func checkPromises(ctx Context, idx Index) ([]diag.Diagnostic, []model.Binding) {
	var ds []diag.Diagnostic
	var offers, accepts []namedPromise
	env, envErr := expr.New(nil)
	if envErr != nil {
		panic(envErr)
	}
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		if contract.Promise == nil {
			continue
		}
		p := *contract.Promise
		element, exists := idx.Elements[id]
		status := contract.Status
		if status == "" && exists {
			status = element.Status
		}
		if status == "" {
			status = "active"
		}
		loc := contract.Location
		if loc.File == "" && exists {
			loc = element.Location
		}
		if !exists || element.Kind != "promise" {
			ds = append(ds, diagnostic("SDV200", diag.SeverityError, loc, "promise contract must extend a promise edge", "set specdot_kind=promise on the owning edge", id, false))
		}
		if p.Promiser == "" || p.Promisee == "" || p.Promiser == p.Promisee {
			ds = append(ds, diagnostic("SDV201", diag.SeverityError, loc, "promise needs distinct promiser and promisee agents", "declare who autonomously offers or accepts the body", id, false))
		}
		for _, agent := range []string{p.Promiser, p.Promisee} {
			if target, ok := idx.Elements[agent]; !ok || (target.Kind != "agent" && target.Kind != "superagent") {
				ds = append(ds, diagnostic("SDV202", diag.SeverityError, loc, fmt.Sprintf("promise party %q is not a declared agent", agent), "declare an agent node and reference its semantic id", id, false))
			}
		}
		polarity := normalizePolarity(p.Polarity)
		if polarity == "" {
			ds = append(ds, diagnostic("SDV203", diag.SeverityError, loc, fmt.Sprintf("invalid promise polarity %q", p.Polarity), "use offer/+ or accept/-", id, false))
		}
		if p.Body.Type == "" {
			ds = append(ds, diagnostic("SDV204", diag.SeverityError, loc, "promise body has no nominal type", "set body.type or specdot_type", id, false))
		} else if !knownType(p.Body.Type, idx) {
			ds = append(ds, diagnostic("SDV205", diag.SeverityError, loc, fmt.Sprintf("promise body type %q is not declared", p.Body.Type), "declare the type in a data graph", id, false))
		}
		for label, source := range map[string]string{"condition": p.Condition, "constraint": p.Body.Constraints} {
			if err := env.CompileBool(source); err != nil {
				ds = append(ds, diagnostic("SDV206", diag.SeverityError, loc, fmt.Sprintf("invalid CEL %s: %v", label, err), "use the restricted CEL environment: state, event, data, subject, and context", id, false))
			}
		}
		if p.Prerequisite != "" {
			if target, ok := idx.Contracts[p.Prerequisite]; !ok || target.Promise == nil {
				ds = append(ds, diagnostic("SDV207", diag.SeverityError, loc, fmt.Sprintf("promise prerequisite %q is not a promise", p.Prerequisite), "reference another promise id", id, false))
			}
		}
		if strings.Contains(strings.ToLower(p.Evidence), "authority") && (strings.Contains(strings.ToLower(p.Evidence), "observ") || strings.Contains(strings.ToLower(p.Evidence), "copy") || strings.Contains(strings.ToLower(p.Evidence), "signature")) {
			ds = append(ds, diagnostic("SDV208", diag.SeverityError, loc, "observation, a copy, or a signature is described as authority", "model evidence as observer-local assessment and authority as an explicit promise", id, false))
		}
		if status != "active" || polarity == "" {
			continue
		}
		named := namedPromise{id: id, contract: p, loc: loc, status: status}
		if polarity == "offer" {
			offers = append(offers, named)
		} else {
			accepts = append(accepts, named)
		}
	}

	var bindings []model.Binding
	matchedAccept := make(map[string]bool)
	for _, offer := range offers {
		matched := false
		for _, acceptance := range accepts {
			if promiseCompatible(offer.contract, acceptance.contract) {
				matched = true
				matchedAccept[acceptance.id] = true
				bindings = append(bindings, model.Binding{Offer: offer.id, Acceptance: acceptance.id, BodyType: offer.contract.Body.Type})
			}
		}
		if !matched && !offer.contract.External {
			ds = append(ds, diagnostic("SDV209", diag.SeverityError, offer.loc, "offered promise has no compatible autonomous acceptance", "add a - promise by the promisee, or mark a truly external boundary", offer.id, true))
		}
	}
	for _, acceptance := range accepts {
		if !matchedAccept[acceptance.id] && !acceptance.contract.External {
			ds = append(ds, diagnostic("SDV210", diag.SeverityError, acceptance.loc, "acceptance promise has no compatible offer", "add a + promise by the counterparty, or mark a truly external boundary", acceptance.id, true))
		}
	}
	ds = append(ds, promiseCycles(idx)...)
	return ds, bindings
}

func normalizePolarity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "+", "offer", "offers", "provide":
		return "offer"
	case "-", "accept", "accepts", "consume":
		return "accept"
	default:
		return ""
	}
}

func promiseCompatible(offer, acceptance model.PromiseContract) bool {
	if offer.Promiser != acceptance.Promisee || offer.Promisee != acceptance.Promiser {
		return false
	}
	if offer.Body.Type != acceptance.Body.Type || offer.Body.Resource != acceptance.Body.Resource {
		return false
	}
	return offer.Body.Constraints == "" || acceptance.Body.Constraints == "" || offer.Body.Constraints == acceptance.Body.Constraints
}

func promiseCycles(idx Index) []diag.Diagnostic {
	state := make(map[string]int)
	var stack []string
	var ds []diag.Diagnostic
	var visit func(string)
	visit = func(id string) {
		if state[id] == 2 {
			return
		}
		if state[id] == 1 {
			start := 0
			for i := range stack {
				if stack[i] == id {
					start = i
					break
				}
			}
			cycle := append(append([]string(nil), stack[start:]...), id)
			contract := idx.Contracts[id]
			ds = append(ds, diagnostic("SDV211", diag.SeverityError, contract.Location, "cyclic promise prerequisites: "+strings.Join(cycle, " -> "), "break the dependency cycle or model a stateful bootstrap protocol", id, false))
			return
		}
		contract, ok := idx.Contracts[id]
		if !ok || contract.Promise == nil {
			return
		}
		state[id] = 1
		stack = append(stack, id)
		if next := contract.Promise.Prerequisite; next != "" {
			visit(next)
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	for _, id := range sortedKeys(idx.Contracts) {
		contract := idx.Contracts[id]
		if contract.Promise != nil {
			visit(id)
		}
	}
	return ds
}

func knownType(name string, idx Index) bool {
	switch name {
	case "bool", "int", "int64", "uint", "uint64", "double", "float", "string", "bytes", "duration", "timestamp", "unit":
		return true
	}
	if element, ok := idx.Elements[name]; ok {
		return element.Kind == "type" || element.Kind == "resource" || element.Kind == "data"
	}
	contract, ok := idx.Contracts[name]
	return ok && contract.Data != nil
}
