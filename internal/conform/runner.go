package conform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	semantictrace "github.com/promisegrid/gridspec/conformance/trace"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
)

type scenarioEvidence struct {
	Class    string
	SpecIDs  map[string]bool
	TraceIDs map[string]bool
}

func runScenarios(cfg Config, ir model.IR, locations map[string]model.Location) ([]scenarioEvidence, []diag.Diagnostic) {
	if len(cfg.Scenarios) == 0 {
		return nil, nil
	}
	tempRoot, err := os.MkdirTemp("", "gridspec-conform-*")
	if err != nil {
		return nil, []diag.Diagnostic{conformDiagnostic("SDC200", model.Location{File: cfg.Path}, "create conformance workspace: "+err.Error(), "", "make the system temporary directory writable")}
	}
	defer os.RemoveAll(tempRoot)
	artifacts, buildDS := buildArtifacts(cfg, tempRoot)
	if len(buildDS) != 0 {
		return nil, buildDS
	}
	var evidence []scenarioEvidence
	var diagnostics []diag.Diagnostic
	for _, scenario := range cfg.Scenarios {
		loc := locations[scenario.SpecID]
		if loc.File == "" {
			loc = model.Location{File: cfg.Path}
		}
		var result scenarioEvidence
		var ds []diag.Diagnostic
		if scenario.Command != nil {
			result, ds = runCommandScenario(cfg, ir, scenario, artifacts[scenario.Command.Artifact], tempRoot, loc)
		} else {
			result, ds = runGoTestScenario(cfg, ir, scenario, tempRoot, loc)
		}
		diagnostics = append(diagnostics, ds...)
		if len(ds) == 0 {
			evidence = append(evidence, result)
		}
	}
	return evidence, diagnostics
}

func buildArtifacts(cfg Config, tempRoot string) (map[string]string, []diag.Diagnostic) {
	needed := make(map[string]bool)
	for _, scenario := range cfg.Scenarios {
		if scenario.Command != nil {
			needed[scenario.Command.Artifact] = true
		}
	}
	artifacts := make(map[string]string)
	for _, artifact := range cfg.Go.Artifacts {
		if !needed[artifact.Name] {
			continue
		}
		path := filepath.Join(tempRoot, "bin", artifact.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, []diag.Diagnostic{conformDiagnostic("SDC200", model.Location{File: cfg.Path}, err.Error(), "", "make the temporary artifact directory writable")}
		}
		cmd := exec.Command("go", "build", "-o", path, artifact.Package)
		cmd.Dir = cfg.ModuleRoot
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			return nil, []diag.Diagnostic{conformDiagnostic("SDC201", model.Location{File: cfg.Path}, fmt.Sprintf("build artifact %q: %v: %s", artifact.Name, err, strings.TrimSpace(output.String())), "", "repair the configured Go package before running scenarios")}
		}
		artifacts[artifact.Name] = path
	}
	return artifacts, nil
}

