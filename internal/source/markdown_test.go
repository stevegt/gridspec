package source

import (
	"testing"

	"github.com/promisegrid/specdot/internal/diag"
)

func TestContractRejectsDuplicateYAMLKeys(t *testing.T) {
	data := []byte("```specdot\nschema: specdot.contract/v1\nfor: node.one\nfor: node.two\n```\n")
	_, diagnostics := parseMarkdown(File{Rel: "test.md", Kind: "md", Data: data})
	if !hasRule(diagnostics, "SDV052") {
		t.Fatalf("expected duplicate-key diagnostic, got %#v", diagnostics)
	}
}

func TestContractRejectsMultipleYAMLDocuments(t *testing.T) {
	data := []byte("```specdot\nschema: specdot.contract/v1\nfor: node.one\n---\nschema: specdot.contract/v1\nfor: node.two\n```\n")
	_, diagnostics := parseMarkdown(File{Rel: "test.md", Kind: "md", Data: data})
	if !hasRule(diagnostics, "SDV053") {
		t.Fatalf("expected multi-document diagnostic, got %#v", diagnostics)
	}
}

func hasRule(diagnostics []diag.Diagnostic, rule string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Rule == rule {
			return true
		}
	}
	return false
}
