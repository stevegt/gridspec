package goworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/specdiff"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

func TestPersistentCounterClosedLoopQualificationAndUpdate(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	initial, err := specstate.Build(ctx, store, counterState(9))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "counter")
	built, err := Build(ctx, provider.Static{Text: counterGeneratedJSON(t, 9, true)}, store, testSource(initial), output)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Qualification.Scenarios) != 4 {
		t.Fatalf("qualified scenarios = %#v", built.Qualification.Scenarios)
	}

	// Code-to-spec remains a proposal: disassemble, apply to a clone, and
	// inspect a deterministic semantic diff without changing accepted state.
	graph := initial.Entities[specstate.Key("graph", "graph.counter")]
	observedFields := cloneTestFields(graph.Fields)
	observedFields["summary"] = "Observed persistent counter implementation."
	proposalProgram, err := txnlang.Format(txnlang.Program{Ops: []txnlang.Operation{
		{Action: "assert_exists", Kind: "graph", ID: "graph.counter"},
		{Action: "put", Kind: "graph", ID: "graph.counter", Fields: observedFields},
	}})
	if err != nil {
		t.Fatal(err)
	}
	disassembled, _, err := Disassemble(ctx, provider.Static{Text: string(proposalProgram)}, store, testSource(initial), output)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := txnlang.Parse(disassembled)
	proposed, err := specstate.Apply(initial, parsed)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := specdiff.Compare(initial, proposed)
	if err != nil || len(diff.Changes) != 1 || !strings.HasSuffix(diff.Changes[0].Path, "/summary") {
		t.Fatalf("diff=%#v err=%v", diff, err)
	}

	// The reviewed follow-up transaction raises the bound and changes the
	// corresponding upper-bound scenario from rejection to success.
	followup := txnlang.Program{Ops: []txnlang.Operation{
		{Action: "assert_equals", Kind: "contract", ID: "graph.counter", Field: "metadata", Value: map[string]any{"upper_bound": "9"}},
		{Action: "put", Kind: "contract", ID: "graph.counter", Fields: counterAlgorithmContract(12)},
		{Action: "assert_exists", Kind: "contract", ID: "scenario.counter.upper"},
		{Action: "put", Kind: "contract", ID: "scenario.counter.upper", Fields: counterScenarioContract("scenario.counter.upper", 12)},
	}}
	next, err := specstate.Apply(initial, followup)
	if err != nil {
		t.Fatal(err)
	}
	next, err = specstate.Build(ctx, store, next)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(output, "main.go"))
	dry, err := Update(ctx, provider.Static{Text: counterGeneratedJSON(t, 12, false)}, store, testSource(next), output, false)
	if err != nil {
		t.Fatal(err)
	}
	afterDry, _ := os.ReadFile(filepath.Join(output, "main.go"))
	if len(dry.Changes) == 0 || string(before) != string(afterDry) {
		t.Fatalf("dry-run changes=%v mutated=%t", dry.Changes, string(before) != string(afterDry))
	}
	written, err := Update(ctx, provider.Static{Text: counterGeneratedJSON(t, 12, false)}, store, testSource(next), output, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(written.Generated.Qualification.Scenarios) != 4 {
		t.Fatalf("update evidence = %#v", written.Generated.Qualification)
	}
	afterWrite, _ := os.ReadFile(filepath.Join(output, "main.go"))
	if !strings.Contains(string(afterWrite), "const limit = 12") {
		t.Fatalf("updated code does not contain new bound:\n%s", afterWrite)
	}
}

func counterState(limit int) specstate.State {
	entities := map[string]specstate.Entity{
		specstate.Key("graph", "graph.counter"): {
			Kind: "graph", ID: "graph.counter", Fields: map[string]any{
				"title": "Persistent counter", "summary": "Stores one bounded non-negative integer.",
				"kind": "data", "status": "active", "directed": true, "strict": true, "attrs": map[string]any{},
			},
		},
		specstate.Key("contract", "graph.counter"): {Kind: "contract", ID: "graph.counter", Fields: counterAlgorithmContract(limit)},
	}
	for _, id := range []string{"scenario.counter.missing", "scenario.counter.increment", "scenario.counter.negative", "scenario.counter.upper"} {
		entities[specstate.Key("graph", id)] = specstate.Entity{Kind: "graph", ID: id, Fields: map[string]any{
			"title": id, "summary": "Compiler-owned counter command scenario.",
			"kind": "scenario", "status": "active", "directed": true, "strict": true, "attrs": map[string]any{},
		}}
		entities[specstate.Key("contract", id)] = specstate.Entity{Kind: "contract", ID: id, Fields: counterScenarioContract(id, limit)}
	}
	return specstate.State{Module: "example.test/counter", Profile: "default", Entities: entities}
}

