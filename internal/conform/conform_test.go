package conform

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/model"
)

func TestRunRequiresEvidenceOrExplicitExclusion(t *testing.T) {
	ir := model.IR{
		Schema: model.IRSchema, Module: "example", Digest: "sha256:test",
		Graphs: []model.Graph{{ID: "graph.example", Kind: "decision", Status: "active", Location: model.Location{File: "example.dot", Line: 1}}},
	}
	cfg := Config{Name: "example-go", Path: "gridspec.impl.yaml", Coverage: CoveragePolicy{Floors: map[string]float64{}}}
	report := Run(cfg, ir, RunOptions{IgnoreBaseline: true})
	if !hasRule(report, "SDC400") {
		t.Fatalf("expected unclassified-ID diagnostic: %#v", report.Diagnostics)
	}
	cfg.Exclusions = []Exclusion{{SpecIDs: []string{"graph.example"}, Reason: "architecture declaration has no direct code object"}}
	report = Run(cfg, ir, RunOptions{IgnoreBaseline: true})
	if len(report.Diagnostics) != 0 {
		t.Fatalf("explicit exclusion should classify the ID: %#v", report.Diagnostics)
	}
}

func TestBaselineRatchetsEvidenceClass(t *testing.T) {
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "baseline.json")
	baseline := Baseline{
		Schema: BaselineSchema, Module: "example", Implementation: "example-go", SpecDigest: "old",
		Evidence: map[string][]string{"static": {}, "command": {"scenario.example"}, "go_test": {}, "trace": {}},
	}
	if err := WriteBaseline(baselinePath, baseline); err != nil {
		t.Fatal(err)
	}
	ir := model.IR{Schema: model.IRSchema, Module: "example", Digest: "new", Graphs: []model.Graph{{ID: "scenario.example", Kind: "scenario", Status: "active", Location: model.Location{File: "scenario.dot"}}}}
	cfg := Config{
		Name: "example-go", Path: filepath.Join(dir, "gridspec.impl.yaml"), Dir: dir,
		Exclusions: []Exclusion{{SpecIDs: []string{"scenario.example"}, Reason: "test setup"}},
		Coverage:   CoveragePolicy{Baseline: "baseline.json", Floors: map[string]float64{}},
	}
	report := Run(cfg, ir, RunOptions{})
	if !hasRule(report, "SDC412") {
		t.Fatalf("expected baseline regression: %#v", report.Diagnostics)
	}
	if !hasRule(report, "SDC413") {
		t.Fatalf("expected new-exclusion regression: %#v", report.Diagnostics)
	}
}

func TestSidecarRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gridspec.impl.yaml")
	data := []byte("schema: gridspec.implementation/v1\nname: example\ngo: {}\ncoverage: {}\nunknown: true\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected strict sidecar decoding error")
	}
}

func TestSidecarRequiresCoverageRatchet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gridspec.impl.yaml")
	data := []byte("schema: gridspec.implementation/v1\nname: example\ngo: {}\ncoverage: {}\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected missing baseline/floors/exclusion-limit error")
	}
}

func TestGoTestOutcomeRequiresExactTest(t *testing.T) {
	output := []byte("{\"Action\":\"run\",\"Test\":\"TestExact\"}\n{\"Action\":\"pass\",\"Test\":\"TestExact\"}\n")
	if found, passed := goTestOutcome(output, "TestExact"); !found || !passed {
		t.Fatalf("found=%t passed=%t", found, passed)
	}
	if found, _ := goTestOutcome(output, "TestOther"); found {
		t.Fatal("unexpected evidence for an unexecuted test")
	}
}

