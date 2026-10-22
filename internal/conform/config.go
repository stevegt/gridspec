// Package conform compares a vetted specification IR with an existing Go
// implementation. The implementation sidecar is deliberately separate from
// the language-neutral transactional state.
package conform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

const (
	ImplementationSchema = "gridspec.implementation/v1"
	ReportSchema         = "gridspec.conformance/v1"
	BaselineSchema       = "gridspec.conformance-baseline/v1"
	DefaultFilename      = "gridspec.impl.yaml"
)

type Config struct {
	Schema     string         `yaml:"schema" json:"schema"`
	Name       string         `yaml:"name" json:"name"`
	SpecConfig string         `yaml:"spec_config,omitempty" json:"spec_config,omitempty"`
	Profile    string         `yaml:"profile,omitempty" json:"profile,omitempty"`
	Go         *GoConfig      `yaml:"go" json:"go"`
	Bindings   []Binding      `yaml:"bindings,omitempty" json:"bindings,omitempty"`
	Scenarios  []Scenario     `yaml:"scenarios,omitempty" json:"scenarios,omitempty"`
	Exclusions []Exclusion    `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
	Coverage   CoveragePolicy `yaml:"coverage" json:"coverage"`
	Path       string         `yaml:"-" json:"-"`
	Dir        string         `yaml:"-" json:"-"`
	ModuleRoot string         `yaml:"-" json:"-"`
}

type GoConfig struct {
	ModuleRoot string     `yaml:"module_root,omitempty" json:"module_root,omitempty"`
	Artifacts  []Artifact `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
}

type Artifact struct {
	Name    string `yaml:"name" json:"name"`
	Package string `yaml:"package" json:"package"`
}

// Binding names a stable Go object without pinning it to a source line.
type Binding struct {
	SpecID   string `yaml:"spec_id" json:"spec_id"`
	Package  string `yaml:"package" json:"package"`
	Kind     string `yaml:"kind" json:"kind"`
	Receiver string `yaml:"receiver,omitempty" json:"receiver,omitempty"`
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
}

type Scenario struct {
	SpecID  string           `yaml:"spec_id" json:"spec_id"`
	Covers  []string         `yaml:"covers,omitempty" json:"covers,omitempty"`
	Command *CommandRunner   `yaml:"command,omitempty" json:"command,omitempty"`
	GoTest  *GoTestRunner    `yaml:"go_test,omitempty" json:"go_test,omitempty"`
	Expect  ScenarioExpect   `yaml:"expect,omitempty" json:"expect,omitempty"`
	Trace   TraceExpectation `yaml:"trace,omitempty" json:"trace,omitempty"`
}

// CommandRunner is the legacy sidecar command format. Accepted executable
// scenario contracts are authoritative whenever they exist for the same ID.
type CommandRunner struct {
	Artifact string            `yaml:"artifact" json:"artifact"`
	Args     []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Fixture  string            `yaml:"fixture,omitempty" json:"fixture,omitempty"`
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Timeout  string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Repeat   int               `yaml:"repeat,omitempty" json:"repeat,omitempty"`
}

