package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/promisegrid/specdot/internal/config"
	impl "github.com/promisegrid/specdot/internal/conform"
	"github.com/promisegrid/specdot/internal/diag"
)

type ConformOptions struct {
	ConfigPath         string
	ImplementationPath string
	Profile            string
	Format             string
	EmitBaseline       string
	Stdout             io.Writer
	Stderr             io.Writer
}

func Conform(opts ConformOptions) int {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	var implementation impl.Config
	var err error
	if opts.ImplementationPath != "" {
		implementation, err = impl.Load(opts.ImplementationPath)
		if err != nil {
			fmt.Fprintln(opts.Stderr, "specdot conform:", err)
			return 2
		}
		if opts.ConfigPath == "" && implementation.SpecConfig != "" {
			opts.ConfigPath, _ = implementation.Resolve(implementation.SpecConfig)
		}
	}
	manifest := opts.ConfigPath
	if manifest == "" {
		manifest, err = config.Discover(".")
		if err != nil {
			fmt.Fprintln(opts.Stderr, "specdot conform:", err)
			return 2
		}
	}
	cfg, err := config.Load(manifest)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot conform:", err)
		return 2
	}
	if opts.ImplementationPath == "" {
		path := filepath.Join(cfg.Dir, impl.DefaultFilename)
		implementation, err = impl.Load(path)
		if err != nil {
			fmt.Fprintln(opts.Stderr, "specdot conform:", err)
			return 2
		}
	}
	if implementation.SpecConfig != "" {
		declared, _ := implementation.Resolve(implementation.SpecConfig)
		if filepath.Clean(declared) != filepath.Clean(cfg.Path) {
			fmt.Fprintf(opts.Stderr, "specdot conform: implementation sidecar selects %s, not %s\n", declared, cfg.Path)
			return 2
		}
	}
	profileName := opts.Profile
	if profileName == "" {
		profileName = implementation.Profile
	}
	profile, err := cfg.SelectedProfile(profileName)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot conform:", err)
		return 2
	}
	ir, vetDiagnostics, err := analyzeModule(cfg, profileName, profile, nil, false, nil)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot conform: analyze:", err)
		return 2
	}
	if diag.HasErrors(vetDiagnostics) {
		emptyEvidence := map[string][]string{"static": {}, "command": {}, "go_test": {}, "trace": {}}
		emptyCoverage := make(map[string]impl.CoverageResult, 4)
		for _, class := range []string{"static", "command", "go_test", "trace"} {
			emptyCoverage[class] = impl.CoverageResult{Floor: implementation.Coverage.Floors[class]}
		}
		report := impl.Report{
			Schema: impl.ReportSchema, Module: ir.Module, Implementation: implementation.Name,
			SpecDigest: ir.Digest, Evidence: emptyEvidence,
			Coverage: emptyCoverage, Diagnostics: vetDiagnostics,
		}
		if err := impl.WriteReport(opts.Stdout, opts.Format, report); err != nil {
			fmt.Fprintln(opts.Stderr, "specdot conform: report:", err)
			return 2
		}
		return 1
	}
	result := impl.Run(implementation, ir, impl.RunOptions{IgnoreBaseline: opts.EmitBaseline != ""})
	result.Diagnostics = append(result.Diagnostics, vetDiagnostics...)
	diag.Sort(result.Diagnostics)
	if opts.EmitBaseline != "" && !diag.HasErrors(result.Diagnostics) {
		if err := impl.WriteBaseline(opts.EmitBaseline, result.Baseline()); err != nil {
			fmt.Fprintln(opts.Stderr, "specdot conform: emit baseline:", err)
			return 2
		}
	}
	if err := impl.WriteReport(opts.Stdout, opts.Format, result); err != nil {
		fmt.Fprintln(opts.Stderr, "specdot conform: report:", err)
		return 2
	}
	if diag.HasErrors(result.Diagnostics) {
		return 1
	}
	return 0
}
