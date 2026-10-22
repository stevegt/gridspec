package goworkflow

import (
	"context"
	"encoding/json"
	"fmt"
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
	state, err := specstate.Build(ctx, store, qualifiedState("example.test/generated", "1\n"))
	if err != nil {
		t.Fatal(err)
	}
	source := testSource(state)
	initial := `{"files":[{"path":"go.mod","content":"module example.test/generated\n\ngo 1.25\n","spec_ids":[]},{"path":"main.go","content":"package main\n\nimport \"fmt\"\nfunc Value() int { return 1 }\nfunc main() { fmt.Println(Value()) }\n","spec_ids":["value"]},{"path":"main_test.go","content":"package main\n\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n","spec_ids":["value"]}],"artifacts":[{"name":"counter","package":"."}],"summary":"initial"}`
	output := filepath.Join(t.TempDir(), "output")
	firstBuild, err := Build(ctx, provider.Static{Text: initial}, store, source, output)
	if err != nil {
		t.Fatal(err)
	}
	secondBuild, err := Build(ctx, provider.Static{Text: initial}, store, source, filepath.Join(t.TempDir(), "output"))
	if err != nil {
		t.Fatal(err)
	}
	firstEvidence, _ := QualificationEvidence(firstBuild)
	secondEvidence, _ := QualificationEvidence(secondBuild)
	if string(firstEvidence) != string(secondEvidence) {
		t.Fatalf("qualification evidence is not deterministic:\n%s\n%s", firstEvidence, secondEvidence)
	}
	if _, err := os.Stat(filepath.Join(output, manifestName)); err != nil {
		t.Fatal(err)
	}
	builtManifest, err := loadManifest(output)
	if err != nil {
		t.Fatal(err)
	}
	if builtManifest.Schema != manifestSchemaV3 {
		t.Fatalf("build manifest schema = %q", builtManifest.Schema)
	}
	if len(builtManifest.Artifacts) != 1 || builtManifest.Artifacts[0].Name != "counter" || builtManifest.Qualification.Schema != qualificationSchema || len(builtManifest.Qualification.Scenarios) != 1 {
		t.Fatalf("build manifest qualification metadata = %#v", builtManifest)
	}
	legacyManifest := Manifest{Schema: manifestSchemaV1, StateCID: state.CID.String(), Files: builtManifest.Files}
	legacyBytes, err := json.Marshal(legacyManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, manifestName), legacyBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	updated := `{"files":[{"path":"main.go","content":"package main\n\nimport \"fmt\"\nfunc Value() int { return 1 }\nfunc main() { fmt.Println(Value()) }\n","spec_ids":["value"]}],"artifacts":[{"name":"counter","package":"."}],"summary":"same behavior"}`
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
	if manifest.Schema != manifestSchemaV3 || manifest.Source.State != state.CID.String() || manifest.Source.Event != source.Event.String() {
		t.Fatalf("manifest does not pin compiler source: %#v", manifest)
	}
}

func TestBuildRejectsSelfTestedBehaviorMismatchWithoutOutput(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, qualifiedState("example.test/faulty", "1\n"))
	if err != nil {
		t.Fatal(err)
	}
	response := `{"files":[{"path":"go.mod","content":"module example.test/faulty\n\ngo 1.25\n","spec_ids":[]},{"path":"main.go","content":"package main\n\nimport \"fmt\"\nfunc Value() int { return 2 }\nfunc main() { fmt.Println(Value()) }\n","spec_ids":["value"]},{"path":"main_test.go","content":"package main\n\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 2 { t.Fatal(Value()) } }\n","spec_ids":["value"]}],"artifacts":[{"name":"counter","package":"."}],"summary":"faulty"}`
	output := filepath.Join(t.TempDir(), "output")
	if _, err := Build(ctx, provider.Static{Text: response}, store, testSource(state), output); err == nil || !strings.Contains(err.Error(), "observed exit/stdout/stderr") {
		t.Fatalf("qualification error = %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed build left output: %v", err)
	}
}

