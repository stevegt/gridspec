package app

import (
	"fmt"
	"io"
	"os"

	impl "github.com/promisegrid/gridspec/internal/conform"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/specstate"
)

type StateConformOptions struct {
	State              specstate.State
	ImplementationPath string
	Profile            string
	Format             string
	EmitBaseline       string
	Stdout             io.Writer
	Stderr             io.Writer
}

// ConformState compares one verified transactional state with existing Go
// without regenerating that implementation. Authority selection is a caller
// policy; recorded evidence is restricted to accepted state by project.
func ConformState(options StateConformOptions) int {
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	if options.ImplementationPath == "" {
		options.ImplementationPath = impl.DefaultFilename
	}
	implementation, err := impl.Load(options.ImplementationPath)
	if err != nil {
		fmt.Fprintln(options.Stderr, "gridspec conform:", err)
		return 2
	}
	profile := options.Profile
	if profile == "" {
		profile = implementation.Profile
	}
	ir, vetDiagnostics, err := AnalyzeState(options.State, profile, false)
	if err != nil {
		fmt.Fprintln(options.Stderr, "gridspec conform:", err)
		return 2
	}
	if diag.HasErrors(vetDiagnostics) {
		emptyEvidence := map[string][]string{"static": {}, "command": {}, "go_test": {}, "trace": {}}
		emptyCoverage := make(map[string]impl.CoverageResult, 4)
		for _, class := range []string{"static", "command", "go_test", "trace"} {
			emptyCoverage[class] = impl.CoverageResult{Floor: implementation.Coverage.Floors[class]}
		}
		report := impl.Report{Schema: impl.ReportSchema, Module: ir.Module, Implementation: implementation.Name, SpecDigest: ir.Digest, Evidence: emptyEvidence, Coverage: emptyCoverage, Diagnostics: vetDiagnostics}
		if err := impl.WriteReport(options.Stdout, options.Format, report); err != nil {
			fmt.Fprintln(options.Stderr, "gridspec conform: report:", err)
			return 2
		}
		return 1
	}
	result := impl.Run(implementation, ir, impl.RunOptions{IgnoreBaseline: options.EmitBaseline != ""})
	result.Diagnostics = append(result.Diagnostics, vetDiagnostics...)
	diag.Sort(result.Diagnostics)
	if options.EmitBaseline != "" && !diag.HasErrors(result.Diagnostics) {
		if err := impl.WriteBaseline(options.EmitBaseline, result.Baseline()); err != nil {
			fmt.Fprintln(options.Stderr, "gridspec conform: emit baseline:", err)
			return 2
		}
	}
	if err := impl.WriteReport(options.Stdout, options.Format, result); err != nil {
		fmt.Fprintln(options.Stderr, "gridspec conform: report:", err)
		return 2
	}
	if diag.HasErrors(result.Diagnostics) {
		return 1
	}
	return 0
}
