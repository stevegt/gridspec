package goworkflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/scenarios"
	"github.com/promisegrid/gridspec/internal/specsource"
	"github.com/promisegrid/gridspec/internal/specstate"
)

const qualificationSchema = "gridspec.qualification/v1"

const goToolTimeout = 2 * time.Minute

type QualificationReport struct {
	Schema       string                `json:"schema"`
	Source       ManifestSource        `json:"source"`
	OutputDigest string                `json:"output_digest"`
	Artifacts    []GeneratedArtifact   `json:"artifacts"`
	GoTest       ProcessObservation    `json:"go_test"`
	Scenarios    []ScenarioObservation `json:"scenarios"`
}

type ProcessObservation struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type ScenarioObservation struct {
	ID      string              `json:"id"`
	Covers  []string            `json:"covers"`
	Repeats []RepeatObservation `json:"repeats"`
}

type RepeatObservation struct {
	Run   int               `json:"run"`
	Steps []StepObservation `json:"steps"`
}

type StepObservation struct {
	ID       string            `json:"id"`
	ExitCode int               `json:"exit_code"`
	Stdout   string            `json:"stdout"`
	Stderr   string            `json:"stderr"`
	Files    []FileObservation `json:"files"`
}

type FileObservation struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256,omitempty"`
}

// ScenarioResult records one accepted command scenario without weakening the
// other results when another independent scenario fails.
type ScenarioResult struct {
	ID          string
	Covers      []string
	Observation ScenarioObservation
	Err         error
}

func qualify(ctx context.Context, root string, state specstate.State, source specsource.Identity, generated *GeneratedSet) error {
	definitions, err := scenarios.Definitions(state)
	if err != nil {
		return err
	}
	goTest, err := runGoTests(ctx, root)
	if err != nil {
		return err
	}
	results, err := RunAcceptedScenarios(ctx, root, definitions, generated.Artifacts)
	if err != nil {
		return err
	}
	report := QualificationReport{
		Schema: qualificationSchema, Source: manifestSource(source),
		Artifacts: append([]GeneratedArtifact(nil), generated.Artifacts...), GoTest: goTest,
	}
	for _, result := range results {
		if result.Err != nil {
			return fmt.Errorf("qualification scenario %s: %w", result.ID, result.Err)
		}
		report.Scenarios = append(report.Scenarios, result.Observation)
	}
	generated.Qualification = report
	digest, err := Digest(*generated)
	if err != nil {
		return err
	}
	generated.Qualification.OutputDigest = digest
	return nil
}

// RunAcceptedScenarios executes the language-neutral command oracle against an
// existing Go module. Generated builds and existing-code conformance call this
// same function, so artifact mappings cannot redefine behavior.
func RunAcceptedScenarios(ctx context.Context, root string, definitions []scenarios.Definition, artifacts []GeneratedArtifact) ([]ScenarioResult, error) {
	if err := validateArtifactCoverage(definitions, artifacts); err != nil {
		return nil, err
	}
	binaries, cleanup, err := buildArtifacts(ctx, root, definitions, artifacts)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	results := make([]ScenarioResult, 0, len(definitions))
	for _, definition := range definitions {
		observation, runErr := runScenario(ctx, root, definition, binaries[definition.Contract.Artifact])
		results = append(results, ScenarioResult{
			ID: definition.ID, Covers: append([]string(nil), definition.Contract.Covers...),
			Observation: observation, Err: runErr,
		})
	}
	return results, nil
}

func validateArtifactCoverage(definitions []scenarios.Definition, artifacts []GeneratedArtifact) error {
	byName := make(map[string]GeneratedArtifact, len(artifacts))
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		foldedName := strings.ToLower(artifact.Name)
		if !validArtifactName(artifact.Name) || strings.TrimSpace(artifact.Package) != artifact.Package || strings.HasPrefix(artifact.Package, "-") {
			return fmt.Errorf("invalid artifact mapping %q to package %q", artifact.Name, artifact.Package)
		}
		if seen[foldedName] {
			return fmt.Errorf("artifact name %q is duplicated", artifact.Name)
		}
		seen[foldedName] = true
		byName[artifact.Name] = artifact
	}
	for _, definition := range definitions {
		if _, ok := byName[definition.Contract.Artifact]; !ok {
			return fmt.Errorf("scenario %s references artifact %q, but the implementation did not map it to a Go package", definition.ID, definition.Contract.Artifact)
		}
	}
	return nil
}