func runCommandScenario(cfg Config, ir model.IR, scenario Scenario, binary, tempRoot string, loc model.Location) (scenarioEvidence, []diag.Diagnostic) {
	result := scenarioEvidence{Class: "command", SpecIDs: coveredIDs(scenario), TraceIDs: make(map[string]bool)}
	repeat := scenario.Command.Repeat
	if repeat == 0 {
		repeat = 1
	}
	var firstStdout, firstStderr []byte
	for run := 1; run <= repeat; run++ {
		work := filepath.Join(tempRoot, "work", safeName(scenario.SpecID), fmt.Sprintf("%d", run))
		fixture, _ := cfg.Resolve(scenario.Command.Fixture)
		if err := copyTree(fixture, work); err != nil {
			return result, []diag.Diagnostic{conformDiagnostic("SDC202", loc, fmt.Sprintf("scenario %s fixture: %v", scenario.SpecID, err), scenario.SpecID, "use a directory of regular fixture files contained by the sidecar")}
		}
		tracePath := filepath.Join(tempRoot, "traces", safeName(scenario.SpecID), fmt.Sprintf("%d.ndjson", run))
		if len(scenario.Trace.Ordered) != 0 {
			if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
				return result, []diag.Diagnostic{conformDiagnostic("SDC300", loc, err.Error(), scenario.SpecID, "make the temporary trace directory writable")}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), runnerTimeout(scenario.Command.Timeout))
		cmd := exec.CommandContext(ctx, binary, scenario.Command.Args...)
		cmd.Dir = work
		overrides := make(map[string]string, len(scenario.Command.Env)+2)
		for key, value := range scenario.Command.Env {
			overrides[key] = value
		}
		if len(scenario.Trace.Ordered) != 0 {
			overrides[semantictrace.FileEnv] = tracePath
			overrides[semantictrace.ScenarioEnv] = scenario.SpecID
		}
		cmd.Env = mergedEnvironment(overrides)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, []diag.Diagnostic{conformDiagnostic("SDC203", loc, fmt.Sprintf("scenario %s exceeded %s", scenario.SpecID, runnerTimeout(scenario.Command.Timeout)), scenario.SpecID, "increase the reviewed timeout or repair the stalled implementation")}
		}
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				return result, []diag.Diagnostic{conformDiagnostic("SDC203", loc, fmt.Sprintf("start scenario %s: %v", scenario.SpecID, err), scenario.SpecID, "make the built artifact executable")}
			}
		}
		if exitCode != *scenario.Expect.ExitCode {
			return result, []diag.Diagnostic{conformDiagnostic("SDC204", loc, fmt.Sprintf("scenario %s exit code is %d, want %d", scenario.SpecID, exitCode, *scenario.Expect.ExitCode), scenario.SpecID, "repair the implementation or review the expected public outcome")}
		}
		normalize := func(data []byte) []byte {
			text := string(data)
			text = strings.ReplaceAll(text, work, "<WORK>")
			text = strings.ReplaceAll(text, cfg.ModuleRoot, "<MODULE>")
			text = strings.ReplaceAll(text, binary, "<BINARY>")
			return []byte(text)
		}
		out, errOut := normalize(stdout.Bytes()), normalize(stderr.Bytes())
		if ds := matchStream(cfg, loc, scenario.SpecID, "stdout", out, scenario.Expect.Stdout); len(ds) != 0 {
			return result, ds
		}
		if ds := matchStream(cfg, loc, scenario.SpecID, "stderr", errOut, scenario.Expect.Stderr); len(ds) != 0 {
			return result, ds
		}
		if ds := matchFiles(cfg, loc, scenario.SpecID, work, scenario.Expect.Files); len(ds) != 0 {
			return result, ds
		}
		if run == 1 {
			firstStdout, firstStderr = append([]byte(nil), out...), append([]byte(nil), errOut...)
		} else if !bytes.Equal(firstStdout, out) || !bytes.Equal(firstStderr, errOut) {
			return result, []diag.Diagnostic{conformDiagnostic("SDC205", loc, fmt.Sprintf("scenario %s produced different normalized output on repeat %d", scenario.SpecID, run), scenario.SpecID, "remove nondeterminism or declare a more precise stable assertion")}
		}
		if len(scenario.Trace.Ordered) != 0 {
			traceIDs, ds := checkTrace(ir, scenario, tracePath, loc)
			if len(ds) != 0 {
				return result, ds
			}
			for id := range traceIDs {
				result.TraceIDs[id] = true
			}
		}
	}
	return result, nil
}

