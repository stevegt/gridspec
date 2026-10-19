package migrate

import (
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/model"
	"github.com/promisegrid/gridspec/internal/source"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

func TestLegacyProgramReusesDocumentIDAndEmbedsContent(t *testing.T) {
	base := specstate.Empty("example.test", "build")
	base.Entities[specstate.Key("document", "readable.id")] = specstate.Entity{
		Kind: "document", ID: "readable.id", Fields: map[string]any{"path": "docs/design.md"},
	}
	content := "# Design\n\nExact source.\n"
	document := source.IndexMarkdown("docs/design.md", []byte(content))
	program, err := LegacyProgram("example.test", "build", model.IR{}, map[string]source.Document{
		"docs/design.md": document,
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(program), "put document readable.id") {
		t.Fatalf("program did not reuse accepted document ID:\n%s", program)
	}
	parsed, err := txnlang.Parse(program)
	if err != nil {
		t.Fatal(err)
	}
	state, err := specstate.Apply(base, parsed)
	if err != nil {
		t.Fatal(err)
	}
	_, documents, _, err := state.SemanticView()
	if err != nil {
		t.Fatal(err)
	}
	if documents["docs/design.md"].Content != content {
		t.Fatal("migration omitted exact Markdown content")
	}
}