func counterAlgorithmContract(limit int) map[string]any {
	return map[string]any{
		"schema": "gridspec.contract/v1", "for": "graph.counter", "kind": "data", "status": "active",
		"metadata": map[string]any{"upper_bound": fmt.Sprint(limit)},
	}
}

func counterScenarioContract(id string, limit int) map[string]any {
	scenario := map[string]any{
		"artifact": "counter", "covers": []any{id, "graph.counter"}, "repeat": int64(1), "fixtures": []any{},
	}
	step := func(id string, args []any, code int64, stdout, stderr string, files []any) any {
		return map[string]any{
			"id": id, "args": args, "stdin": "", "timeout": "5s",
			"expect": map[string]any{"exit_code": code, "stdout": stdout, "stderr": stderr, "files": files},
		}
	}
	state3 := []any{map[string]any{"path": "counter.json", "exists": true, "json": "{\"value\":3}"}}
	switch id {
	case "scenario.counter.missing":
		scenario["repeat"] = int64(2)
		scenario["steps"] = []any{step("counter.get.missing", []any{"get"}, 0, "0\n", "", []any{})}
	case "scenario.counter.increment":
		scenario["steps"] = []any{
			step("counter.add.three", []any{"add", "3"}, 0, "3\n", "", state3),
			step("counter.get.three", []any{"get"}, 0, "3\n", "", state3),
		}
	case "scenario.counter.negative":
		scenario["steps"] = []any{
			step("counter.seed.three", []any{"add", "3"}, 0, "3\n", "", state3),
			step("counter.reject.negative", []any{"add", "-1"}, 1, "", "increment must be non-negative\n", state3),
			step("counter.remains.three", []any{"get"}, 0, "3\n", "", state3),
		}
	case "scenario.counter.upper":
		if limit == 9 {
			scenario["steps"] = []any{step("counter.reject.ten", []any{"add", "10"}, 1, "", "counter exceeds 9\n", []any{map[string]any{"path": "counter.json", "exists": false}})}
		} else {
			scenario["steps"] = []any{step("counter.accept.ten", []any{"add", "10"}, 0, "10\n", "", []any{map[string]any{"path": "counter.json", "exists": true, "json": "{\"value\":10}"}})}
		}
	}
	return map[string]any{
		"schema": "gridspec.contract/v1", "for": id, "kind": "scenario", "status": "active", "scenario": scenario,
	}
}

func counterGeneratedJSON(t *testing.T, limit int, includeModule bool) string {
	t.Helper()
	mainSource := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

const limit = %d
const statePath = "counter.json"
type state struct { Value int `+"`json:\"value\"`"+` }

func load() (state, error) {
	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) { return state{}, nil }
	if err != nil { return state{}, err }
	var value state
	err = json.Unmarshal(data, &value)
	return value, err
}

func save(value state) error {
	data, err := json.Marshal(value)
	if err != nil { return err }
	data = append(data, '\n')
	return os.WriteFile(statePath, data, 0600)
}

func main() {
	value, err := load()
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
	if len(os.Args) == 2 && os.Args[1] == "get" { fmt.Println(value.Value); return }
	if len(os.Args) == 3 && os.Args[1] == "add" {
		increment, err := strconv.Atoi(os.Args[2])
		if err != nil { fmt.Fprintln(os.Stderr, "invalid increment"); os.Exit(2) }
		if increment < 0 { fmt.Fprintln(os.Stderr, "increment must be non-negative"); os.Exit(1) }
		if value.Value + increment > limit { fmt.Fprintf(os.Stderr, "counter exceeds %%d\n", limit); os.Exit(1) }
		value.Value += increment
		if err := save(value); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
		fmt.Println(value.Value)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: counter get | counter add N")
	os.Exit(2)
}
`, limit)
	testSource := fmt.Sprintf("package main\n\nimport \"testing\"\nfunc TestLimit(t *testing.T) { if limit != %d { t.Fatal(limit) } }\n", limit)
	generated := GeneratedSet{
		Files: []GeneratedFile{
			{Path: "main.go", Content: mainSource, SpecIDs: []string{"graph.counter"}},
			{Path: "main_test.go", Content: testSource, SpecIDs: []string{"graph.counter"}},
		},
		Artifacts: []GeneratedArtifact{{Name: "counter", Package: "."}}, Summary: "persistent counter",
	}
	if includeModule {
		generated.Files = append(generated.Files, GeneratedFile{Path: "go.mod", Content: "module example.test/counter\n\ngo 1.25\n"})
	}
	encoded, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func cloneTestFields(fields map[string]any) map[string]any {
	result := make(map[string]any, len(fields))
	for key, value := range fields {
		result[key] = value
	}
	return result
}
