package specstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

func TestBuildLoadAndReplayAreDeterministic(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	program, err := txnlang.Parse([]byte(`gridspec 1
module "example.test"
put graph algorithm.main { kind = "algorithm"; status = "active"; directed = true }
`))
	if err != nil {
		t.Fatal(err)
	}
	state, err := Apply(Empty("placeholder", "default"), program)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Build(ctx, store, state)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(ctx, store, first.CID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(ctx, store, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !first.CID.Equals(second.CID) {
		t.Fatalf("state CID changed: %s != %s", first.CID, second.CID)
	}
}

func TestSemanticViewRequiresExactMarkdown(t *testing.T) {
	content := "# Intro\n\n[Next](next.md)\n"
	digest := sha256.Sum256([]byte(content))
	fields, err := FieldsFrom(struct {
		Path    string         `json:"path"`
		Content string         `json:"content"`
		SHA256  string         `json:"sha256"`
		Anchors map[string]int `json:"anchors"`
		Links   []struct {
			Destination string `json:"destination"`
			Line        int    `json:"line"`
		} `json:"links"`
	}{
		Path: "docs/intro.md", Content: content, SHA256: hex.EncodeToString(digest[:]),
		Anchors: map[string]int{"intro": 1},
		Links: []struct {
			Destination string `json:"destination"`
			Line        int    `json:"line"`
		}{{Destination: "next.md", Line: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := Empty("example.test", "default")
	state.Entities[Key("document", "intro")] = Entity{Kind: "document", ID: "intro", Fields: fields}
	_, documents, _, err := state.SemanticView()
	if err != nil {
		t.Fatal(err)
	}
	if documents["docs/intro.md"].Content != content {
		t.Fatal("semantic view did not preserve exact Markdown")
	}

	broken := state.Clone()
	broken.Entities[Key("document", "intro")].Fields["sha256"] = strings.Repeat("0", 64)
	if _, _, _, err := broken.SemanticView(); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("digest mismatch error = %v", err)
	}

	broken = state.Clone()
	broken.Entities[Key("document", "intro")].Fields["anchors"] = map[string]any{}
	if _, _, _, err := broken.SemanticView(); err == nil || !strings.Contains(err.Error(), "anchors or links") {
		t.Fatalf("index mismatch error = %v", err)
	}
}

func TestApplyIsAtomic(t *testing.T) {
	base := Empty("example.test", "default")
	program, err := txnlang.Parse([]byte("gridspec 1\nput graph g { kind = \"algorithm\" }\ndelete node absent\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(base, program); err == nil {
		t.Fatal("expected failed transaction")
	}
	if len(base.Entities) != 0 {
		t.Fatal("base state was mutated")
	}
}
