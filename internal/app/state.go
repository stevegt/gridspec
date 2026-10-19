package app

import (
	"fmt"
	"io"
	"os"

	"github.com/promisegrid/gridspec/internal/checker"
	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/report"
	"github.com/promisegrid/gridspec/internal/rules"
	"github.com/promisegrid/gridspec/internal/specstate"
)

type StateVetOptions struct {
	State   specstate.State
	Profile string
	Format  string
	Online  bool
	EmitIR  string
	Stdout  io.Writer
	Stderr  io.Writer
}

// AnalyzeState runs the same semantic and finite checks used by the legacy
// importer against an accepted transactional snapshot.
func AnalyzeState(state specstate.State, profileName string, online bool) (model.IR, []diag.Diagnostic, error) {
	if profileName == "" {
		profileName = state.Profile
	}
	configuration, profile, err := config.Builtin(state.Module, profileName)
	if err != nil {
		return model.IR{}, nil, err
	}
	ir, documents, rawContracts, err := state.SemanticView()
	if err != nil {
		return model.IR{}, nil, err
	}
	ir.Profile = profileName
	semantic, bindings := rules.Run(rules.Context{
		IR: &ir, Config: configuration, Profile: profile, Documents: documents,
		RawContracts: rawContracts, Online: online,
	})
	ir.Bindings = bindings
	runs, checkDiagnostics := checker.RunAll(&ir, profile)
	ir.CheckResult = runs
	if err := ir.Seal(); err != nil {
		return model.IR{}, nil, err
	}
	diagnostics := append(semantic, checkDiagnostics...)
	diagnostics = applyProfile(profile, diagnostics)
	diag.Sort(diagnostics)
	return ir, diagnostics, nil
}

func VetState(options StateVetOptions) int {
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	ir, diagnostics, err := AnalyzeState(options.State, options.Profile, options.Online)
	if err != nil {
		fmt.Fprintln(options.Stderr, "gridspec vet:", err)
		return 2
	}
	if options.EmitIR != "" {
		if err := emitIR(options.EmitIR, ir, options.Stdout); err != nil {
			fmt.Fprintln(options.Stderr, "gridspec vet: emit IR:", err)
			return 2
		}
	}
	reportWriter := options.Stdout
	if options.EmitIR == "-" {
		reportWriter = options.Stderr
	}
	if err := report.Write(reportWriter, options.Format, ir.Module, ir.Digest, diagnostics); err != nil {
		fmt.Fprintln(options.Stderr, "gridspec vet: report:", err)
		return 2
	}
	if diag.HasErrors(diagnostics) {
		return 1
	}
	return 0
}
