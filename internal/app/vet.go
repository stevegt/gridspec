// Package app orchestrates the public specdot commands.
package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/promisegrid/specdot/internal/checker"
	"github.com/promisegrid/specdot/internal/compile"
	"github.com/promisegrid/specdot/internal/config"
	"github.com/promisegrid/specdot/internal/diag"
	"github.com/promisegrid/specdot/internal/model"
	"github.com/promisegrid/specdot/internal/report"
	"github.com/promisegrid/specdot/internal/rules"
	"github.com/promisegrid/specdot/internal/source"
)

type VetOptions struct {
	ConfigPath string
	Profile    string
	Format     string
	Online     bool
	EmitIR     string
	Paths      []string
	Stdout     io.Writer
	Stderr     io.Writer
}

// Vet returns the documented process exit code: 0 clean, 1 findings, 2 usage
// or internal failure.
func Vet(opts VetOptions) int {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	manifest := opts.ConfigPath
	if manifest == "" {
		start := "."
		if len(opts.Paths) > 0 {
			start = opts.Paths[0]
		}
		var err error
		manifest, err = config.Discover(start)
		if err != nil {
			fmt.Fprintln(opts.Stderr, "specdot vet:", err)
			return 2
		}
	}
	cfg, err := config.Load(manifest)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot vet:", err)
		return 2
	}
	profileName := opts.Profile
	if profileName == "" {
		profileName = "default"
	}
	profile, err := cfg.SelectedProfile(profileName)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot vet:", err)
		return 2
	}
	files, err := source.Discover(cfg, opts.Paths)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot vet:", err)
		return 2
	}
	parsed := source.Parse(files)
	ir := compile.IR(cfg, profileName, parsed)
	semantic, bindings := rules.Run(rules.Context{
		IR: &ir, Config: cfg, Profile: profile, Documents: parsed.Documents,
		RawContracts: parsed.Contracts, Online: opts.Online,
	})
	ir.Bindings = bindings
	runs, checkDS := checker.RunAll(&ir, profile)
	ir.CheckResult = runs
	if err := ir.Seal(); err != nil {
		fmt.Fprintln(opts.Stderr, "specdot vet: seal IR:", err)
		return 2
	}
	diagnostics := append(append(append([]diag.Diagnostic(nil), parsed.Diagnostics...), semantic...), checkDS...)
	diagnostics = applyProfile(profile, diagnostics)
	diag.Sort(diagnostics)
	if opts.EmitIR != "" {
		if err := emitIR(opts.EmitIR, ir, opts.Stdout); err != nil {
			fmt.Fprintln(opts.Stderr, "specdot vet: emit IR:", err)
			return 2
		}
	}
	reportWriter := opts.Stdout
	if opts.EmitIR == "-" {
		reportWriter = opts.Stderr
	}
	if err := report.Write(reportWriter, opts.Format, cfg.Module, ir.Digest, diagnostics); err != nil {
		fmt.Fprintln(opts.Stderr, "specdot vet: report:", err)
		return 2
	}
	if diag.HasErrors(diagnostics) {
		return 1
	}
	return 0
}

func applyProfile(profile config.Profile, diagnostics []diag.Diagnostic) []diag.Diagnostic {
	result := diagnostics[:0]
	for _, d := range diagnostics {
		if severity, ok := profile.Rules[d.Rule]; ok {
			if d.Waivable || severity == "error" {
				switch severity {
				case "error":
					d.Severity = diag.SeverityError
				case "warning":
					d.Severity = diag.SeverityWarning
				case "note":
					d.Severity = diag.SeverityNote
				case "off":
					continue
				}
			}
		}
		if profile.WarningsAsError && d.Severity == diag.SeverityWarning {
			d.Severity = diag.SeverityError
		}
		result = append(result, d)
	}
	return result
}

func emitIR(target string, ir model.IR, stdout io.Writer) error {
	var writer io.Writer = stdout
	var file *os.File
	if target != "-" {
		abs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(abs), ".specdot-ir-*")
		if err != nil {
			return err
		}
		file, writer = tmp, tmp
		defer func() { _ = os.Remove(tmp.Name()) }()
		defer tmp.Close()
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(ir); err != nil {
		return err
	}
	if file != nil {
		if err := file.Sync(); err != nil {
			return err
		}
		name := file.Name()
		if err := file.Close(); err != nil {
			return err
		}
		return os.Rename(name, target)
	}
	return nil
}