func TestAcceptedScenarioQualifiesExistingPersistentCode(t *testing.T) {
	root := t.TempDir()
	module := []byte("module example.test/existing\n\ngo 1.25\n")
	source := []byte(`package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type state struct { Value int ` + "`json:\"value\"`" + ` }

func load() state {
	data, err := os.ReadFile("counter.json")
	if os.IsNotExist(err) { return state{} }
	if err != nil { panic(err) }
	var value state
	if err := json.Unmarshal(data, &value); err != nil { panic(err) }
	return value
}

func main() {
	value := load()
	if len(os.Args) == 2 && os.Args[1] == "get" { fmt.Println(value.Value); return }
	if len(os.Args) == 3 && os.Args[1] == "add" {
		amount, err := strconv.Atoi(os.Args[2]); if err != nil { panic(err) }
		value.Value += amount
		data, _ := json.Marshal(value); data = append(data, '\n')
		if err := os.WriteFile("counter.json", data, 0600); err != nil { panic(err) }
		fmt.Println(value.Value); return
	}
	os.Exit(2)
}
`)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), module, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	ir := existingCounterIR()
	maximum := 0
	cfg := Config{
		Name: "existing-counter", Path: filepath.Join(root, "gridspec.impl.yaml"), Dir: root, ModuleRoot: root,
		Go:       &GoConfig{Artifacts: []Artifact{{Name: "counter", Package: "."}}},
		Coverage: CoveragePolicy{Floors: map[string]float64{"static": 0, "command": 0, "go_test": 0, "trace": 0}, MaxExclusions: &maximum},
	}
	report := Run(cfg, ir, RunOptions{IgnoreBaseline: true})
	if len(report.Diagnostics) != 0 {
		t.Fatalf("accepted existing-code scenario failed: %#v", report.Diagnostics)
	}
	if strings.Join(report.Evidence["command"], ",") != "scenario.counter" || len(report.AcceptedRuns) != 1 || len(report.AcceptedRuns[0].Repeats) != 2 {
		t.Fatalf("command evidence=%v runs=%#v", report.Evidence["command"], report.AcceptedRuns)
	}
	afterModule, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	afterSource, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(module, afterModule) || !bytes.Equal(source, afterSource) {
		t.Fatal("existing-code qualification modified the source module")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "go.mod" || entries[1].Name() != "main.go" {
		t.Fatalf("existing-code qualification added source files: %v", entries)
	}

	faulty := bytes.Replace(source, []byte("fmt.Println(value.Value); return\n\t}"), []byte("fmt.Println(value.Value + 1); return\n\t}"), 1)
	if err := os.WriteFile(filepath.Join(root, "main.go"), faulty, 0o600); err != nil {
		t.Fatal(err)
	}
	report = Run(cfg, ir, RunOptions{IgnoreBaseline: true})
	if !hasRule(report, "SDC206") || len(report.Evidence["command"]) != 0 {
		t.Fatalf("faulty existing code report=%#v", report)
	}
}

func TestSidecarCommandCannotOverrideAcceptedScenario(t *testing.T) {
	ir := existingCounterIR()
	exit := 0
	cfg := Config{
		Name: "duplicate", Path: "gridspec.impl.yaml", Go: &GoConfig{Artifacts: []Artifact{{Name: "counter", Package: "."}}},
		Scenarios: []Scenario{{
			SpecID: "scenario.counter", Command: &CommandRunner{Artifact: "counter", Fixture: "fixture"},
			Expect: ScenarioExpect{ExitCode: &exit},
		}},
		Coverage: CoveragePolicy{Floors: map[string]float64{}},
	}
	report := Run(cfg, ir, RunOptions{IgnoreBaseline: true})
	if !hasRule(report, "SDC003") {
		t.Fatalf("duplicate accepted oracle report=%#v", report.Diagnostics)
	}
}

func existingCounterIR() model.IR {
	exit := int64(0)
	empty := ""
	three := "3\n"
	exists := true
	jsonThree := `{"value":3}`
	file := model.ScenarioFileExpectation{Path: "counter.json", Exists: &exists, JSON: &jsonThree}
	step := func(id string, args []string, stdout string) model.ScenarioStep {
		return model.ScenarioStep{
			ID: id, Args: args, Timeout: "5s",
			Expect: model.ScenarioExpect{ExitCode: &exit, Stdout: &stdout, Stderr: &empty, Files: []model.ScenarioFileExpectation{file}},
		}
	}
	return model.IR{
		Schema: model.IRSchema, Module: "example.test/existing", Digest: "sha256:existing-counter",
		Graphs: []model.Graph{{ID: "scenario.counter", Title: "Existing counter", Kind: "scenario", Status: "active"}},
		Contracts: []model.Contract{{
			Schema: "gridspec.contract/v1", For: "scenario.counter", Kind: "scenario", Status: "active",
			Scenario: &model.ScenarioContract{
				Artifact: "counter", Covers: []string{"scenario.counter"}, Repeat: 2,
				Steps: []model.ScenarioStep{
					step("counter.add", []string{"add", "3"}, three),
					step("counter.get", []string{"get"}, three),
				},
			},
		}},
	}
}

func hasRule(report Report, rule string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == rule {
			return true
		}
	}
	return false
}
