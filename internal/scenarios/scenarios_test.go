package scenarios

import (
	"context"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

func TestGeneratePreservesExistingFieldsAndAuditsExchange(t *testing.T) {
	state := scenarioState(t, false)
	response := `{"scenarios":[{"id":"scenario.test","scenario":{"artifact":"tool","covers":["scenario.test"],"repeat":1,"fixtures":[{"path":"input.txt","content":"hello"}],"steps":[{"id":"step.read","args":["read"],"stdin":"","timeout":"5s","expect":{"exit_code":0,"stdout":"hello\n","stderr":"","files":[{"path":"input.txt","exists":true,"text":"hello"}]}}]}}]}`
	result, err := Generate(context.Background(), provider.Static{Text: response}, state, "scenario.test")
	if err != nil {
		t.Fatal(err)
	}
	program, err := txnlang.Parse(result.Program)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := specstate.Apply(state, program)
	if err != nil {
		t.Fatal(err)
	}
	graph := updated.Entities[specstate.Key("graph", "scenario.test")]
	if graph.Fields["title"] != "Existing title" || graph.Fields["attrs"].(map[string]any)["owner"] != "team" {
		t.Fatalf("graph changed: %#v", graph.Fields)
	}
	contract := updated.Entities[specstate.Key("contract", "scenario.test")]
	if contract.Fields["summary"] != "Existing summary" || contract.Fields["metadata"].(map[string]any)["owner"] != "team" {
		t.Fatalf("contract fields changed: %#v", contract.Fields)
	}
	if _, err := Definitions(updated); err != nil {
		t.Fatal(err)
	}
	audit, err := Audit(state, result)
	if err != nil || !strings.Contains(string(audit), `"operation":"gridspec_scenarios"`) || !strings.Contains(string(audit), `step.read`) {
		t.Fatalf("audit=%s err=%v", audit, err)
	}
}

func TestGenerateRejectsIncompleteOrUnsafeResponses(t *testing.T) {
	state := scenarioState(t, false)
	validScenario := `{"artifact":"tool","covers":["scenario.test"],"repeat":1,"steps":[{"id":"step.read","expect":{"exit_code":0,"stdout":"","stderr":""}}]}`
	tests := map[string]string{
		"omitted":       `{"scenarios":[]}`,
		"wrong target":  `{"scenarios":[{"id":"scenario.other","scenario":` + validScenario + `}]}`,
		"unknown field": `{"scenarios":[{"id":"scenario.test","extra":true,"scenario":` + validScenario + `}]}`,
		"unsafe path":   `{"scenarios":[{"id":"scenario.test","scenario":{"artifact":"tool","covers":["scenario.test"],"repeat":1,"fixtures":[{"path":"../escape","content":"x"}],"steps":[{"id":"step.read","expect":{"exit_code":0,"stdout":"","stderr":""}}]}}]}`,
		"trailing":      `{"scenarios":[]} {}`,
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Generate(context.Background(), provider.Static{Text: response}, state, "scenario.test"); err == nil {
				t.Fatal("Generate succeeded")
			}
		})
	}
}

func TestGenerateCreatesOracleWhenOnlyDescriptiveScenariosExist(t *testing.T) {
	state := scenarioState(t, false)
	response := `{"scenarios":[{"id":"scenario.generated","title":"Generated smoke test","summary":"Checks the generated command artifact.","scenario":{"artifact":"tool","covers":["scenario.test"],"repeat":1,"steps":[{"id":"step.smoke","expect":{"exit_code":0,"stdout":"ok\n","stderr":""}}]}}]}`
	result, err := Generate(context.Background(), provider.Static{Text: response}, state, "")
	if err != nil {
		t.Fatal(err)
	}
	program, _ := txnlang.Parse(result.Program)
	updated, err := specstate.Apply(state, program)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := Definitions(updated)
	if err != nil || len(definitions) != 1 || definitions[0].ID != "scenario.generated" {
		t.Fatalf("definitions=%#v err=%v", definitions, err)
	}
	if updated.Entities[specstate.Key("graph", "scenario.generated")].Fields["title"] != "Generated smoke test" {
		t.Fatal("generated graph was not deterministically constructed")
	}
}

