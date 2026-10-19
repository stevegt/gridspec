// Package checker implements GridSpec's native bounded EFSM verifier.
package checker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/expr"
	"github.com/promisegrid/gridspec/internal/model"
)

type machine struct {
	graph       model.Graph
	contract    model.Contract
	spec        model.MachineContract
	states      map[string]model.Element
	children    map[string]map[string][]string // parent -> region -> state ids
	transitions []model.Element
	outgoing    map[string][]model.Element
	events      []string
	env         *expr.Environment
	profile     config.FiniteProfile
}

type configuration struct {
	Active    []string       `json:"active"`
	Variables map[string]any `json:"variables,omitempty"`
	Queue     []string       `json:"queue,omitempty"`
}

type predecessor struct {
	From       string
	Event      string
	Transition string
}

type exploration struct {
	configs      map[string]configuration
	order        []string
	pred         map[string]predecessor
	adj          map[string][]string
	covered      map[string]bool
	deadlocks    []string
	steps        int
	inconclusive string
}

// RunAll verifies every active machine graph when the selected profile enables
// finite checking.
func RunAll(ir *model.IR, profile config.Profile) ([]model.CheckRun, []diag.Diagnostic) {
	if !profile.Finite.Enabled {
		var runs []model.CheckRun
		for _, graph := range ir.Graphs {
			if graph.Kind == "machine" && graph.Status == "active" {
				runs = append(runs, model.CheckRun{Machine: graph.ID, Profile: ir.Profile, Status: "unchecked", Reason: "finite checking is disabled by profile"})
			}
		}
		return runs, nil
	}
	contracts := make(map[string]model.Contract)
	for _, contract := range ir.Contracts {
		contracts[contract.For] = contract
	}
	var runs []model.CheckRun
	var ds []diag.Diagnostic
	for _, graph := range ir.Graphs {
		if graph.Kind != "machine" || graph.Status != "active" {
			continue
		}
		contract := contracts[graph.ID]
		run, err := checkMachine(graph, contract, profile.Finite, ir.Profile)
		if err != nil {
			run = model.CheckRun{Machine: graph.ID, Profile: ir.Profile, Status: "inconclusive", Reason: err.Error()}
		}
		runs = append(runs, run)
		switch run.Status {
		case "violated":
			ds = append(ds, checkDiagnostic("SDV700", diag.SeverityError, graph.Location, fmt.Sprintf("finite verification found a counterexample in %s", graph.ID), run, false))
		case "inconclusive":
			severity := diag.SeverityWarning
			if profile.Finite.Required || hasRequiredInconclusive(run, contract.Claims) {
				severity = diag.SeverityError
			}
			ds = append(ds, checkDiagnostic("SDV701", severity, graph.Location, fmt.Sprintf("finite verification is inconclusive for %s: %s", graph.ID, run.Reason), run, severity != diag.SeverityError))
		}
	}
	return runs, ds
}

