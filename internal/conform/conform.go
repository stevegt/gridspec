package conform

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/goworkflow"
	"github.com/promisegrid/gridspec/internal/model"
)

type RunOptions struct {
	IgnoreBaseline bool
}

type Report struct {
	Schema         string                           `json:"schema"`
	Module         string                           `json:"module"`
	Implementation string                           `json:"implementation"`
	SpecDigest     string                           `json:"spec_digest"`
	Evidence       map[string][]string              `json:"evidence"`
	Coverage       map[string]CoverageResult        `json:"coverage"`
	Exclusions     []ExcludedID                     `json:"exclusions,omitempty"`
	AcceptedRuns   []goworkflow.ScenarioObservation `json:"accepted_scenario_runs,omitempty"`
	Diagnostics    []diag.Diagnostic                `json:"diagnostics"`
}

type CoverageResult struct {
	Eligible int     `json:"eligible"`
	Covered  int     `json:"covered"`
	Percent  float64 `json:"percent"`
	Floor    float64 `json:"floor"`
}

type ExcludedID struct {
	SpecID string `json:"spec_id"`
	Reason string `json:"reason"`
}

type Baseline struct {
	Schema         string              `json:"schema"`
	Module         string              `json:"module"`
	Implementation string              `json:"implementation"`
	SpecDigest     string              `json:"spec_digest"`
	Evidence       map[string][]string `json:"evidence"`
	Exclusions     []string            `json:"exclusions,omitempty"`
}

func Run(cfg Config, ir model.IR, opts RunOptions) Report {
	locations, active, kinds := indexIR(ir)
	evidenceSets := map[string]map[string]bool{
		"static": {}, "command": {}, "go_test": {}, "trace": {},
	}
	var diagnostics []diag.Diagnostic
	var acceptedRuns []goworkflow.ScenarioObservation
	diagnostics = append(diagnostics, validateReferences(cfg, ir, locations, kinds)...)
	if !diag.HasErrors(diagnostics) {
		static, ds := resolveBindings(cfg, ir, locations)
		for id, ok := range static {
			if ok {
				evidenceSets["static"][id] = true
			}
		}
		diagnostics = append(diagnostics, ds...)
		if !diag.HasErrors(diagnostics) {
			runs, observations, runDS := runScenarios(cfg, ir, locations)
			acceptedRuns = observations
			diagnostics = append(diagnostics, runDS...)
			for _, run := range runs {
				for id := range run.SpecIDs {
					evidenceSets[run.Class][id] = true
				}
				for id := range run.TraceIDs {
					evidenceSets["trace"][id] = true
				}
			}
		}
	}
	exclusions := make(map[string]string)
	for _, group := range cfg.Exclusions {
		for _, id := range group.SpecIDs {
			if previous, exists := exclusions[id]; exists {
				diagnostics = append(diagnostics, conformDiagnostic("SDC402", locationFor(locations, id, cfg.Path), fmt.Sprintf("spec ID %s is excluded more than once (%q and %q)", id, previous, group.Reason), id, "list each excluded semantic ID exactly once"))
				continue
			}
			exclusions[id] = group.Reason
		}
	}
	for id := range active {
		covered := false
		for _, class := range []string{"static", "command", "go_test", "trace"} {
			covered = covered || evidenceSets[class][id]
		}
		if covered && exclusions[id] != "" {
			diagnostics = append(diagnostics, conformDiagnostic("SDC403", locationFor(locations, id, cfg.Path), fmt.Sprintf("spec ID %s has evidence and is also excluded", id), id, "remove the obsolete exclusion or the incorrect evidence binding"))
		}
		if !covered && exclusions[id] == "" {
			diagnostics = append(diagnostics, conformDiagnostic("SDC400", locationFor(locations, id, cfg.Path), fmt.Sprintf("active spec ID %s has no implementation evidence or explicit exclusion", id), id, "add a static binding, scenario/trace evidence, or a reviewed exclusion with rationale"))
		}
	}
	if cfg.Coverage.MaxExclusions != nil && len(exclusions) > *cfg.Coverage.MaxExclusions {
		diagnostics = append(diagnostics, conformDiagnostic("SDC404", model.Location{File: cfg.Path}, fmt.Sprintf("implementation has %d exclusions, exceeding limit %d", len(exclusions), *cfg.Coverage.MaxExclusions), "", "reduce exclusions or deliberately raise the reviewed limit"))
	}
	coverage := make(map[string]CoverageResult)
	eligible := len(active) - countActiveExclusions(active, exclusions)
	for _, class := range []string{"static", "command", "go_test", "trace"} {
		covered := 0
		for id := range evidenceSets[class] {
			if active[id] && exclusions[id] == "" {
				covered++
			}
		}
		percent := 100.0
		if eligible > 0 {
			percent = float64(covered) * 100 / float64(eligible)
		}
		floor := cfg.Coverage.Floors[class]
		coverage[class] = CoverageResult{Eligible: eligible, Covered: covered, Percent: percent, Floor: floor}
		if percent+1e-9 < floor {
			diagnostics = append(diagnostics, conformDiagnostic("SDC405", model.Location{File: cfg.Path}, fmt.Sprintf("%s evidence coverage %.2f%% is below floor %.2f%%", class, percent, floor), "", "add evidence or deliberately lower the reviewed floor"))
		}
	}
	evidence := sortedEvidence(evidenceSets)
	excluded := sortedExclusions(exclusions)
	if !opts.IgnoreBaseline && cfg.Coverage.Baseline != "" {
		path, _ := cfg.Resolve(cfg.Coverage.Baseline)
		baseline, err := loadBaseline(path)
		if err != nil {
			diagnostics = append(diagnostics, conformDiagnostic("SDC410", model.Location{File: cfg.Path}, "load conformance baseline: "+err.Error(), "", "emit and review a baseline with --emit-baseline"))
		} else {
			diagnostics = append(diagnostics, compareBaseline(cfg, ir.Module, baseline, evidence, exclusions, locations)...)
		}
	}
	diag.Sort(diagnostics)
	if diagnostics == nil {
		diagnostics = []diag.Diagnostic{}
	}
	return Report{
		Schema: ReportSchema, Module: ir.Module, Implementation: cfg.Name, SpecDigest: ir.Digest,
		Evidence: evidence, Coverage: coverage, Exclusions: excluded,
		AcceptedRuns: acceptedRuns, Diagnostics: diagnostics,
	}
}

