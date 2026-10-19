package rules

import (
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/model"
)

func TestDescriptionDiagnosticsEnforceTextLimits(t *testing.T) {
	location := model.Location{File: "gridspec/test"}
	for name, test := range map[string]struct {
		title, summary string
		want           string
	}{
		"trimmed":        {title: " title", want: "SDV121"},
		"single line":    {summary: "one\ntwo", want: "SDV122"},
		"title length":   {title: strings.Repeat("t", model.TitleMaxRunes+1), want: "SDV121"},
		"summary length": {summary: strings.Repeat("s", model.SummaryMaxRunes+1), want: "SDV122"},
	} {
		t.Run(name, func(t *testing.T) {
			diagnostics := descriptionDiagnostics(test.title, test.summary, location, "item")
			if len(diagnostics) != 1 || diagnostics[0].Rule != test.want {
				t.Fatalf("diagnostics = %#v, want %s", diagnostics, test.want)
			}
		})
	}
}

func TestDescriptionDiagnosticsAcceptRuneLimits(t *testing.T) {
	diagnostics := descriptionDiagnostics(
		strings.Repeat("é", model.TitleMaxRunes),
		strings.Repeat("界", model.SummaryMaxRunes),
		model.Location{File: "gridspec/test"},
		"item",
	)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestBuildReadyRequiresTitlesOnActiveDeclarations(t *testing.T) {
	ir := model.IR{Graphs: []model.Graph{{
		ID: "graph.test", Kind: "algorithm", Status: "active",
		Nodes: []model.Element{
			{ID: "node.active", Element: "node", Status: "active"},
			{ID: "node.draft", Element: "node", Status: "draft"},
		},
		Edges: []model.Element{{ID: "edge.titled", Element: "edge", Status: "active", Title: "Continue"}},
	}}}
	diagnostics := checkBuildReadiness(Context{IR: &ir, Profile: config.Profile{BuildReady: true}}, Index{Contracts: map[string]model.Contract{}})
	missing := map[string]bool{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Rule == "SDV602" {
			missing[diagnostic.ElementID] = true
		}
	}
	if !missing["graph.test"] || !missing["node.active"] || len(missing) != 2 {
		t.Fatalf("missing-title diagnostics = %#v", missing)
	}
}
