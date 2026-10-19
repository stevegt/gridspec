package goworkflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/specsource"
	"github.com/promisegrid/gridspec/internal/specstate"
)

func TestBuildAndOwnedUpdate(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, specstate.Empty("example.test/generated", "default"))
	if err != nil {
		t.Fatal(err)
	}
	source := testSource(state)
	initial := `{"files":[{"path":"go.mod","content":"module example.test/generated\n\ngo 1.25\n","spec_ids":[]},{"path":"main.go","content":"package generated\n\nfunc Value() int { return 1 }\n","spec_ids":["value"]},{"path":"main_test.go","content":"package generated\n\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n","spec_ids":["value"]}],"summary":"initial"}`
	output := filepath.Join(t.TempDir(), "output")
	if _, err := Build(ctx, provider.Static{Text: initial}, store, source, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, manifestName)); err != nil {
		t.Fatal(err)
	}
	builtManifest, err := loadManifest(output)
	if err != nil {
		t.Fatal(err)
	}
	if builtManifest.Schema != manifestSchemaV2 {
		t.Fatalf("build manifest schema = %q", builtManifest.Schema)
	}
	legacyManifest := Manifest{Schema: manifestSchemaV1, StateCID: state.CID.String(), Files: builtManifest.Files}
	legacyBytes, err := json.Marshal(legacyManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, manifestName), legacyBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	updated := `{"files":[{"path":"main.go","content":"package generated\n\nfunc Value() int { return 1 }\n","spec_ids":["value"]}],"summary":"same behavior"}`
	result, err := Update(ctx, provider.Static{Text: updated}, store, source, output, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("unexpected changes: %v", result.Changes)
	}
	manifest, err := loadManifest(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 3 {
		t.Fatalf("partial update dropped ownership entries: %#v", manifest.Files)
	}
	if manifest.Schema != manifestSchemaV2 || manifest.Source.State != state.CID.String() || manifest.Source.Event != source.Event.String() {
		t.Fatalf("manifest does not pin compiler source: %#v", manifest)
	}
}

func TestDisassembleValidatesTransaction(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, _ := specstate.Build(ctx, store, specstate.Empty("example.test/source", "default"))
	source := testSource(state)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/source\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	program, _, err := Disassemble(ctx, provider.Static{Text: "gridspec 1\nput document implementation { path = \"README.md\" }\n"}, store, source, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(program), "gridspec 1\n") {
		t.Fatalf("program = %q", program)
	}
	if _, _, err := Disassemble(ctx, provider.Static{Text: "not a transaction"}, store, source, root); err == nil {
		t.Fatal("invalid provider output was accepted")
	}
}

func TestCompilerRequiresMaterializedCASState(t *testing.T) {
	ctx := context.Background()
	populated := cas.NewMemory()
	state, err := specstate.Build(ctx, populated, specstate.Empty("example.test/missing", "default"))
	if err != nil {
		t.Fatal(err)
	}
	model := &countingProvider{}
	_, _, err = Generate(ctx, model, cas.NewMemory(), testSource(state))
	if err == nil || !strings.Contains(err.Error(), "load compiler state") {
		t.Fatalf("missing CAS state error = %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("provider called %d time(s) before CAS verification", model.calls)
	}
}

func TestProposalUpdateCannotWrite(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, specstate.Empty("example.test/preview", "default"))
	if err != nil {
		t.Fatal(err)
	}
	source := testSource(state)
	source.Kind = specsource.Proposal
	_, err = Update(ctx, provider.Static{Text: `{}`}, store, source, t.TempDir(), true)
	if err == nil || !strings.Contains(err.Error(), "requires accepted") {
		t.Fatalf("proposal write error = %v", err)
	}
}

func TestLoadV1ManifestForCompatibility(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, specstate.Empty("example.test/v1", "default"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	data := `{"schema":"gridspec.generated/v1","state_cid":"` + state.CID.String() + `","files":[]}`
	if err := os.WriteFile(filepath.Join(root, manifestName), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := loadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != manifestSchemaV1 || manifest.StateCID != state.CID.String() {
		t.Fatalf("v1 manifest changed during load: %#v", manifest)
	}
}

func testSource(state specstate.State) specsource.Identity {
	return specsource.Identity{
		Kind: specsource.Accepted, AcceptedCommit: state.CID,
		Event: state.CID, State: state.CID,
	}
}

type countingProvider struct{ calls int }

func (model *countingProvider) Generate(context.Context, provider.Request) (provider.Response, error) {
	model.calls++
	return provider.Response{Text: `{}`}, nil
}