func checkMachine(graph model.Graph, contract model.Contract, finite config.FiniteProfile, profileName string) (model.CheckRun, error) {
	if contract.Machine == nil {
		return model.CheckRun{}, fmt.Errorf("machine has no contract")
	}
	m, err := prepare(graph, contract, finite)
	if err != nil {
		return model.CheckRun{}, err
	}
	digest, err := machineDigest(graph, contract, finite)
	if err != nil {
		return model.CheckRun{}, err
	}
	initial, err := m.initialConfiguration()
	if err != nil {
		return model.CheckRun{}, err
	}
	settled, err := m.settle(initial, finite.MaxDepth)
	if err != nil {
		return model.CheckRun{}, err
	}
	if len(settled) == 0 {
		return model.CheckRun{}, fmt.Errorf("initial run-to-completion step has no stable configuration")
	}
	if len(settled) > finite.MaxStates {
		return model.CheckRun{Machine: graph.ID, Profile: profileName, Digest: digest, States: len(settled), Status: "inconclusive", Reason: fmt.Sprintf("initial configurations exceed state bound %d", finite.MaxStates)}, nil
	}
	ex := exploration{
		configs: make(map[string]configuration), pred: make(map[string]predecessor),
		adj: make(map[string][]string), covered: make(map[string]bool),
	}
	queue := make([]string, 0, len(settled))
	for _, cfg := range settled {
		key := configKey(cfg)
		if _, exists := ex.configs[key]; !exists {
			ex.configs[key] = cfg
			ex.order = append(ex.order, key)
			queue = append(queue, key)
		}
	}
	depth := make(map[string]int)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		cfg := ex.configs[key]
		anyEnabled := false
		for _, event := range m.events {
			batches, err := m.enabledBatches(cfg, event)
			if err != nil {
				return model.CheckRun{}, err
			}
			for _, batch := range batches {
				anyEnabled = true
				next, err := m.apply(cfg, event, batch)
				if err != nil {
					return model.CheckRun{}, err
				}
				stable, err := m.settle(next, finite.MaxDepth-depth[key])
				if err != nil {
					return model.CheckRun{}, err
				}
				for _, target := range stable {
					if ex.steps >= finite.MaxTransitions {
						ex.inconclusive = fmt.Sprintf("transition bound %d reached", finite.MaxTransitions)
						break
					}
					ex.steps++
					targetKey := configKey(target)
					ex.adj[key] = appendUnique(ex.adj[key], targetKey)
					for _, transition := range batch {
						ex.covered[transition.ID] = true
					}
					if _, exists := ex.configs[targetKey]; !exists {
						if len(ex.configs) >= finite.MaxStates {
							ex.inconclusive = fmt.Sprintf("state bound %d reached", finite.MaxStates)
							break
						}
						if depth[key]+1 > finite.MaxDepth {
							ex.inconclusive = fmt.Sprintf("depth bound %d reached", finite.MaxDepth)
							break
						}
						ex.configs[targetKey] = target
						ex.order = append(ex.order, targetKey)
						ex.pred[targetKey] = predecessor{From: key, Event: event, Transition: batchIDs(batch)}
						depth[targetKey] = depth[key] + 1
						queue = append(queue, targetKey)
					}
				}
				if ex.inconclusive != "" {
					break
				}
			}
			if ex.inconclusive != "" {
				break
			}
		}
		if !anyEnabled && !m.isFinal(cfg) {
			ex.deadlocks = append(ex.deadlocks, key)
		}
		if ex.inconclusive != "" {
			break
		}
	}
	for key := range ex.adj {
		sort.Strings(ex.adj[key])
	}
	run := model.CheckRun{Machine: graph.ID, Profile: profileName, Digest: digest, States: len(ex.configs), Steps: ex.steps, Status: "passed"}
	violatingKey := ""
	for _, claim := range contract.Claims {
		claimRun, bad, err := m.evaluateClaim(claim, ex)
		if err != nil {
			claimRun = model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: err.Error()}
		}
		run.Claims = append(run.Claims, claimRun)
		if claimRun.Status == "violated" && violatingKey == "" {
			violatingKey = bad
			run.Status = "violated"
		}
		if claimRun.Status == "inconclusive" && run.Status != "violated" {
			run.Status = "inconclusive"
			run.Reason = claimRun.Reason
		}
		if claimRun.Status == "assumed" && run.Status == "passed" {
			run.Status = "assumed"
			run.Reason = claimRun.Reason
		}
		if claimRun.Status == "assumed" {
			for _, assumption := range claim.Assumptions {
				run.Assumptions = appendUnique(run.Assumptions, assumption)
			}
		}
	}
	if ex.inconclusive != "" && run.Status != "violated" {
		run.Status = "inconclusive"
		run.Reason = ex.inconclusive
	}
	if (run.Status == "passed" || run.Status == "assumed") && hasNetworkTransitions(graph) {
		run.Status = "assumed"
		networkReason := "channel delivery is conservatively abstracted as independently schedulable receives"
		if run.Reason == "" {
			run.Reason = networkReason
		} else {
			run.Reason += "; " + networkReason
		}
		run.Assumptions = append(run.Assumptions, "send does not block local control", "receive may occur whenever the environment delivers the declared channel")
	}
	if violatingKey != "" {
		run.Trace = buildTrace(violatingKey, ex)
	}
	return run, nil
}