func runGoTestScenario(cfg Config, ir model.IR, scenario Scenario, tempRoot string, loc model.Location) (scenarioEvidence, []diag.Diagnostic) {
	result := scenarioEvidence{Class: "go_test", SpecIDs: coveredIDs(scenario), TraceIDs: make(map[string]bool)}
	tracePath := filepath.Join(tempRoot, "traces", safeName(scenario.SpecID)+".ndjson")
	if len(scenario.Trace.Ordered) != 0 {
		if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
			return result, []diag.Diagnostic{conformDiagnostic("SDC300", loc, err.Error(), scenario.SpecID, "make the temporary trace directory writable")}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerTimeout(scenario.GoTest.Timeout))
	defer cancel()
	testPattern := "^" + regexp.QuoteMeta(scenario.GoTest.Test) + "$"
	cmd := exec.CommandContext(ctx, "go", "test", "-json", scenario.GoTest.Package, "-run", testPattern, "-count=1")
	cmd.Dir = cfg.ModuleRoot
	overrides := make(map[string]string, 2)
	if len(scenario.Trace.Ordered) != 0 {
		overrides[semantictrace.FileEnv] = tracePath
		overrides[semantictrace.ScenarioEnv] = scenario.SpecID
	}
	cmd.Env = mergedEnvironment(overrides)
	output, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, []diag.Diagnostic{conformDiagnostic("SDC210", loc, fmt.Sprintf("Go test scenario %s exceeded %s", scenario.SpecID, runnerTimeout(scenario.GoTest.Timeout)), scenario.SpecID, "increase the reviewed timeout or repair the stalled test")}
	}
	found, passed := goTestOutcome(output, scenario.GoTest.Test)
	if !found {
		return result, []diag.Diagnostic{conformDiagnostic("SDC211", loc, fmt.Sprintf("Go test scenario %s did not run exact test %s", scenario.SpecID, scenario.GoTest.Test), scenario.SpecID, "correct the package/test binding; a package-level pass is not scenario evidence")}
	}
	if err != nil || !passed {
		return result, []diag.Diagnostic{conformDiagnostic("SDC212", loc, fmt.Sprintf("Go test scenario %s failed: %v: %s", scenario.SpecID, err, strings.TrimSpace(string(output))), scenario.SpecID, "repair the bound test or implementation")}
	}
	if len(scenario.Trace.Ordered) != 0 {
		traceIDs, ds := checkTrace(ir, scenario, tracePath, loc)
		if len(ds) != 0 {
			return result, ds
		}
		result.TraceIDs = traceIDs
	}
	return result, nil
}

func goTestOutcome(output []byte, testName string) (found, passed bool) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Test != testName {
			continue
		}
		if event.Action == "run" {
			found = true
		}
		if event.Action == "pass" {
			passed = true
		}
		if event.Action == "fail" {
			passed = false
		}
	}
	return found, passed
}

func matchStream(cfg Config, loc model.Location, scenarioID, label string, actual []byte, want StreamExpectation) []diag.Diagnostic {
	failed := func(message string) []diag.Diagnostic {
		return []diag.Diagnostic{conformDiagnostic("SDC220", loc, fmt.Sprintf("scenario %s %s %s", scenarioID, label, message), scenarioID, "repair the implementation or update the reviewed exact expectation")}
	}
	if want.Empty && len(actual) != 0 {
		return failed("is not empty")
	}
	if want.Equals != nil && string(actual) != *want.Equals {
		return failed("does not equal the inline expectation")
	}
	for _, fragment := range want.Contains {
		if !bytes.Contains(actual, []byte(fragment)) {
			return failed(fmt.Sprintf("does not contain %q", fragment))
		}
	}
	if want.File != "" {
		path, _ := cfg.Resolve(want.File)
		expected, err := os.ReadFile(path)
		if err != nil {
			return failed("expectation file cannot be read: " + err.Error())
		}
		if !bytes.Equal(actual, expected) {
			return failed("does not equal " + want.File)
		}
	}
	if want.JSONFile != "" {
		path, _ := cfg.Resolve(want.JSONFile)
		expected, err := os.ReadFile(path)
		if err != nil {
			return failed("JSON expectation cannot be read: " + err.Error())
		}
		if !equalJSON(actual, expected) {
			return failed("is not structurally equal to " + want.JSONFile)
		}
	}
	return nil
}