func validArtifactName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func runGoTests(ctx context.Context, root string) (ProcessObservation, error) {
	testContext, cancel := context.WithTimeout(ctx, goToolTimeout)
	defer cancel()
	command := exec.CommandContext(testContext, "go", "test", "-buildvcs=false", "-mod=readonly", "./...")
	command.Dir = root
	command.Env = withEnvironment("GOWORK", "off")
	stdout, stderr := &limitedBuffer{limit: scenarios.MaxStreamBytes}, &limitedBuffer{limit: scenarios.MaxStreamBytes}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	observation := ProcessObservation{ExitCode: exitCode(err), Stdout: normalizeOutput(stdout.Bytes(), root, "", ""), Stderr: normalizeOutput(stderr.Bytes(), root, "", "")}
	if stdout.exceeded || stderr.exceeded {
		return observation, fmt.Errorf("generated Go test output exceeds %d bytes", scenarios.MaxStreamBytes)
	}
	if errors.Is(testContext.Err(), context.DeadlineExceeded) {
		return observation, fmt.Errorf("generated Go tests exceeded %s", goToolTimeout)
	}
	if err != nil {
		return observation, fmt.Errorf("generated Go tests failed: %s%s: %w", observation.Stdout, observation.Stderr, err)
	}
	// Successful `go test` package lines include elapsed time and cache markers.
	// Preserve the result, not those nondeterministic presentation details.
	return ProcessObservation{ExitCode: 0, Stdout: "pass\n", Stderr: ""}, nil
}

func buildArtifacts(ctx context.Context, root string, definitions []scenarios.Definition, artifacts []GeneratedArtifact) (map[string]string, func(), error) {
	needed := make(map[string]bool)
	for _, definition := range definitions {
		needed[definition.Contract.Artifact] = true
	}
	temp, err := os.MkdirTemp("", "gridspec-qualification-bin-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	result := make(map[string]string)
	for _, artifact := range artifacts {
		if !needed[artifact.Name] {
			continue
		}
		binary := filepath.Join(temp, artifact.Name)
		buildContext, cancel := context.WithTimeout(ctx, goToolTimeout)
		command := exec.CommandContext(buildContext, "go", "build", "-buildvcs=false", "-mod=readonly", "-o", binary, artifact.Package)
		command.Dir = root
		command.Env = withEnvironment("GOWORK", "off")
		output := &limitedBuffer{limit: scenarios.MaxStreamBytes}
		command.Stdout, command.Stderr = output, output
		err := command.Run()
		timedOut := errors.Is(buildContext.Err(), context.DeadlineExceeded)
		cancel()
		if timedOut {
			cleanup()
			return nil, func() {}, fmt.Errorf("build artifact %q exceeded %s", artifact.Name, goToolTimeout)
		}
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("build artifact %q: %s: %w", artifact.Name, strings.TrimSpace(string(output.Bytes())), err)
		}
		if output.exceeded {
			cleanup()
			return nil, func() {}, fmt.Errorf("build output for artifact %q exceeds %d bytes", artifact.Name, scenarios.MaxStreamBytes)
		}
		result[artifact.Name] = binary
	}
	return result, cleanup, nil
}

func runScenario(parent context.Context, moduleRoot string, definition scenarios.Definition, binary string) (ScenarioObservation, error) {
	result := ScenarioObservation{ID: definition.ID, Covers: append([]string(nil), definition.Contract.Covers...)}
	sort.Strings(result.Covers)
	for run := int64(1); run <= definition.Contract.Repeat; run++ {
		work, err := os.MkdirTemp("", "gridspec-qualification-work-")
		if err != nil {
			return result, err
		}
		repeat, runErr := runScenarioOnce(parent, moduleRoot, definition.Contract, binary, work, int(run))
		removeErr := os.RemoveAll(work)
		if runErr != nil {
			return result, runErr
		}
		if removeErr != nil {
			return result, removeErr
		}
		if len(result.Repeats) != 0 && !reflect.DeepEqual(result.Repeats[0].Steps, repeat.Steps) {
			return result, fmt.Errorf("repeat %d produced different normalized observations", run)
		}
		result.Repeats = append(result.Repeats, repeat)
	}
	return result, nil
}