func prepare(graph model.Graph, contract model.Contract, profile config.FiniteProfile) (*machine, error) {
	spec := *contract.Machine
	env, err := expr.New(spec.Variables)
	if err != nil {
		return nil, err
	}
	m := &machine{
		graph: graph, contract: contract, spec: spec, states: make(map[string]model.Element),
		children: make(map[string]map[string][]string), outgoing: make(map[string][]model.Element),
		env: env, profile: profile,
	}
	for _, state := range graph.Nodes {
		if state.Kind == "history" {
			return nil, fmt.Errorf("history pseudostate %s is representable but has no finite-checker lowering in v1", state.ID)
		}
		m.states[state.ID] = state
		parent, region := state.Attrs["specdot_parent"], state.Attrs["specdot_region"]
		if m.children[parent] == nil {
			m.children[parent] = make(map[string][]string)
		}
		m.children[parent][region] = append(m.children[parent][region], state.ID)
	}
	for parent := range m.children {
		for region := range m.children[parent] {
			sort.Strings(m.children[parent][region])
		}
	}
	eventSet := make(map[string]bool)
	for _, transition := range graph.Edges {
		m.transitions = append(m.transitions, transition)
		m.outgoing[transition.From] = append(m.outgoing[transition.From], transition)
		event := transitionEvent(transition)
		if event != "" {
			eventSet[event] = true
		}
	}
	for _, event := range spec.Events {
		if event.Policy == "handled" {
			eventSet[event.Name] = true
		}
	}
	for event := range eventSet {
		m.events = append(m.events, event)
	}
	sort.Strings(m.events)
	for state := range m.outgoing {
		sort.Slice(m.outgoing[state], func(i, j int) bool { return m.outgoing[state][i].ID < m.outgoing[state][j].ID })
	}
	return m, nil
}

func (m *machine) initialConfiguration() (configuration, error) {
	active, err := m.enter("")
	if err != nil {
		return configuration{}, err
	}
	variables := make(map[string]any, len(m.spec.Variables))
	for _, variable := range m.spec.Variables {
		value, err := normalizeValue(variable.Type, variable.Initial)
		if err != nil {
			return configuration{}, fmt.Errorf("variable %s: %w", variable.Name, err)
		}
		if err := valueInDomain(variable, value); err != nil {
			return configuration{}, err
		}
		variables[variable.Name] = value
	}
	cfg := canonical(configuration{Active: active, Variables: variables})
	for _, state := range m.orderedActiveStates(cfg.Active, false) {
		if err := m.applyAction(&cfg, "", m.states[state].Attrs["specdot_entry"], "entry "+state); err != nil {
			return configuration{}, err
		}
	}
	return canonical(cfg), nil
}

// enter recursively follows each region's initial state. Parent "" enters all
// top-level regions, giving orthogonal regions an atomic shared configuration.
func (m *machine) enter(parent string) ([]string, error) {
	regions := m.children[parent]
	if len(regions) == 0 {
		if parent == "" {
			return nil, fmt.Errorf("machine has no states")
		}
		return []string{parent}, nil
	}
	regionNames := sortedKeys(regions)
	var active []string
	for _, region := range regionNames {
		initial := ""
		for _, id := range regions[region] {
			if truthy(m.states[id].Attrs["specdot_initial"]) {
				if initial != "" {
					return nil, fmt.Errorf("region parent=%q region=%q has multiple initial states", parent, region)
				}
				initial = id
			}
		}
		if initial == "" {
			return nil, fmt.Errorf("region parent=%q region=%q has no initial state", parent, region)
		}
		entered, err := m.enter(initial)
		if err != nil {
			return nil, err
		}
		active = append(active, entered...)
	}
	return active, nil
}

