package app

import (
	"github.com/promisegrid/gridspec/internal/checker"
	"github.com/promisegrid/gridspec/internal/compile"
	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/rules"
	"github.com/promisegrid/gridspec/internal/source"
)

// LegacyModule is the migration-only result of compiling DOT and Markdown.
type LegacyModule struct {
	Config      config.Config
	ProfileName string
	Profile     config.Profile
	IR          model.IR
	Documents   map[string]source.Document
	Diagnostics []diag.Diagnostic
}

// LoadLegacyModule compiles and vets a Specdot v1 filesystem module without
// changing any GridSpec refs.
func LoadLegacyModule(manifest, profileName string, online bool) (LegacyModule, error) {
	configuration, err := config.Load(manifest)
	if err != nil {
		return LegacyModule{}, err
	}
	if profileName == "" {
		profileName = "default"
	}
	profile, err := configuration.SelectedProfile(profileName)
	if err != nil {
		return LegacyModule{}, err
	}
	files, err := source.Discover(configuration, nil)
	if err != nil {
		return LegacyModule{}, err
	}
	parsed := source.Parse(files)
	ir := compile.IR(configuration, profileName, parsed)
	semantic, bindings := rules.Run(rules.Context{
		IR: &ir, Config: configuration, Profile: profile,
		Documents: parsed.Documents, RawContracts: parsed.Contracts, Online: online,
	})
	ir.Bindings = bindings
	runs, checkDiagnostics := checker.RunAll(&ir, profile)
	ir.CheckResult = runs
	if err := ir.Seal(); err != nil {
		return LegacyModule{}, err
	}
	diagnostics := append(append(append([]diag.Diagnostic(nil), parsed.Diagnostics...), semantic...), checkDiagnostics...)
	diagnostics = applyProfile(profile, diagnostics)
	diag.Sort(diagnostics)
	return LegacyModule{
		Config: configuration, ProfileName: profileName, Profile: profile,
		IR: ir, Documents: parsed.Documents, Diagnostics: diagnostics,
	}, nil
}
