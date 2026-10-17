// Package config loads the specdot module manifest.
package config

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

const Filename = "specdot.yaml"

// Config describes one independently vetted spec module.
type Config struct {
	Schema     string             `yaml:"schema" json:"schema"`
	Module     string             `yaml:"module" json:"module"`
	Roots      []string           `yaml:"roots" json:"roots"`
	Include    []string           `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude    []string           `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Profiles   map[string]Profile `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	LinkPolicy LinkPolicy         `yaml:"link_policy,omitempty" json:"link_policy,omitempty"`
	Build      Build              `yaml:"build,omitempty" json:"build,omitempty"`
	Path       string             `yaml:"-" json:"path"`
	Dir        string             `yaml:"-" json:"dir"`
}

type LinkPolicy struct {
	RequireBacklinks bool          `yaml:"require_backlinks,omitempty" json:"require_backlinks,omitempty"`
	AllowRemote      bool          `yaml:"allow_remote,omitempty" json:"allow_remote,omitempty"`
	RemoteTimeout    time.Duration `yaml:"-" json:"remote_timeout,omitempty"`
	RemoteTimeoutRaw string        `yaml:"remote_timeout,omitempty" json:"-"`
}

type Build struct {
	Targets []BuildTarget `yaml:"targets,omitempty" json:"targets,omitempty"`
}

type BuildTarget struct {
	Name    string   `yaml:"name" json:"name"`
	Profile string   `yaml:"profile,omitempty" json:"profile,omitempty"`
	Roots   []string `yaml:"roots,omitempty" json:"roots,omitempty"`
}

// Profile controls severity and finite verification limits.
type Profile struct {
	BuildReady      bool              `yaml:"build_ready,omitempty" json:"build_ready,omitempty"`
	WarningsAsError bool              `yaml:"warnings_as_errors,omitempty" json:"warnings_as_errors,omitempty"`
	Rules           map[string]string `yaml:"rules,omitempty" json:"rules,omitempty"`
	Finite          FiniteProfile     `yaml:"finite,omitempty" json:"finite,omitempty"`
}

type FiniteProfile struct {
	Enabled        bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Required       bool `yaml:"required,omitempty" json:"required,omitempty"`
	MaxStates      int  `yaml:"max_states,omitempty" json:"max_states,omitempty"`
	MaxDepth       int  `yaml:"max_depth,omitempty" json:"max_depth,omitempty"`
	MaxQueue       int  `yaml:"max_queue,omitempty" json:"max_queue,omitempty"`
	MaxTransitions int  `yaml:"max_transitions,omitempty" json:"max_transitions,omitempty"`
}

func defaults() Config {
	return Config{
		Schema:  "specdot/v1",
		Roots:   []string{"."},
		Include: []string{"**/*.dot", "**/*.md"},
		Exclude: []string{".git/**", "vendor/**"},
		Profiles: map[string]Profile{
			"default": {
				Finite: FiniteProfile{Enabled: true, MaxStates: 10000, MaxDepth: 200, MaxQueue: 8, MaxTransitions: 100000},
			},
			"build": {
				BuildReady: true,
				Finite:     FiniteProfile{Enabled: true, Required: true, MaxStates: 100000, MaxDepth: 1000, MaxQueue: 16, MaxTransitions: 1000000},
			},
		},
		LinkPolicy: LinkPolicy{RemoteTimeout: 5 * time.Second},
	}
}

// Discover walks upward from start and returns the first module manifest.
func Discover(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if st, statErr := os.Stat(abs); statErr == nil && !st.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		candidate := filepath.Join(abs, Filename)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("%s not found above %s", Filename, start)
		}
		abs = parent
	}
}

// Load reads a manifest and rejects unknown fields.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()

	cfg := defaults()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("manifest must contain exactly one YAML document")
		}
		return Config{}, fmt.Errorf("decode trailing YAML: %w", err)
	}
	if cfg.Schema != "specdot/v1" {
		return Config{}, fmt.Errorf("unsupported schema %q (want specdot/v1)", cfg.Schema)
	}
	if cfg.Module == "" {
		return Config{}, errors.New("manifest module is required")
	}
	if len(cfg.Roots) == 0 {
		return Config{}, errors.New("manifest roots must not be empty")
	}
	if cfg.LinkPolicy.RemoteTimeoutRaw != "" {
		d, err := time.ParseDuration(cfg.LinkPolicy.RemoteTimeoutRaw)
		if err != nil {
			return Config{}, fmt.Errorf("link_policy.remote_timeout: %w", err)
		}
		cfg.LinkPolicy.RemoteTimeout = d
	}
	if cfg.LinkPolicy.RemoteTimeout <= 0 {
		return Config{}, errors.New("link_policy.remote_timeout must be positive")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	cfg.Path = abs
	cfg.Dir = filepath.Dir(abs)
	for _, root := range cfg.Roots {
		resolved := root
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(cfg.Dir, resolved)
		}
		rel, err := filepath.Rel(cfg.Dir, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return Config{}, fmt.Errorf("root %q escapes module directory", root)
		}
	}
	return cfg, nil
}

// SelectedProfile resolves a named profile and applies finite defaults.
func (c Config) SelectedProfile(name string) (Profile, error) {
	if name == "" {
		name = "default"
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("unknown profile %q", name)
	}
	d := defaults().Profiles["default"].Finite
	if p.Finite.MaxStates == 0 {
		p.Finite.MaxStates = d.MaxStates
	}
	if p.Finite.MaxDepth == 0 {
		p.Finite.MaxDepth = d.MaxDepth
	}
	if p.Finite.MaxQueue == 0 {
		p.Finite.MaxQueue = d.MaxQueue
	}
	if p.Finite.MaxTransitions == 0 {
		p.Finite.MaxTransitions = d.MaxTransitions
	}
	if p.Finite.Enabled && (p.Finite.MaxStates < 1 || p.Finite.MaxDepth < 1 || p.Finite.MaxQueue < 1 || p.Finite.MaxTransitions < 1) {
		return Profile{}, fmt.Errorf("profile %q finite bounds must all be positive", name)
	}
	for rule, severity := range p.Rules {
		if severity != "error" && severity != "warning" && severity != "note" && severity != "off" {
			return Profile{}, fmt.Errorf("profile %q rule %s has invalid severity %q", name, rule, severity)
		}
	}
	return p, nil
}