func (m *machine) enabledBatches(cfg configuration, event string) ([][]model.Element, error) {
	groups := make(map[string][]model.Element)
	seen := make(map[string]bool)
	for _, leaf := range cfg.Active {
		bestDepth := -1
		var selected []model.Element
		for state, depth := leaf, m.depth(leaf); state != ""; state, depth = m.states[state].Attrs["specdot_parent"], depth-1 {
			if m.states[state].Kind == "join" && !m.joinReady(cfg, state) {
				continue
			}
			var atDepth []model.Element
			for _, transition := range m.outgoing[state] {
				if transitionEvent(transition) != event {
					continue
				}
				enabled, err := m.env.EvalBool(transition.Attrs["specdot_guard"], strings.Join(cfg.Active, "|"), event, cfg.Variables)
				if err != nil {
					return nil, fmt.Errorf("guard %s: %w", transition.ID, err)
				}
				if enabled {
					atDepth = append(atDepth, transition)
				}
			}
			if len(atDepth) > 0 {
				bestDepth = depth
				selected = atDepth
				break
			}
		}
		if bestDepth < 0 {
			continue
		}
		for _, transition := range selected {
			if seen[transition.ID] {
				continue
			}
			seen[transition.ID] = true
			source := m.states[transition.From]
			group := source.Attrs["specdot_parent"] + "\x00" + source.Attrs["specdot_region"]
			groups[group] = append(groups[group], transition)
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	groupNames := sortedKeys(groups)
	choices := make([][][]model.Element, 0, len(groupNames))
	for _, group := range groupNames {
		transitions := groups[group]
		allFork := len(transitions) > 1
		forkSource := ""
		for _, transition := range transitions {
			if forkSource == "" {
				forkSource = transition.From
			}
			if transition.From != forkSource || m.states[transition.From].Kind != "fork" {
				allFork = false
			}
		}
		if allFork {
			sort.Slice(transitions, func(i, j int) bool { return transitions[i].ID < transitions[j].ID })
			choices = append(choices, [][]model.Element{transitions})
			continue
		}
		maxPriority := -int(^uint(0)>>1) - 1
		hasPriority := false
		for _, transition := range transitions {
			if p, err := strconv.Atoi(transition.Attrs["specdot_priority"]); err == nil {
				hasPriority = true
				if p > maxPriority {
					maxPriority = p
				}
			}
		}
		var alternatives []model.Element
		for _, transition := range transitions {
			if hasPriority {
				p, err := strconv.Atoi(transition.Attrs["specdot_priority"])
				if err != nil || p != maxPriority {
					continue
				}
			}
			alternatives = append(alternatives, transition)
		}
		sort.Slice(alternatives, func(i, j int) bool { return alternatives[i].ID < alternatives[j].ID })
		options := make([][]model.Element, len(alternatives))
		for i := range alternatives {
			options[i] = []model.Element{alternatives[i]}
		}
		choices = append(choices, options)
	}
	return cartesianBatchOptions(choices), nil
}

func (m *machine) joinReady(cfg configuration, join string) bool {
	parent := m.states[join].Attrs["specdot_parent"]
	for _, leaf := range cfg.Active {
		if parent == "" || m.descendantOrSelf(leaf, parent) {
			if !m.descendantOrSelf(leaf, join) {
				return false
			}
		}
	}
	return true
}

func cartesianBatchOptions(groups [][][]model.Element) [][]model.Element {
	result := [][]model.Element{{}}
	for _, options := range groups {
		var next [][]model.Element
		for _, prefix := range result {
			for _, option := range options {
				batch := append(append([]model.Element(nil), prefix...), option...)
				next = append(next, batch)
			}
		}
		result = next
	}
	return result
}

func (m *machine) apply(cfg configuration, event string, batch []model.Element) (configuration, error) {
	next := cloneConfiguration(cfg)
	targetActive := append([]string(nil), cfg.Active...)
	sort.Slice(batch, func(i, j int) bool { return batch[i].ID < batch[j].ID })
	exitSet := make(map[string]bool)
	entrySet := make(map[string]bool)
	for _, transition := range batch {
		if truthy(transition.Attrs["specdot_internal"]) {
			continue
		}
		for _, leaf := range cfg.Active {
			if m.descendantOrSelf(leaf, transition.From) {
				for state := leaf; state != ""; state = m.states[state].Attrs["specdot_parent"] {
					exitSet[state] = true
					if state == transition.From {
						break
					}
				}
			}
		}
		kept := targetActive[:0]
		for _, leaf := range targetActive {
			if !m.descendantOrSelf(leaf, transition.From) {
				kept = append(kept, leaf)
			}
		}
		targetActive = kept
		entered, err := m.enter(transition.To)
		if err != nil {
			return configuration{}, fmt.Errorf("enter %s: %w", transition.To, err)
		}
		targetActive = append(targetActive, entered...)
		for _, leaf := range entered {
			for state := leaf; state != ""; state = m.states[state].Attrs["specdot_parent"] {
				entrySet[state] = true
				if state == transition.To {
					break
				}
			}
		}
	}
	exits := sortedStateSet(exitSet, m, true)
	for _, state := range exits {
		if err := m.applyAction(&next, event, m.states[state].Attrs["specdot_exit"], "exit "+state); err != nil {
			return configuration{}, err
		}
	}
	for _, transition := range batch {
		if err := m.applyAction(&next, event, transition.Attrs["specdot_action"], "transition "+transition.ID); err != nil {
			return configuration{}, err
		}
	}
	next.Active = canonical(configuration{Active: targetActive}).Active
	entries := sortedStateSet(entrySet, m, false)
	for _, state := range entries {
		if err := m.applyAction(&next, event, m.states[state].Attrs["specdot_entry"], "entry "+state); err != nil {
			return configuration{}, err
		}
	}
	for _, transition := range batch {
		for _, raised := range split(transition.Attrs["specdot_raise"]) {
			next.Queue = append(next.Queue, raised)
			if len(next.Queue) > m.profile.MaxQueue {
				return configuration{}, fmt.Errorf("internal event queue exceeded bound %d", m.profile.MaxQueue)
			}
		}
	}
	return canonical(next), nil
}

func (m *machine) applyAction(cfg *configuration, event, source, label string) error {
	actions, err := m.env.CompileAction(source)
	if err != nil {
		return fmt.Errorf("%s action: %w", label, err)
	}
	for _, assignment := range actions {
		value, err := assignment.Eval(strings.Join(cfg.Active, "|"), event, cfg.Variables)
		if err != nil {
			return fmt.Errorf("%s action: %w", label, err)
		}
		variable := m.variable(assignment.Name)
		value, err = normalizeValue(variable.Type, value)
		if err != nil {
			return fmt.Errorf("%s action: %w", label, err)
		}
		if err := valueInDomain(variable, value); err != nil {
			return fmt.Errorf("%s action: %w", label, err)
		}
		cfg.Variables[assignment.Name] = value
	}
	return nil
}

func (m *machine) orderedActiveStates(active []string, innerFirst bool) []string {
	set := make(map[string]bool)
	for _, leaf := range active {
		for state := leaf; state != ""; state = m.states[state].Attrs["specdot_parent"] {
			set[state] = true
		}
	}
	return sortedStateSet(set, m, innerFirst)
}

func sortedStateSet(set map[string]bool, m *machine, innerFirst bool) []string {
	states := sortedKeys(set)
	sort.SliceStable(states, func(i, j int) bool {
		di, dj := m.depth(states[i]), m.depth(states[j])
		if di == dj {
			return states[i] < states[j]
		}
		if innerFirst {
			return di > dj
		}
		return di < dj
	})
	return states
}

// settle drains raised internal events, then eventless transitions, before the
// next external event is admitted.
func (m *machine) settle(start configuration, budget int) ([]configuration, error) {
	type item struct {
		cfg   configuration
		steps int
	}
	queue := []item{{cfg: canonical(start)}}
	seen := make(map[string]bool)
	var stable []configuration
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		key := configKey(current.cfg)
		if seen[key] {
			continue
		}
		seen[key] = true
		if current.steps > budget {
			return nil, fmt.Errorf("run-to-completion exceeded depth budget")
		}
		event := ""
		base := current.cfg
		if len(base.Queue) > 0 {
			event = base.Queue[0]
			base.Queue = append([]string(nil), base.Queue[1:]...)
		}
		batches, err := m.enabledBatches(base, event)
		if err != nil {
			return nil, err
		}
		if len(batches) == 0 {
			if event != "" {
				queue = append(queue, item{cfg: base, steps: current.steps + 1})
			} else {
				stable = append(stable, canonical(base))
			}
			continue
		}
		for _, batch := range batches {
			next, err := m.apply(base, event, batch)
			if err != nil {
				return nil, err
			}
			queue = append(queue, item{cfg: next, steps: current.steps + 1})
		}
	}
	if len(stable) == 0 {
		return nil, fmt.Errorf("run-to-completion microstep cycle has no stable configuration")
	}
	sort.Slice(stable, func(i, j int) bool { return configKey(stable[i]) < configKey(stable[j]) })
	return stable, nil
}

func (m *machine) evaluateClaim(claim model.Claim, ex exploration) (model.ClaimRun, string, error) {
	result := model.ClaimRun{ID: claim.ID, Status: "passed"}
	predicate := func(cfg configuration) (bool, error) {
		if claim.State != "" {
			for _, state := range cfg.Active {
				if m.descendantOrSelf(state, claim.State) {
					return true, nil
				}
			}
			return false, nil
		}
		return m.env.EvalBool(claim.Expression, strings.Join(cfg.Active, "|"), "", cfg.Variables)
	}
	switch claim.Kind {
	case "invariant", "always":
		for _, key := range ex.order {
			ok, err := predicate(ex.configs[key])
			if err != nil {
				return result, "", err
			}
			if !ok {
				result.Status, result.Reason = "violated", "invariant is false"
				return result, key, nil
			}
		}
	case "never", "forbidden":
		for _, key := range ex.order {
			ok, err := predicate(ex.configs[key])
			if err != nil {
				return result, "", err
			}
			if ok {
				result.Status, result.Reason = "violated", "forbidden predicate/state is reachable"
				return result, key, nil
			}
		}
	case "reachable":
		for _, key := range ex.order {
			ok, err := predicate(ex.configs[key])
			if err != nil {
				return result, "", err
			}
			if ok {
				return result, "", nil
			}
		}
		if ex.inconclusive != "" {
			return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: ex.inconclusive}, "", nil
		}
		result.Status, result.Reason = "violated", "target is unreachable"
		if len(ex.order) > 0 {
			return result, ex.order[len(ex.order)-1], nil
		}
	case "deadlock_free":
		if len(ex.deadlocks) > 0 {
			result.Status, result.Reason = "violated", "non-final deadlock is reachable"
			return result, ex.deadlocks[0], nil
		}
		if ex.inconclusive != "" {
			return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: ex.inconclusive}, "", nil
		}
	case "transition_coverage":
		for _, transition := range m.transitions {
			if !ex.covered[transition.ID] {
				if ex.inconclusive != "" {
					return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: ex.inconclusive}, "", nil
				}
				result.Status, result.Reason = "violated", "transition "+transition.ID+" is never taken"
				return result, "", nil
			}
		}
	case "eventually":
		if len(claim.Assumptions) == 0 {
			return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: "liveness has no fairness/progress assumptions"}, "", nil
		}
		if ex.inconclusive != "" {
			return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: ex.inconclusive}, "", nil
		}
		bad, err := livenessCounterexample(ex, func(cfg configuration) (bool, error) { return predicate(cfg) }, nil)
		if err != nil {
			return result, "", err
		}
		if bad != "" {
			result.Status, result.Reason = "violated", "an execution can avoid the eventual target forever"
			return result, bad, nil
		}
		result.Status, result.Reason = "assumed", "passed under assumptions: "+strings.Join(claim.Assumptions, ", ")
	case "leads_to":
		if len(claim.Assumptions) == 0 {
			return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: "liveness has no fairness/progress assumptions"}, "", nil
		}
		if ex.inconclusive != "" {
			return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: ex.inconclusive}, "", nil
		}
		antecedent := func(cfg configuration) (bool, error) {
			if claim.From != "" {
				return activeContains(m, cfg, claim.From), nil
			}
			return false, fmt.Errorf("leads_to requires from")
		}
		consequent := func(cfg configuration) (bool, error) {
			if claim.To != "" {
				return activeContains(m, cfg, claim.To), nil
			}
			return false, fmt.Errorf("leads_to requires to")
		}
		bad, err := livenessCounterexample(ex, consequent, antecedent)
		if err != nil {
			return result, "", err
		}
		if bad != "" {
			result.Status, result.Reason = "violated", "an antecedent execution can avoid its consequent forever"
			return result, bad, nil
		}
		result.Status, result.Reason = "assumed", "passed under assumptions: "+strings.Join(claim.Assumptions, ", ")
	default:
		return model.ClaimRun{ID: claim.ID, Status: "inconclusive", Reason: "unsupported claim kind " + claim.Kind}, "", nil
	}
	return result, "", nil
}