func TestGenerateForGraphRequiresCoverage(t *testing.T) {
	state := scenarioState(t, false)
	state.Entities[specstate.Key("graph", "graph.algorithm")] = specstate.Entity{
		Kind: "graph", ID: "graph.algorithm", Fields: map[string]any{
			"title": "Requested algorithm", "summary": "Needs an executable qualification scenario.",
			"kind": "algorithm", "status": "active", "directed": true, "strict": true, "attrs": map[string]any{},
		},
	}
	var err error
	state, err = specstate.Build(context.Background(), cas.NewMemory(), state)
	if err != nil {
		t.Fatal(err)
	}
	response := `{"scenarios":[{"id":"scenario.generated","title":"Generated smoke test","summary":"Checks the generated command artifact.","scenario":{"artifact":"tool","covers":["scenario.generated"],"repeat":1,"steps":[{"id":"step.smoke","expect":{"exit_code":0,"stdout":"ok\n","stderr":""}}]}}]}`
	if _, err := Generate(context.Background(), provider.Static{Text: response}, state, "graph.algorithm"); err == nil || !strings.Contains(err.Error(), "does not cover requested graph") {
		t.Fatalf("coverage error = %v", err)
	}
}

func TestValidationBoundsAndMissing(t *testing.T) {
	state := scenarioState(t, false)
	missing, err := Missing(state, "scenario.test")
	if err != nil || len(missing) != 1 || missing[0] != "scenario.test" {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	if _, err := Definitions(state); err == nil || !strings.Contains(err.Error(), "no active command scenarios") {
		t.Fatalf("Definitions error = %v", err)
	}
	state = scenarioState(t, true)
	definitions, err := Definitions(state)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("definitions=%#v err=%v", definitions, err)
	}
	definitions[0].Contract.Repeat = MaxRepeat + 1
	ir, _, _, _ := state.SemanticView()
	for i := range ir.Contracts {
		if ir.Contracts[i].For == "scenario.test" {
			ir.Contracts[i].Scenario = &definitions[0].Contract
		}
	}
	if problems := Validate(ir); len(problems) == 0 || !strings.Contains(problems[0].Message, "repeat") {
		t.Fatalf("problems=%#v", problems)
	}
}

func TestInactiveScenarioContractIsNotAnOracle(t *testing.T) {
	state := scenarioState(t, true)
	contract := state.Entities[specstate.Key("contract", "scenario.test")]
	contract.Fields["status"] = "inactive"
	state.Entities[specstate.Key("contract", "scenario.test")] = contract
	state, err := specstate.Build(context.Background(), cas.NewMemory(), state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Definitions(state); err == nil || !strings.Contains(err.Error(), "no active command scenarios") {
		t.Fatalf("Definitions error = %v", err)
	}
	missing, err := Missing(state, "scenario.test")
	if err != nil || len(missing) != 1 || missing[0] != "scenario.test" {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
}

func scenarioState(t *testing.T, complete bool) specstate.State {
	t.Helper()
	contract := map[string]any{
		"schema": "gridspec.contract/v1", "for": "scenario.test", "kind": "scenario", "status": "active",
		"summary": "Existing summary", "metadata": map[string]any{"owner": "team"},
	}
	if complete {
		contract["scenario"] = map[string]any{
			"artifact": "tool", "covers": []any{"scenario.test"}, "repeat": int64(1), "fixtures": []any{},
			"steps": []any{map[string]any{
				"id": "step.read", "args": []any{}, "stdin": "", "timeout": "5s",
				"expect": map[string]any{"exit_code": int64(0), "stdout": "", "stderr": "", "files": []any{}},
			}},
		}
	}
	state, err := specstate.Build(context.Background(), cas.NewMemory(), specstate.State{
		Module: "example.test/scenarios", Profile: "default", Entities: map[string]specstate.Entity{
			specstate.Key("graph", "scenario.test"): {
				Kind: "graph", ID: "scenario.test", Fields: map[string]any{
					"title": "Existing title", "summary": "Existing graph summary.",
					"kind": "scenario", "status": "active", "directed": true, "strict": false,
					"attrs": map[string]any{"owner": "team"},
				},
			},
			specstate.Key("contract", "scenario.test"): {Kind: "contract", ID: "scenario.test", Fields: contract},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}
