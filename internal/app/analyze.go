package app

import (
	"github.com/promisegrid/gridspec/conformance/trace"
	"github.com/promisegrid/gridspec/internal/checker"
	"github.com/promisegrid/gridspec/internal/compile"
	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/rules"
	"github.com/promisegrid/gridspec/internal/source"
)

func analyzeModule(cfg config.Config, profileName string, profile config.Profile, paths []string, online bool, sink trace.Sink) (model.IR, []diag.Diagnostic, error) {
	recordTrace(sink, "vet.discover.sources", "enter", "")
	files, err := source.Discover(cfg, paths)
	if err != nil {
		recordTrace(sink, "vet.discover.sources", "exit", "failure")
		return model.IR{}, nil, err
	}
	recordTrace(sink, "vet.discover.sources", "exit", "success")
	recordTrace(sink, "vet.parse.sources", "enter", "")
	parsed := source.Parse(files)
	recordTrace(sink, "vet.parse.sources", "exit", "success")
	recordTrace(sink, "vet.compile.ir", "enter", "")
	ir := compile.IR(cfg, profileName, parsed)
	recordTrace(sink, "vet.compile.ir", "exit", "success")
	recordTrace(sink, "vet.run.rules", "enter", "")
	semantic, bindings := rules.Run(rules.Context{
		IR: &ir, Config: cfg, Profile: profile, Documents: parsed.Documents,
		RawContracts: parsed.Contracts, Online: online,
	})
	ir.Bindings = bindings
	recordTrace(sink, "vet.run.rules", "exit", "success")
	recordTrace(sink, "vet.run.checker", "enter", "")
	runs, checkDS := checker.RunAll(&ir, profile)
	ir.CheckResult = runs
	recordTrace(sink, "vet.run.checker", "exit", "success")
	recordTrace(sink, "vet.seal.ir", "enter", "")
	if err := ir.Seal(); err != nil {
		recordTrace(sink, "vet.seal.ir", "exit", "failure")
		return model.IR{}, nil, err
	}
	recordTrace(sink, "vet.seal.ir", "exit", "success")
	diagnostics := append(append(append([]diag.Diagnostic(nil), parsed.Diagnostics...), semantic...), checkDS...)
	diagnostics = applyProfile(profile, diagnostics)
	diag.Sort(diagnostics)
	return ir, diagnostics, nil
}

func recordTrace(sink trace.Sink, specID, phase, outcome string) {
	if sink != nil {
		_ = sink.Emit(specID, phase, outcome, nil)
	}
}