type predicate func(configuration) (bool, error)

// livenessCounterexample finds a reachable cycle or terminal path in the
// subgraph that avoids the target. With a start predicate it only considers
// paths at or after an antecedent state.
func livenessCounterexample(ex exploration, target predicate, start predicate) (string, error) {
	allowed := make(map[string]bool)
	seeds := make(map[string]bool)
	for _, key := range ex.order {
		ok, err := target(ex.configs[key])
		if err != nil {
			return "", err
		}
		if !ok {
			allowed[key] = true
		}
		if start == nil {
			seeds[key] = true
		} else {
			match, err := start(ex.configs[key])
			if err != nil {
				return "", err
			}
			if match {
				seeds[key] = true
			}
		}
	}
	reachable := make(map[string]bool)
	queue := sortedKeys(seeds)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if reachable[key] || !allowed[key] {
			continue
		}
		reachable[key] = true
		for _, next := range ex.adj[key] {
			queue = append(queue, next)
		}
	}
	components := tarjan(ex.adj, reachable)
	for _, component := range components {
		if len(component) > 1 {
			return component[0], nil
		}
		key := component[0]
		for _, next := range ex.adj[key] {
			if next == key {
				return key, nil
			}
		}
		hasAllowedSuccessor := false
		for _, next := range ex.adj[key] {
			if reachable[next] {
				hasAllowedSuccessor = true
			}
		}
		if !hasAllowedSuccessor && len(ex.adj[key]) == 0 {
			return key, nil
		}
	}
	return "", nil
}