func TestFailedWriteQualificationLeavesTreeAndManifestUnchanged(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, qualifiedState("example.test/rollback", "1\n"))
	if err != nil {
		t.Fatal(err)
	}
	source := testSource(state)
	initial := `{"files":[{"path":"go.mod","content":"module example.test/rollback\n\ngo 1.25\n","spec_ids":[]},{"path":"main.go","content":"package main\n\nimport \"fmt\"\nfunc Value() int { return 1 }\nfunc main() { fmt.Println(Value()) }\n","spec_ids":["value"]},{"path":"main_test.go","content":"package main\n\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n","spec_ids":["value"]}],"artifacts":[{"name":"counter","package":"."}],"summary":"initial"}`
	root := filepath.Join(t.TempDir(), "output")
	if _, err := Build(ctx, provider.Static{Text: initial}, store, source, root); err != nil {
		t.Fatal(err)
	}
	beforeMain, _ := os.ReadFile(filepath.Join(root, "main.go"))
	beforeManifest, _ := os.ReadFile(filepath.Join(root, manifestName))
	faulty := `{"files":[{"path":"main.go","content":"package main\n\nimport \"fmt\"\nfunc Value() int { return 2 }\nfunc main() { fmt.Println(Value()) }\n","spec_ids":["value"]},{"path":"main_test.go","content":"package main\n\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 2 { t.Fatal(Value()) } }\n","spec_ids":["value"]}],"artifacts":[{"name":"counter","package":"."}],"summary":"faulty"}`
	if _, err := Update(ctx, provider.Static{Text: faulty}, store, source, root, true); err == nil {
		t.Fatal("faulty update qualified")
	}
	afterMain, _ := os.ReadFile(filepath.Join(root, "main.go"))
	afterManifest, _ := os.ReadFile(filepath.Join(root, manifestName))
	if string(beforeMain) != string(afterMain) || string(beforeManifest) != string(afterManifest) {
		t.Fatal("failed qualification mutated source or manifest")
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

func TestBuildWithoutAcceptedScenarioStopsBeforeModelOrOutput(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, specstate.Empty("example.test/no-oracle", "default"))
	if err != nil {
		t.Fatal(err)
	}
	model := &countingProvider{}
	output := filepath.Join(t.TempDir(), "output")
	if _, err := Build(ctx, model, store, testSource(state), output); err == nil || !strings.Contains(err.Error(), "no active command scenarios") {
		t.Fatalf("missing scenario error = %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("provider called %d time(s)", model.calls)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("missing scenario build left output: %v", err)
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

func TestLoadV2ManifestForCompatibility(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	state, err := specstate.Build(ctx, store, specstate.Empty("example.test/v2", "default"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifest := Manifest{Schema: manifestSchemaV2, Source: manifestSource(testSource(state)), Files: []ManifestFile{}}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, manifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadManifest(root)
	if err != nil || loaded.Schema != manifestSchemaV2 || loaded.Source.State != state.CID.String() {
		t.Fatalf("v2 manifest=%#v err=%v", loaded, err)
	}
}

func TestGeneratedArtifactsMustUseLocalPackages(t *testing.T) {
	base := `{"files":[{"path":"go.mod","content":"module example.test/local\n\ngo 1.25\n","spec_ids":[]}],"artifacts":[{"name":"tool","package":%q}],"summary":"test"}`
	for _, packageName := range []string{"../outside", "/outside", "example.com/remote", "./../outside", "-flag", "./...", "./cmd/..."} {
		t.Run(packageName, func(t *testing.T) {
			if _, err := parseGenerated([]byte(fmt.Sprintf(base, packageName))); err == nil {
				t.Fatalf("accepted artifact package %q", packageName)
			}
		})
	}
	for _, packageName := range []string{".", "./cmd/tool"} {
		t.Run(packageName, func(t *testing.T) {
			if _, err := parseGenerated([]byte(fmt.Sprintf(base, packageName))); err != nil {
				t.Fatalf("rejected artifact package %q: %v", packageName, err)
			}
		})
	}
}

func testSource(state specstate.State) specsource.Identity {
	return specsource.Identity{
		Kind: specsource.Accepted, AcceptedCommit: state.CID,
		Event: state.CID, State: state.CID,
	}
}

func qualifiedState(module, stdout string) specstate.State {
	exit := int64(0)
	return specstate.State{Module: module, Profile: "default", Entities: map[string]specstate.Entity{
		specstate.Key("graph", "scenario.output"): {
			Kind: "graph", ID: "scenario.output", Fields: map[string]any{
				"title": "Expected output", "summary": "Checks compiler-owned command output.",
				"kind": "scenario", "status": "active", "directed": true, "strict": false, "attrs": map[string]any{},
			},
		},
		specstate.Key("contract", "scenario.output"): {
			Kind: "contract", ID: "scenario.output", Fields: map[string]any{
				"schema": "gridspec.contract/v1", "for": "scenario.output", "kind": "scenario", "status": "active",
				"scenario": map[string]any{
					"artifact": "counter", "covers": []any{"scenario.output"}, "repeat": int64(1), "fixtures": []any{},
					"steps": []any{map[string]any{
						"id": "step.output", "args": []any{}, "stdin": "", "timeout": "5s",
						"expect": map[string]any{"exit_code": exit, "stdout": stdout, "stderr": "", "files": []any{}},
					}},
				},
			},
		},
	}}
}

type countingProvider struct{ calls int }

func (model *countingProvider) Generate(context.Context, provider.Request) (provider.Response, error) {
	model.calls++
	return provider.Response{Text: `{}`}, nil
}