func validateReferences(cfg Config, ir model.IR, locations map[string]model.Location, kinds map[string]string) []diag.Diagnostic {
	var ds []diag.Diagnostic
	acceptedCommands := make(map[string]bool)
	for _, contract := range ir.Contracts {
		if (contract.Status == "" || contract.Status == "active") && contract.Scenario != nil {
			acceptedCommands[contract.For] = true
		}
	}
	check := func(id, use string) {
		if !hasSpecID(ir, id) {
			ds = append(ds, conformDiagnostic("SDC001", model.Location{File: cfg.Path}, fmt.Sprintf("%s references unknown spec ID %s", use, id), id, "use an active semantic ID from the vetted IR or remove the stale entry"))
		}
	}
	for _, binding := range cfg.Bindings {
		check(binding.SpecID, "binding")
	}
	for _, scenario := range cfg.Scenarios {
		check(scenario.SpecID, "scenario")
		if scenario.Command != nil && acceptedCommands[scenario.SpecID] {
			ds = append(ds, conformDiagnostic("SDC003", locationFor(locations, scenario.SpecID, cfg.Path), fmt.Sprintf("sidecar command scenario %s duplicates an accepted command contract", scenario.SpecID), scenario.SpecID, "remove command arguments and expectations from the sidecar; accepted specification state is the behavioral authority"))
		}
		if kind := kinds[scenario.SpecID]; kind != "" && kind != "scenario" {
			ds = append(ds, conformDiagnostic("SDC002", locationFor(locations, scenario.SpecID, cfg.Path), fmt.Sprintf("scenario binding %s targets kind %s, not scenario", scenario.SpecID, kind), scenario.SpecID, "bind runners to a scenario graph"))
		}
		for _, id := range scenario.Covers {
			check(id, "scenario coverage")
		}
		for _, event := range scenario.Trace.Ordered {
			check(event.SpecID, "trace expectation")
		}
	}
	for _, exclusion := range cfg.Exclusions {
		for _, id := range exclusion.SpecIDs {
			check(id, "exclusion")
		}
	}
	return ds
}

func indexIR(ir model.IR) (map[string]model.Location, map[string]bool, map[string]string) {
	locations := make(map[string]model.Location)
	active := make(map[string]bool)
	kinds := make(map[string]string)
	for _, graph := range ir.Graphs {
		locations[graph.ID], kinds[graph.ID] = graph.Location, graph.Kind
		if graph.Status == "active" {
			active[graph.ID] = true
		}
		for _, element := range append(append([]model.Element(nil), graph.Nodes...), graph.Edges...) {
			locations[element.ID], kinds[element.ID] = element.Location, element.Kind
			if element.Status == "active" {
				active[element.ID] = true
			}
		}
	}
	return locations, active, kinds
}

func hasSpecID(ir model.IR, id string) bool {
	for _, graph := range ir.Graphs {
		if graph.ID == id {
			return true
		}
		for _, element := range graph.Nodes {
			if element.ID == id {
				return true
			}
		}
		for _, element := range graph.Edges {
			if element.ID == id {
				return true
			}
		}
	}
	return false
}

func locationFor(locations map[string]model.Location, id, fallback string) model.Location {
	if loc := locations[id]; loc.File != "" {
		return loc
	}
	return model.Location{File: fallback}
}

func conformDiagnostic(rule string, location model.Location, message, elementID, help string) diag.Diagnostic {
	return diag.Diagnostic{
		Rule: rule, Severity: diag.SeverityError, Message: message, Help: help,
		ElementID: elementID, Waivable: false,
		Location: diag.Location{File: location.File, Line: location.Line, Column: location.Column},
	}
}