func tarjan(adj map[string][]string, included map[string]bool) [][]string {
	index := 0
	indices, low := make(map[string]int), make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var result [][]string
	var strong func(string)
	strong = func(v string) {
		index++
		indices[v], low[v] = index, index
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if !included[w] {
				continue
			}
			if indices[w] == 0 {
				strong(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && indices[w] < low[v] {
				low[v] = indices[w]
			}
		}
		if low[v] == indices[v] {
			var component []string
			for {
				n := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[n] = false
				component = append(component, n)
				if n == v {
					break
				}
			}
			sort.Strings(component)
			result = append(result, component)
		}
	}
	for _, v := range sortedKeys(included) {
		if included[v] && indices[v] == 0 {
			strong(v)
		}
	}
	return result
}

func buildTrace(key string, ex exploration) []model.TraceStep {
	var reverse []model.TraceStep
	for {
		cfg := ex.configs[key]
		pred, hasPred := ex.pred[key]
		step := model.TraceStep{State: strings.Join(cfg.Active, "|"), Variables: cfg.Variables}
		if hasPred {
			step.Event, step.Transition = pred.Event, pred.Transition
		}
		reverse = append(reverse, step)
		if !hasPred {
			break
		}
		key = pred.From
	}
	trace := make([]model.TraceStep, len(reverse))
	for i := range reverse {
		trace[len(reverse)-1-i] = reverse[i]
		trace[len(reverse)-1-i].Depth = len(reverse) - 1 - i
	}
	return trace
}

func (m *machine) depth(id string) int {
	depth := 0
	for id != "" {
		depth++
		id = m.states[id].Attrs["specdot_parent"]
	}
	return depth
}
func (m *machine) descendantOrSelf(id, ancestor string) bool {
	for id != "" {
		if id == ancestor {
			return true
		}
		id = m.states[id].Attrs["specdot_parent"]
	}
	return false
}
func (m *machine) variable(name string) model.Variable {
	for _, v := range m.spec.Variables {
		if v.Name == name {
			return v
		}
	}
	return model.Variable{Name: name}
}
func (m *machine) isFinal(cfg configuration) bool {
	if len(cfg.Active) == 0 {
		return false
	}
	for _, id := range cfg.Active {
		state := m.states[id]
		if state.Kind != "final" && !truthy(state.Attrs["specdot_final"]) {
			return false
		}
	}
	return true
}
func activeContains(m *machine, cfg configuration, target string) bool {
	for _, state := range cfg.Active {
		if m.descendantOrSelf(state, target) {
			return true
		}
	}
	return false
}

func transitionEvent(t model.Element) string {
	if receive := t.Attrs["specdot_receive"]; receive != "" {
		return "@receive:" + receive
	}
	if after := t.Attrs["specdot_after"]; after != "" {
		return "@after:" + after
	}
	return t.Attrs["specdot_event"]
}

func hasNetworkTransitions(graph model.Graph) bool {
	for _, transition := range graph.Edges {
		if transition.Attrs["specdot_send"] != "" || transition.Attrs["specdot_receive"] != "" {
			return true
		}
	}
	return false
}
func batchIDs(batch []model.Element) string {
	ids := make([]string, len(batch))
	for i := range batch {
		ids[i] = batch[i].ID
	}
	sort.Strings(ids)
	return strings.Join(ids, "+")
}
func appendUnique(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

func cloneConfiguration(cfg configuration) configuration {
	result := configuration{Active: append([]string(nil), cfg.Active...), Queue: append([]string(nil), cfg.Queue...), Variables: make(map[string]any, len(cfg.Variables))}
	for name, value := range cfg.Variables {
		result.Variables[name] = value
	}
	return result
}

func canonical(cfg configuration) configuration {
	sort.Strings(cfg.Active)
	active := cfg.Active[:0]
	for _, state := range cfg.Active {
		if len(active) == 0 || active[len(active)-1] != state {
			active = append(active, state)
		}
	}
	cfg.Active = active
	return cfg
}

func configKey(cfg configuration) string {
	b, _ := json.Marshal(canonical(cloneConfiguration(cfg)))
	return string(b)
}

func normalizeValue(typ string, value any) (any, error) {
	switch typ {
	case "int", "int64":
		switch v := value.(type) {
		case int:
			return int64(v), nil
		case int64:
			return v, nil
		case uint64:
			return int64(v), nil
		case float64:
			return int64(v), nil
		default:
			return nil, fmt.Errorf("%v is not an integer", value)
		}
	case "bool":
		if v, ok := value.(bool); ok {
			return v, nil
		}
		return nil, fmt.Errorf("%v is not bool", value)
	case "string":
		if v, ok := value.(string); ok {
			return v, nil
		}
		return nil, fmt.Errorf("%v is not string", value)
	default:
		return value, nil
	}
}

func valueInDomain(variable model.Variable, value any) error {
	if variable.Type == "int" || variable.Type == "int64" {
		v, ok := value.(int64)
		if !ok {
			return fmt.Errorf("variable %s is not int64", variable.Name)
		}
		if variable.Min == nil || variable.Max == nil || v < *variable.Min || v > *variable.Max {
			return fmt.Errorf("variable %s value %d is outside its finite domain", variable.Name, v)
		}
		return nil
	}
	if variable.Type == "bool" {
		return nil
	}
	if len(variable.Values) > 0 {
		for _, candidate := range variable.Values {
			normalized, _ := normalizeValue(variable.Type, candidate)
			if fmt.Sprint(normalized) == fmt.Sprint(value) {
				return nil
			}
		}
		return fmt.Errorf("variable %s value %v is outside its finite domain", variable.Name, value)
	}
	return nil
}

func machineDigest(graph model.Graph, contract model.Contract, profile config.FiniteProfile) (string, error) {
	payload := struct {
		Graph    model.Graph          `json:"graph"`
		Contract model.Contract       `json:"contract"`
		Profile  config.FiniteProfile `json:"profile"`
	}{graph, contract, profile}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func checkDiagnostic(rule string, severity diag.Severity, loc model.Location, message string, run model.CheckRun, waivable bool) diag.Diagnostic {
	help := "inspect the emitted shortest trace"
	if run.Reason != "" {
		help = run.Reason
	}
	return diag.Diagnostic{Rule: rule, Severity: severity, Message: message, Help: help, ElementID: run.Machine, Waivable: waivable, Location: diag.Location{File: loc.File, Line: loc.Line, Column: loc.Column}}
}

func hasRequiredInconclusive(run model.CheckRun, claims []model.Claim) bool {
	required := make(map[string]bool)
	for _, claim := range claims {
		required[claim.ID] = claim.Required
	}
	for _, result := range run.Claims {
		if result.Status == "inconclusive" && required[result.ID] {
			return true
		}
	}
	return false
}

func split(value string) []string {
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
func truthy(value string) bool { return value == "true" || value == "yes" || value == "1" }
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