func matchFiles(cfg Config, loc model.Location, scenarioID, work string, files []FileExpectation) []diag.Diagnostic {
	for _, want := range files {
		path, err := containedJoin(work, want.Path)
		if err != nil {
			return []diag.Diagnostic{conformDiagnostic("SDC221", loc, fmt.Sprintf("scenario %s expected file: %v", scenarioID, err), scenarioID, "keep expected paths inside the fixture workspace")}
		}
		data, readErr := os.ReadFile(path)
		exists := readErr == nil
		wantExists := true
		if want.Exists != nil {
			wantExists = *want.Exists
		}
		if exists != wantExists {
			return []diag.Diagnostic{conformDiagnostic("SDC221", loc, fmt.Sprintf("scenario %s file %s existence is %t, want %t", scenarioID, want.Path, exists, wantExists), scenarioID, "repair the declared filesystem effect")}
		}
		if !exists {
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return []diag.Diagnostic{conformDiagnostic("SDC221", loc, fmt.Sprintf("scenario %s read file %s: %v", scenarioID, want.Path, readErr), scenarioID, "make the declared file readable")}
			}
			continue
		}
		if want.EqualsFile != "" {
			expectPath, _ := cfg.Resolve(want.EqualsFile)
			expected, err := os.ReadFile(expectPath)
			if err != nil || !bytes.Equal(data, expected) {
				return []diag.Diagnostic{conformDiagnostic("SDC221", loc, fmt.Sprintf("scenario %s file %s does not equal %s", scenarioID, want.Path, want.EqualsFile), scenarioID, "repair the file effect or update the reviewed golden file")}
			}
		}
		if want.JSONFile != "" {
			expectPath, _ := cfg.Resolve(want.JSONFile)
			expected, err := os.ReadFile(expectPath)
			if err != nil || !equalJSON(data, expected) {
				return []diag.Diagnostic{conformDiagnostic("SDC221", loc, fmt.Sprintf("scenario %s file %s is not structurally equal to %s", scenarioID, want.Path, want.JSONFile), scenarioID, "repair the JSON effect or update the reviewed golden file")}
			}
		}
	}
	return nil
}

func checkTrace(ir model.IR, scenario Scenario, path string, loc model.Location) (map[string]bool, []diag.Diagnostic) {
	f, err := os.Open(path)
	if err != nil {
		return nil, []diag.Diagnostic{conformDiagnostic("SDC300", loc, fmt.Sprintf("scenario %s did not publish its required trace: %v", scenario.SpecID, err), scenario.SpecID, "integrate the semantic trace sink for this execution path")}
	}
	defer f.Close()
	var events []semantictrace.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event semantictrace.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, []diag.Diagnostic{conformDiagnostic("SDC301", loc, fmt.Sprintf("scenario %s trace is invalid NDJSON: %v", scenario.SpecID, err), scenario.SpecID, "emit one gridspec.trace/v1 event per line")}
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, []diag.Diagnostic{conformDiagnostic("SDC301", loc, fmt.Sprintf("read scenario %s trace: %v", scenario.SpecID, err), scenario.SpecID, "make the trace readable")}
	}
	ids := make(map[string]bool)
	for i, event := range events {
		if event.Schema != semantictrace.Schema || event.ScenarioID != scenario.SpecID || event.Sequence != i+1 {
			return nil, []diag.Diagnostic{conformDiagnostic("SDC301", loc, fmt.Sprintf("scenario %s trace event %d has invalid schema, scenario, or sequence", scenario.SpecID, i+1), scenario.SpecID, "emit deterministic gridspec.trace/v1 events numbered from one")}
		}
		if !hasSpecID(ir, event.SpecID) {
			return nil, []diag.Diagnostic{conformDiagnostic("SDC302", loc, fmt.Sprintf("scenario %s trace references unknown spec ID %s", scenario.SpecID, event.SpecID), scenario.SpecID, "emit only semantic IDs in the vetted IR")}
		}
		ids[event.SpecID] = true
	}
	position := 0
	for _, want := range scenario.Trace.Ordered {
		found := false
		for position < len(events) {
			event := events[position]
			position++
			if event.SpecID == want.SpecID && (want.Phase == "" || event.Phase == want.Phase) && (want.Outcome == "" || event.Outcome == want.Outcome) {
				found = true
				break
			}
		}
		if !found {
			return nil, []diag.Diagnostic{conformDiagnostic("SDC303", loc, fmt.Sprintf("scenario %s trace is missing ordered event %s", scenario.SpecID, want.SpecID), scenario.SpecID, "emit the required semantic event after its preceding expected event")}
		}
	}
	return ids, nil
}

func coveredIDs(scenario Scenario) map[string]bool {
	ids := map[string]bool{scenario.SpecID: true}
	for _, id := range scenario.Covers {
		ids[id] = true
	}
	return ids
}

func equalJSON(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ac, _ := json.Marshal(av)
	bc, _ := json.Marshal(bv)
	return bytes.Equal(ac, bc)
}

func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fixture root %s is a symlink", src)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture symlink %s is not allowed", rel)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture entry %s is not a regular file", rel)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func containedJoin(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path %q is not allowed", path)
	}
	joined := filepath.Join(root, path)
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the fixture", path)
	}
	return joined, nil
}

func safeName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}