func countActiveExclusions(active map[string]bool, exclusions map[string]string) int {
	count := 0
	for id := range exclusions {
		if active[id] {
			count++
		}
	}
	return count
}

func sortedEvidence(sets map[string]map[string]bool) map[string][]string {
	result := make(map[string][]string, len(sets))
	for _, class := range []string{"static", "command", "go_test", "trace"} {
		for id := range sets[class] {
			result[class] = append(result[class], id)
		}
		sort.Strings(result[class])
		if result[class] == nil {
			result[class] = []string{}
		}
	}
	return result
}

func sortedExclusions(exclusions map[string]string) []ExcludedID {
	ids := make([]string, 0, len(exclusions))
	for id := range exclusions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ExcludedID, len(ids))
	for i, id := range ids {
		result[i] = ExcludedID{SpecID: id, Reason: exclusions[id]}
	}
	return result
}

func loadBaseline(path string) (Baseline, error) {
	f, err := os.Open(path)
	if err != nil {
		return Baseline{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var baseline Baseline
	if err := dec.Decode(&baseline); err != nil {
		return Baseline{}, err
	}
	if baseline.Schema != BaselineSchema {
		return Baseline{}, fmt.Errorf("unsupported schema %q", baseline.Schema)
	}
	if baseline.Module == "" || baseline.Implementation == "" || baseline.SpecDigest == "" {
		return Baseline{}, fmt.Errorf("baseline module, implementation, and spec_digest are required")
	}
	allowed := map[string]bool{"static": true, "command": true, "go_test": true, "trace": true}
	for class := range baseline.Evidence {
		if !allowed[class] {
			return Baseline{}, fmt.Errorf("baseline has unknown evidence class %q", class)
		}
	}
	for class := range allowed {
		ids, ok := baseline.Evidence[class]
		if !ok {
			return Baseline{}, fmt.Errorf("baseline is missing evidence class %q", class)
		}
		seen := make(map[string]bool)
		for _, id := range ids {
			if id == "" || seen[id] {
				return Baseline{}, fmt.Errorf("baseline evidence class %q has an empty or duplicate ID %q", class, id)
			}
			seen[id] = true
		}
	}
	seenExclusion := make(map[string]bool)
	for _, id := range baseline.Exclusions {
		if id == "" || seenExclusion[id] {
			return Baseline{}, fmt.Errorf("baseline has an empty or duplicate exclusion %q", id)
		}
		seenExclusion[id] = true
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return Baseline{}, fmt.Errorf("baseline must contain exactly one JSON value")
	}
	return baseline, nil
}

func compareBaseline(cfg Config, module string, baseline Baseline, current map[string][]string, exclusions map[string]string, locations map[string]model.Location) []diag.Diagnostic {
	var ds []diag.Diagnostic
	if baseline.Module != module {
		ds = append(ds, conformDiagnostic("SDC411", model.Location{File: cfg.Path}, fmt.Sprintf("baseline module is %q, want %q", baseline.Module, module), "", "emit a baseline for this specification module"))
		return ds
	}
	if baseline.Implementation != cfg.Name {
		ds = append(ds, conformDiagnostic("SDC411", model.Location{File: cfg.Path}, fmt.Sprintf("baseline implementation is %q, want %q", baseline.Implementation, cfg.Name), "", "emit a baseline for this implementation"))
		return ds
	}
	for _, class := range []string{"static", "command", "go_test", "trace"} {
		currentSet := make(map[string]bool)
		for _, id := range current[class] {
			currentSet[id] = true
		}
		for _, id := range baseline.Evidence[class] {
			if !currentSet[id] {
				ds = append(ds, conformDiagnostic("SDC412", locationFor(locations, id, cfg.Path), fmt.Sprintf("%s evidence for %s regressed from the checked-in baseline", class, id), id, "restore the evidence or explicitly review and replace the baseline"))
			}
		}
	}
	baselineExclusions := make(map[string]bool)
	for _, id := range baseline.Exclusions {
		baselineExclusions[id] = true
	}
	for id := range exclusions {
		if !baselineExclusions[id] {
			ds = append(ds, conformDiagnostic("SDC413", locationFor(locations, id, cfg.Path), fmt.Sprintf("exclusion for %s is new since the checked-in baseline", id), id, "add implementation evidence or explicitly review and replace the baseline"))
		}
	}
	return ds
}

func (r Report) Baseline() Baseline {
	exclusions := make([]string, len(r.Exclusions))
	for i, excluded := range r.Exclusions {
		exclusions[i] = excluded.SpecID
	}
	return Baseline{
		Schema: BaselineSchema, Module: r.Module, Implementation: r.Implementation,
		SpecDigest: r.SpecDigest, Evidence: r.Evidence, Exclusions: exclusions,
	}
}

func WriteBaseline(path string, baseline Baseline) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".gridspec-baseline-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(baseline); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, abs)
}