func runScenarioOnce(parent context.Context, moduleRoot string, contract model.ScenarioContract, binary, work string, run int) (RepeatObservation, error) {
	for _, fixture := range contract.Fixtures {
		path := filepath.Join(work, filepath.FromSlash(fixture.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return RepeatObservation{}, err
		}
		if err := os.WriteFile(path, []byte(fixture.Content), 0o600); err != nil {
			return RepeatObservation{}, err
		}
	}
	result := RepeatObservation{Run: run}
	for _, step := range contract.Steps {
		before, err := workspaceHashes(work)
		if err != nil {
			return result, err
		}
		duration, _ := scenarios.Timeout(step.Timeout)
		ctx, cancel := context.WithTimeout(parent, duration)
		command := exec.CommandContext(ctx, binary, step.Args...)
		command.Dir = work
		command.Stdin = strings.NewReader(step.Stdin)
		stdout, stderr := &limitedBuffer{limit: scenarios.MaxStreamBytes}, &limitedBuffer{limit: scenarios.MaxStreamBytes}
		command.Stdout, command.Stderr = stdout, stderr
		err = command.Run()
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if timedOut {
			return result, fmt.Errorf("step %s exceeded %s", step.ID, duration)
		}
		if stdout.exceeded || stderr.exceeded {
			return result, fmt.Errorf("step %s output exceeds %d bytes", step.ID, scenarios.MaxStreamBytes)
		}
		observation := StepObservation{
			ID: step.ID, ExitCode: exitCode(err),
			Stdout: normalizeOutput(stdout.Bytes(), moduleRoot, work, binary),
			Stderr: normalizeOutput(stderr.Bytes(), moduleRoot, work, binary),
		}
		if observation.ExitCode != int(*step.Expect.ExitCode) || observation.Stdout != *step.Expect.Stdout || observation.Stderr != *step.Expect.Stderr {
			return result, fmt.Errorf("step %s observed exit/stdout/stderr (%d, %q, %q), want (%d, %q, %q)", step.ID, observation.ExitCode, observation.Stdout, observation.Stderr, *step.Expect.ExitCode, *step.Expect.Stdout, *step.Expect.Stderr)
		}
		declared := make(map[string]bool)
		for _, expectation := range step.Expect.Files {
			declared[expectation.Path] = true
			file, err := observeFile(work, expectation)
			if err != nil {
				return result, fmt.Errorf("step %s: %w", step.ID, err)
			}
			observation.Files = append(observation.Files, file)
		}
		after, err := workspaceHashes(work)
		if err != nil {
			return result, err
		}
		for _, changed := range changedPaths(before, after) {
			if !declared[changed] {
				return result, fmt.Errorf("step %s changed undeclared workspace file %s", step.ID, changed)
			}
		}
		result.Steps = append(result.Steps, observation)
	}
	return result, nil
}

func observeFile(work string, expectation model.ScenarioFileExpectation) (FileObservation, error) {
	path := filepath.Join(work, filepath.FromSlash(expectation.Path))
	info, statErr := os.Lstat(path)
	if statErr == nil && !info.Mode().IsRegular() {
		return FileObservation{}, fmt.Errorf("expected file %s is not a regular file", expectation.Path)
	}
	if statErr == nil {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return FileObservation{}, fmt.Errorf("resolve expected file %s: %w", expectation.Path, err)
		}
		relative, err := filepath.Rel(work, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return FileObservation{}, fmt.Errorf("expected file %s resolves outside the workspace", expectation.Path)
		}
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return FileObservation{}, fmt.Errorf("inspect expected file %s: %w", expectation.Path, statErr)
	}
	data, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return FileObservation{}, fmt.Errorf("read expected file %s: %w", expectation.Path, err)
	}
	if exists != *expectation.Exists {
		return FileObservation{}, fmt.Errorf("file %s existence is %t, want %t", expectation.Path, exists, *expectation.Exists)
	}
	result := FileObservation{Path: expectation.Path, Exists: exists}
	if !exists {
		return result, nil
	}
	digest := sha256.Sum256(data)
	result.SHA256 = "sha256:" + hex.EncodeToString(digest[:])
	if expectation.Text != nil && string(data) != *expectation.Text {
		return result, fmt.Errorf("file %s does not equal its exact text expectation", expectation.Path)
	}
	if expectation.JSON != nil && !equalJSON(data, []byte(*expectation.JSON)) {
		return result, fmt.Errorf("file %s does not equal its JSON expectation", expectation.Path)
	}
	return result, nil
}

func workspaceHashes(root string) (map[string]string, error) {
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("workspace entry %s is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		relative, _ := filepath.Rel(root, path)
		result[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	})
	return result, err
}

func changedPaths(before, after map[string]string) []string {
	set := make(map[string]bool)
	for path, digest := range before {
		if after[path] != digest {
			set[path] = true
		}
	}
	for path, digest := range after {
		if before[path] != digest {
			set[path] = true
		}
	}
	result := make([]string, 0, len(set))
	for path := range set {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func normalizeOutput(data []byte, moduleRoot, work, binary string) string {
	value := string(data)
	if work != "" {
		value = strings.ReplaceAll(value, work, "<WORK>")
	}
	if moduleRoot != "" {
		value = strings.ReplaceAll(value, moduleRoot, "<MODULE>")
	}
	if binary != "" {
		value = strings.ReplaceAll(value, binary, "<BINARY>")
	}
	return value
}

func equalJSON(left, right []byte) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func withEnvironment(key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

type limitedBuffer struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	exceeded bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := buffer.limit - len(buffer.data)
	if remaining > 0 {
		write := len(data)
		if write > remaining {
			write = remaining
		}
		buffer.data = append(buffer.data, data[:write]...)
	}
	if len(data) > remaining {
		buffer.exceeded = true
		return len(data), nil
	}
	return len(data), nil
}

func (buffer *limitedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.data...)
}

// QualificationEvidence returns deterministic exact evidence bytes for the
// existing derived-artifact CAS envelope.
func QualificationEvidence(generated GeneratedSet) ([]byte, error) {
	if generated.Qualification.Schema != qualificationSchema || generated.Qualification.OutputDigest == "" {
		return nil, fmt.Errorf("generated output has no completed qualification evidence")
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(generated.Qualification); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

var _ io.Writer = (*limitedBuffer)(nil)