type GoTestRunner struct {
	Package string `yaml:"package" json:"package"`
	Test    string `yaml:"test" json:"test"`
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type ScenarioExpect struct {
	ExitCode *int              `yaml:"exit_code,omitempty" json:"exit_code,omitempty"`
	Stdout   StreamExpectation `yaml:"stdout,omitempty" json:"stdout,omitempty"`
	Stderr   StreamExpectation `yaml:"stderr,omitempty" json:"stderr,omitempty"`
	Files    []FileExpectation `yaml:"files,omitempty" json:"files,omitempty"`
}

type StreamExpectation struct {
	Empty    bool     `yaml:"empty,omitempty" json:"empty,omitempty"`
	Equals   *string  `yaml:"equals,omitempty" json:"equals,omitempty"`
	Contains []string `yaml:"contains,omitempty" json:"contains,omitempty"`
	File     string   `yaml:"file,omitempty" json:"file,omitempty"`
	JSONFile string   `yaml:"json_file,omitempty" json:"json_file,omitempty"`
}

type FileExpectation struct {
	Path       string `yaml:"path" json:"path"`
	Exists     *bool  `yaml:"exists,omitempty" json:"exists,omitempty"`
	EqualsFile string `yaml:"equals_file,omitempty" json:"equals_file,omitempty"`
	JSONFile   string `yaml:"json_file,omitempty" json:"json_file,omitempty"`
}

type TraceExpectation struct {
	Ordered []TraceMatch `yaml:"ordered,omitempty" json:"ordered,omitempty"`
}

type TraceMatch struct {
	SpecID  string `yaml:"spec_id" json:"spec_id"`
	Phase   string `yaml:"phase,omitempty" json:"phase,omitempty"`
	Outcome string `yaml:"outcome,omitempty" json:"outcome,omitempty"`
}

type Exclusion struct {
	SpecIDs []string `yaml:"spec_ids" json:"spec_ids"`
	Reason  string   `yaml:"reason" json:"reason"`
}

type CoveragePolicy struct {
	Baseline      string             `yaml:"baseline,omitempty" json:"baseline,omitempty"`
	Floors        map[string]float64 `yaml:"floors,omitempty" json:"floors,omitempty"`
	MaxExclusions *int               `yaml:"max_exclusions,omitempty" json:"max_exclusions,omitempty"`
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("implementation sidecar must contain exactly one YAML document")
		}
		return Config{}, fmt.Errorf("decode trailing YAML: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	cfg.Path, cfg.Dir = abs, filepath.Dir(abs)
	if cfg.Profile == "" {
		cfg.Profile = "build"
	}
	if cfg.Go == nil {
		return Config{}, errors.New("implementation go configuration is required")
	}
	root := cfg.Go.ModuleRoot
	if root == "" {
		root = "."
	}
	cfg.ModuleRoot, err = cfg.resolveContained(root)
	if err != nil {
		return Config{}, fmt.Errorf("go.module_root: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Resolve(path string) (string, error) {
	return c.resolveContained(path)
}

func (c Config) resolveContained(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(c.Dir, resolved)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(c.Dir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the implementation sidecar directory", path)
	}
	return abs, nil
}

func (c Config) validate() error {
	if c.Schema != ImplementationSchema {
		return fmt.Errorf("unsupported implementation schema %q (want %s)", c.Schema, ImplementationSchema)
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("implementation name is required")
	}
	artifacts := make(map[string]bool)
	foldedArtifacts := make(map[string]bool)
	for i, artifact := range c.Go.Artifacts {
		if artifact.Name == "" || artifact.Package == "" {
			return fmt.Errorf("go.artifacts[%d] requires name and package", i)
		}
		if strings.TrimSpace(artifact.Package) != artifact.Package || strings.HasPrefix(artifact.Package, "-") {
			return fmt.Errorf("go artifact %q has invalid package %q", artifact.Name, artifact.Package)
		}
		if safeName(artifact.Name) != artifact.Name {
			return fmt.Errorf("go artifact name %q may contain only letters, digits, hyphens, and underscores", artifact.Name)
		}
		foldedName := strings.ToLower(artifact.Name)
		if foldedArtifacts[foldedName] {
			return fmt.Errorf("go artifact %q is duplicated", artifact.Name)
		}
		foldedArtifacts[foldedName] = true
		artifacts[artifact.Name] = true
	}
	bindings := make(map[string]bool)
	for i, binding := range c.Bindings {
		if binding.SpecID == "" || binding.Package == "" || binding.Kind == "" {
			return fmt.Errorf("bindings[%d] requires spec_id, package, and kind", i)
		}
		if bindings[binding.SpecID] {
			return fmt.Errorf("binding for %q is duplicated", binding.SpecID)
		}
		bindings[binding.SpecID] = true
		switch binding.Kind {
		case "package":
			if binding.Name != "" || binding.Receiver != "" {
				return fmt.Errorf("package binding %q cannot name an object or receiver", binding.SpecID)
			}
		case "func", "type", "const", "var":
			if binding.Name == "" || binding.Receiver != "" {
				return fmt.Errorf("%s binding %q requires name and no receiver", binding.Kind, binding.SpecID)
			}
		case "method", "field":
			if binding.Name == "" || binding.Receiver == "" {
				return fmt.Errorf("%s binding %q requires receiver and name", binding.Kind, binding.SpecID)
			}
		default:
			return fmt.Errorf("binding %q has unsupported Go kind %q", binding.SpecID, binding.Kind)
		}
	}
	scenarios := make(map[string]bool)
	for i, scenario := range c.Scenarios {
		if scenario.SpecID == "" {
			return fmt.Errorf("scenarios[%d].spec_id is required", i)
		}
		if scenarios[scenario.SpecID] {
			return fmt.Errorf("scenario %q is duplicated", scenario.SpecID)
		}
		scenarios[scenario.SpecID] = true
		if (scenario.Command == nil) == (scenario.GoTest == nil) {
			return fmt.Errorf("scenario %q must declare exactly one of command or go_test", scenario.SpecID)
		}
		if scenario.Command != nil {
			if !artifacts[scenario.Command.Artifact] {
				return fmt.Errorf("scenario %q references unknown artifact %q", scenario.SpecID, scenario.Command.Artifact)
			}
			if scenario.Expect.ExitCode == nil {
				return fmt.Errorf("command scenario %q requires expect.exit_code", scenario.SpecID)
			}
			if scenario.Command.Fixture == "" {
				return fmt.Errorf("command scenario %q requires an isolated fixture directory", scenario.SpecID)
			}
			if scenario.Command.Repeat < 0 {
				return fmt.Errorf("scenario %q repeat must be positive", scenario.SpecID)
			}
			if err := validTimeout(scenario.Command.Timeout); err != nil {
				return fmt.Errorf("scenario %q timeout: %w", scenario.SpecID, err)
			}
			if _, err := c.resolveContained(scenario.Command.Fixture); err != nil {
				return fmt.Errorf("scenario %q fixture: %w", scenario.SpecID, err)
			}
			for label, stream := range map[string]StreamExpectation{"stdout": scenario.Expect.Stdout, "stderr": scenario.Expect.Stderr} {
				if err := c.validateStream(stream); err != nil {
					return fmt.Errorf("scenario %q expect.%s: %w", scenario.SpecID, label, err)
				}
			}
			for j, file := range scenario.Expect.Files {
				if file.Path == "" {
					return fmt.Errorf("scenario %q expect.files[%d].path is required", scenario.SpecID, j)
				}
				if _, err := containedJoin(c.Dir, file.Path); err != nil {
					return fmt.Errorf("scenario %q expect.files[%d]: %w", scenario.SpecID, j, err)
				}
				for _, reference := range []string{file.EqualsFile, file.JSONFile} {
					if reference != "" {
						if _, err := c.resolveContained(reference); err != nil {
							return fmt.Errorf("scenario %q expect.files[%d]: %w", scenario.SpecID, j, err)
						}
					}
				}
				if file.EqualsFile != "" && file.JSONFile != "" {
					return fmt.Errorf("scenario %q expect.files[%d] cannot use both equals_file and json_file", scenario.SpecID, j)
				}
				if file.Exists != nil && !*file.Exists && (file.EqualsFile != "" || file.JSONFile != "") {
					return fmt.Errorf("scenario %q expect.files[%d] cannot compare content when exists is false", scenario.SpecID, j)
				}
			}
		} else {
			if scenario.GoTest.Package == "" || scenario.GoTest.Test == "" {
				return fmt.Errorf("go_test scenario %q requires package and test", scenario.SpecID)
			}
			if err := validTimeout(scenario.GoTest.Timeout); err != nil {
				return fmt.Errorf("scenario %q timeout: %w", scenario.SpecID, err)
			}
		}
	}
	for i, exclusion := range c.Exclusions {
		if len(exclusion.SpecIDs) == 0 || strings.TrimSpace(exclusion.Reason) == "" {
			return fmt.Errorf("exclusions[%d] requires spec_ids and a non-empty reason", i)
		}
	}
	if c.Coverage.Baseline == "" {
		return errors.New("coverage.baseline is required")
	}
	for _, class := range []string{"static", "command", "go_test", "trace"} {
		if _, ok := c.Coverage.Floors[class]; !ok {
			return fmt.Errorf("coverage.floors.%s is required", class)
		}
	}
	for class, floor := range c.Coverage.Floors {
		switch class {
		case "static", "command", "go_test", "trace":
		default:
			return fmt.Errorf("coverage floor has unknown evidence class %q", class)
		}
		if floor < 0 || floor > 100 {
			return fmt.Errorf("coverage floor %q must be between 0 and 100", class)
		}
	}
	if c.Coverage.MaxExclusions == nil {
		return errors.New("coverage.max_exclusions is required")
	}
	if *c.Coverage.MaxExclusions < 0 {
		return errors.New("coverage.max_exclusions cannot be negative")
	}
	if _, err := c.resolveContained(c.Coverage.Baseline); err != nil {
		return fmt.Errorf("coverage.baseline: %w", err)
	}
	if c.SpecConfig != "" {
		if _, err := c.resolveContained(c.SpecConfig); err != nil {
			return fmt.Errorf("spec_config: %w", err)
		}
	}
	return nil
}

func (c Config) validateStream(stream StreamExpectation) error {
	exact := 0
	if stream.Equals != nil {
		exact++
	}
	if stream.File != "" {
		exact++
		if _, err := c.resolveContained(stream.File); err != nil {
			return err
		}
	}
	if stream.JSONFile != "" {
		exact++
		if _, err := c.resolveContained(stream.JSONFile); err != nil {
			return err
		}
	}
	if exact > 1 {
		return errors.New("choose at most one of equals, file, or json_file")
	}
	if stream.Empty && (exact != 0 || len(stream.Contains) != 0) {
		return errors.New("empty cannot be combined with content expectations")
	}
	return nil
}

func validTimeout(value string) error {
	if value == "" {
		return nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	if d <= 0 {
		return errors.New("must be positive")
	}
	return nil
}

func runnerTimeout(value string) time.Duration {
	if value == "" {
		return 30 * time.Second
	}
	d, _ := time.ParseDuration(value)
	return d
}
