package checker

import (
	"testing"

	"github.com/promisegrid/specdot/internal/config"
	"github.com/promisegrid/specdot/internal/model"
)

func TestShortestCounterexample(t *testing.T) {
	loc := model.Location{File: "machine.dot", Line: 1, Column: 1}
	graph := model.Graph{
		ID: "machine.test", Kind: "machine", Status: "active", Directed: true, Location: loc,
		Nodes: []model.Element{
			{ID: "state.safe", GraphID: "machine.test", Element: "node", Kind: "state", Status: "active", Attrs: map[string]string{"specdot_initial": "true"}, Location: loc},
			{ID: "state.bad", GraphID: "machine.test", Element: "node", Kind: "state", Status: "active", Attrs: map[string]string{}, Location: loc},
		},
		Edges: []model.Element{
			{ID: "transition.fail", GraphID: "machine.test", Element: "edge", Kind: "transition", Status: "active", From: "state.safe", To: "state.bad", Attrs: map[string]string{"specdot_event": "Fail"}, Location: loc},
		},
	}
	contract := model.Contract{
		Schema: "specdot.contract/v1", For: "machine.test", Kind: "machine", Location: loc,
		Machine: &model.MachineContract{Agent: "agent.test", Events: []model.EventContract{{Name: "Fail", Policy: "handled"}}},
		Claims:  []model.Claim{{ID: "claim.never_bad", Kind: "never", State: "state.bad", Required: true}},
	}
	ir := model.IR{Schema: model.IRSchema, Module: "test", Profile: "default", Graphs: []model.Graph{graph}, Contracts: []model.Contract{contract}}
	profile := config.Profile{Finite: config.FiniteProfile{Enabled: true, Required: true, MaxStates: 20, MaxDepth: 10, MaxQueue: 4, MaxTransitions: 20}}
	runs, diagnostics := RunAll(&ir, profile)
	if len(runs) != 1 || runs[0].Status != "violated" {
		t.Fatalf("runs = %#v", runs)
	}
	if len(runs[0].Trace) != 2 || runs[0].Trace[1].Transition != "transition.fail" {
		t.Fatalf("trace = %#v", runs[0].Trace)
	}
	if len(diagnostics) != 1 || diagnostics[0].Rule != "SDV700" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestExitTransitionEntryActionOrder(t *testing.T) {
	loc := model.Location{File: "actions.dot", Line: 1, Column: 1}
	min, max := int64(0), int64(10)
	graph := model.Graph{
		ID: "machine.actions", Kind: "machine", Status: "active", Directed: true, Location: loc,
		Nodes: []model.Element{
			{ID: "state.actions.start", GraphID: "machine.actions", Element: "node", Kind: "state", Status: "active", Attrs: map[string]string{"specdot_initial": "true", "specdot_exit": "x = x + 1"}, Location: loc},
			{ID: "state.actions.done", GraphID: "machine.actions", Element: "node", Kind: "final", Status: "active", Attrs: map[string]string{"specdot_final": "true", "specdot_entry": "x = x + 3"}, Location: loc},
		},
		Edges: []model.Element{
			{ID: "transition.actions.go", GraphID: "machine.actions", Element: "edge", Kind: "transition", Status: "active", From: "state.actions.start", To: "state.actions.done", Attrs: map[string]string{"specdot_event": "Go", "specdot_action": "x = x * 2"}, Location: loc},
		},
	}
	contract := model.Contract{
		Schema: "specdot.contract/v1", For: graph.ID, Kind: "machine", Location: loc,
		Machine: &model.MachineContract{
			Agent: "agent.test", Events: []model.EventContract{{Name: "Go", Policy: "handled"}},
			Variables: []model.Variable{{Name: "x", Type: "int", Initial: int64(0), Min: &min, Max: &max}},
		},
		Claims: []model.Claim{{ID: "claim.actions.order", Kind: "always", Expression: `(state == "state.actions.start" && x == 0) || (state == "state.actions.done" && x == 5)`, Required: true}},
	}
	ir := model.IR{Schema: model.IRSchema, Module: "test", Profile: "default", Graphs: []model.Graph{graph}, Contracts: []model.Contract{contract}}
	profile := config.Profile{Finite: config.FiniteProfile{Enabled: true, Required: true, MaxStates: 20, MaxDepth: 10, MaxQueue: 4, MaxTransitions: 20}}
	runs, diagnostics := RunAll(&ir, profile)
	if len(runs) != 1 || runs[0].Status != "passed" || len(diagnostics) != 0 {
		t.Fatalf("runs=%#v diagnostics=%#v", runs, diagnostics)
	}
}

func TestParallelRegionsProduceAtomicBatch(t *testing.T) {
	loc := model.Location{File: "parallel.dot", Line: 1, Column: 1}
	state := func(id, region string, initial bool) model.Element {
		attrs := map[string]string{"specdot_region": region}
		if initial {
			attrs["specdot_initial"] = "true"
		}
		return model.Element{ID: id, Element: "node", Kind: "state", Status: "active", Attrs: attrs, Location: loc}
	}
	graph := model.Graph{ID: "machine.parallel", Kind: "machine", Status: "active", Location: loc,
		Nodes: []model.Element{state("state.a1", "one", true), state("state.b1", "one", false), state("state.a2", "two", true), state("state.b2", "two", false)},
		Edges: []model.Element{
			{ID: "transition.one", From: "state.a1", To: "state.b1", Attrs: map[string]string{"specdot_event": "Go"}},
			{ID: "transition.two", From: "state.a2", To: "state.b2", Attrs: map[string]string{"specdot_event": "Go"}},
		},
	}
	contract := model.Contract{For: graph.ID, Machine: &model.MachineContract{Agent: "agent.test", Events: []model.EventContract{{Name: "Go", Policy: "handled"}}}}
	m, err := prepare(graph, contract, config.FiniteProfile{MaxQueue: 4})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := m.initialConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	batches, err := m.enabledBatches(cfg, "Go")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("batches = %#v", batches)
	}
}
