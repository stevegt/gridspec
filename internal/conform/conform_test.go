package conform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/promisegrid/specdot/internal/model"
)

func TestRunRequiresEvidenceOrExplicitExclusion(t *testing.T) {
	ir := model.IR{
		Schema: model.IRSchema, Module: "example", Digest: "sha256:test",
		Graphs: []model.Graph{{ID: "graph.example", Kind: "decision", Status: "active", Location: model.Location{File: "example.dot", Line: 1}}},
	}
	cfg := Config{Name: "example-go", Path: "specdot.impl.yaml", Coverage: CoveragePolicy{Floors: map[string]float64{}}}
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
		Name: "example-go", Path: filepath.Join(dir, "specdot.impl.yaml"), Dir: dir,
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
	path := filepath.Join(t.TempDir(), "specdot.impl.yaml")
	data := []byte("schema: specdot.implementation/v1\nname: example\ngo: {}\ncoverage: {}\nunknown: true\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected strict sidecar decoding error")
	}
}

func TestSidecarRequiresCoverageRatchet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "specdot.impl.yaml")
	data := []byte("schema: specdot.implementation/v1\nname: example\ngo: {}\ncoverage: {}\n")
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

func hasRule(report Report, rule string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == rule {
			return true
		}
	}
	return false
}
