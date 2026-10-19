// Package app orchestrates GridSpec validation and legacy migration.
package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/promisegrid/gridspec/conformance/trace"
	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/report"
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
	Trace      trace.Sink
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
	closeTrace := func() error { return nil }
	if opts.Trace == nil {
		var err error
		opts.Trace, closeTrace, err = trace.OpenFromEnv()
		if err != nil {
			fmt.Fprintln(opts.Stderr, "specdot vet: trace:", err)
			return 2
		}
	}
	defer func() { _ = closeTrace() }()
	manifest := opts.ConfigPath
	recordTrace(opts.Trace, "vet.resolve.manifest", "enter", "")
	if manifest == "" {
		start := "."
		if len(opts.Paths) > 0 {
			start = opts.Paths[0]
		}
		var err error
		manifest, err = config.Discover(start)
		if err != nil {
			recordTrace(opts.Trace, "vet.resolve.manifest", "exit", "failure")
			fmt.Fprintln(opts.Stderr, "specdot vet:", err)
			return 2
		}
	}
	recordTrace(opts.Trace, "vet.resolve.manifest", "exit", "success")
	recordTrace(opts.Trace, "vet.load.config", "enter", "")
	cfg, err := config.Load(manifest)
	if err != nil {
		recordTrace(opts.Trace, "vet.load.config", "exit", "failure")
		fmt.Fprintln(opts.Stderr, "specdot vet:", err)
		return 2
	}
	recordTrace(opts.Trace, "vet.load.config", "exit", "success")
	profileName := opts.Profile
	if profileName == "" {
		profileName = "default"
	}
	recordTrace(opts.Trace, "vet.select.profile", "enter", "")
	profile, err := cfg.SelectedProfile(profileName)
	if err != nil {
		recordTrace(opts.Trace, "vet.select.profile", "exit", "failure")
		fmt.Fprintln(opts.Stderr, "specdot vet:", err)
		return 2
	}
	recordTrace(opts.Trace, "vet.select.profile", "exit", "success")
	ir, diagnostics, err := analyzeModule(cfg, profileName, profile, opts.Paths, opts.Online, opts.Trace)
	if err != nil {
		fmt.Fprintln(opts.Stderr, "specdot vet: analyze:", err)
		return 2
	}
	if opts.EmitIR != "" {
		recordTrace(opts.Trace, "vet.emit.ir", "enter", "")
		if err := emitIR(opts.EmitIR, ir, opts.Stdout); err != nil {
			recordTrace(opts.Trace, "vet.emit.ir", "exit", "failure")
			fmt.Fprintln(opts.Stderr, "specdot vet: emit IR:", err)
			return 2
		}
		recordTrace(opts.Trace, "vet.emit.ir", "exit", "success")
	}
	reportWriter := opts.Stdout
	if opts.EmitIR == "-" {
		reportWriter = opts.Stderr
	}
	recordTrace(opts.Trace, "vet.write.report", "enter", "")
	if err := report.Write(reportWriter, opts.Format, cfg.Module, ir.Digest, diagnostics); err != nil {
		recordTrace(opts.Trace, "vet.write.report", "exit", "failure")
		fmt.Fprintln(opts.Stderr, "specdot vet: report:", err)
		return 2
	}
	recordTrace(opts.Trace, "vet.write.report", "exit", "success")
	if diag.HasErrors(diagnostics) {
		recordTrace(opts.Trace, "vet.has.errors", "observe", "errors")
		return 1
	}
	recordTrace(opts.Trace, "vet.has.errors", "observe", "clean")
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
		tmp, err := os.CreateTemp(filepath.Dir(abs), ".gridspec-ir-*")
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
