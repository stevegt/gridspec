// Package goworkflow implements the Go-focused compiler and disassembler
// vertical slice. Model output is untrusted until deterministic validation and
// Go tests pass.
package goworkflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/provider"
	"github.com/promisegrid/gridspec/internal/specsource"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
	"github.com/promisegrid/gridspec/internal/view"
)

const (
	manifestName     = ".gridspec-generated.json"
	manifestSchemaV1 = "gridspec.generated/v1"
	manifestSchemaV2 = "gridspec.generated/v2"
)

type SourceFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

type SourceBundle struct {
	Files []SourceFile `json:"files"`
}

type GeneratedFile struct {
	Path    string   `json:"path"`
	Content string   `json:"content"`
	SpecIDs []string `json:"spec_ids"`
}

type GeneratedSet struct {
	Files         []GeneratedFile `json:"files"`
	Summary       string          `json:"summary"`
	ProviderModel string          `json:"-"`
	ResponseID    string          `json:"-"`
}

type Manifest struct {
	Schema   string         `json:"schema"`
	Source   ManifestSource `json:"source,omitempty"`
	StateCID string         `json:"state_cid,omitempty"`
	Files    []ManifestFile `json:"files"`
}

type ManifestSource struct {
	Kind           string `json:"kind"`
	AcceptedCommit string `json:"accepted_commit"`
	Event          string `json:"event"`
	State          string `json:"state"`
}

type ManifestFile struct {
	Path    string   `json:"path"`
	SHA256  string   `json:"sha256"`
	SpecIDs []string `json:"spec_ids"`
}

// Scan captures exact Go/module inputs without following symlinks or entering
// VCS, vendor, or generated candidate directories.
func Scan(root string) (SourceBundle, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return SourceBundle{}, err
	}
	var result SourceBundle
	total := 0
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if path != abs && (entry.Name() == ".git" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".gridspec-build-")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" && entry.Name() != "go.mod" && entry.Name() != "go.sum" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += len(data)
		if total > 8<<20 {
			return fmt.Errorf("Go source bundle exceeds 8 MiB")
		}
		relative, _ := filepath.Rel(abs, path)
		digest := sha256.Sum256(data)
		result.Files = append(result.Files, SourceFile{Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(digest[:]), Content: string(data)})
		return nil
	})
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	return result, err
}

func BundleDigest(bundle SourceBundle) (string, error) {
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func loadState(ctx context.Context, store cas.Store, source specsource.Identity) (specstate.State, error) {
	if store == nil {
		return specstate.State{}, fmt.Errorf("CAS store is required")
	}
	if err := source.Validate(); err != nil {
		return specstate.State{}, err
	}
	state, err := specstate.Load(ctx, store, source.State)
	if err != nil {
		return specstate.State{}, fmt.Errorf("load compiler state %s from CAS: %w", source.State, err)
	}
	return state, nil
}

// Disassemble asks a provider to translate Go into a transaction, then parses
// and canonically reformats it. The caller publishes it as an unsigned proposal.
func Disassemble(ctx context.Context, model provider.Provider, store cas.Store, source specsource.Identity, root string) ([]byte, provider.Response, error) {
	if model == nil {
		return nil, provider.Response{}, fmt.Errorf("model provider is required")
	}
	if source.Kind != specsource.Accepted {
		return nil, provider.Response{}, fmt.Errorf("disassembly requires accepted specifications as its base")
	}
	base, err := loadState(ctx, store, source)
	if err != nil {
		return nil, provider.Response{}, err
	}
	bundle, err := Scan(root)
	if err != nil {
		return nil, provider.Response{}, err
	}
	current, err := view.Text(base)
	if err != nil {
		return nil, provider.Response{}, err
	}
	input, _ := json.Marshal(struct {
		AcceptedSpec string       `json:"accepted_spec"`
		Go           SourceBundle `json:"go"`
	}{string(current), bundle})
	response, err := model.Generate(ctx, provider.Request{
		Operation:    "go_to_gridspec",
		Instructions: "Disassemble the supplied Go implementation into a GridSpec transaction that updates the accepted specification. Return only a valid GridSpec transaction-language v1 program, beginning with 'gridspec 1'. Every new graph, node, and edge must have a trimmed single-line title of at most 40 characters; add a trimmed single-line summary of at most 140 characters where the implementation supports one. Preserve uncertainty as explicit contract metadata; do not claim whole-program equivalence.",
		Input:        string(input), MaxOutputTokens: 32_768,
	})
	if err != nil {
		return nil, response, err
	}
	program, err := txnlang.Parse([]byte(response.Text))
	if err != nil {
		return nil, response, fmt.Errorf("provider returned invalid GridSpec transaction: %w", err)
	}
	canonical, err := txnlang.Format(program)
	return canonical, response, err
}

// Generate asks a provider to compile one CAS-backed state into a closed file
// set. The state is always reloaded by CID at this compiler boundary.
func Generate(ctx context.Context, model provider.Provider, store cas.Store, source specsource.Identity) (GeneratedSet, provider.Response, error) {
	state, err := loadState(ctx, store, source)
	if err != nil {
		return GeneratedSet{}, provider.Response{}, err
	}
	return generate(ctx, model, state)
}

func generate(ctx context.Context, model provider.Provider, state specstate.State) (GeneratedSet, provider.Response, error) {
	if model == nil {
		return GeneratedSet{}, provider.Response{}, fmt.Errorf("model provider is required")
	}
	text, err := view.Text(state)
	if err != nil {
		return GeneratedSet{}, provider.Response{}, err
	}
	response, err := model.Generate(ctx, provider.Request{
		Operation:    "gridspec_to_go",
		Instructions: "Compile the supplied CAS-materialized GridSpec state into Go. Return only one strict JSON object: {\"files\":[{\"path\":string,\"content\":string,\"spec_ids\":[string]}],\"summary\":string}. A from-scratch build must include go.mod. Paths must be relative. Do not use Markdown fences. Generated code must compile and test; do not invent behavior omitted by the specs without documenting the choice in code.",
		Input:        string(text), MaxOutputTokens: 65_536,
	})
	if err != nil {
		return GeneratedSet{}, response, err
	}
	generated, err := parseGenerated([]byte(response.Text))
	generated.ProviderModel, generated.ResponseID = response.Model, response.ID
	return generated, response, err
}

// Build creates and tests a new output tree. Existing output is never replaced.
func Build(ctx context.Context, model provider.Provider, store cas.Store, source specsource.Identity, output string) (GeneratedSet, error) {
	state, err := loadState(ctx, store, source)
	if err != nil {
		return GeneratedSet{}, err
	}
	generated, _, err := generate(ctx, model, state)
	if err != nil {
		return GeneratedSet{}, err
	}
	hasModule := false
	for _, file := range generated.Files {
		hasModule = hasModule || file.Path == "go.mod"
	}
	if !hasModule {
		return GeneratedSet{}, fmt.Errorf("from-scratch build did not produce go.mod")
	}
	if _, err := os.Stat(output); err == nil || !os.IsNotExist(err) {
		return GeneratedSet{}, fmt.Errorf("build output %s already exists", output)
	}
	parent, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return GeneratedSet{}, err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return GeneratedSet{}, err
	}
	stage, err := os.MkdirTemp(parent, ".gridspec-build-")
	if err != nil {
		return GeneratedSet{}, err
	}
	defer os.RemoveAll(stage)
	if err := writeSet(stage, source, generated); err != nil {
		return GeneratedSet{}, err
	}
	if err := testGo(ctx, stage); err != nil {
		return GeneratedSet{}, err
	}
	if err := os.Rename(stage, output); err != nil {
		return GeneratedSet{}, err
	}
	return generated, nil
}

// UpdateResult describes a deterministic proposed source update.
type UpdateResult struct {
	Generated GeneratedSet
	Changes   []string
}

// Update compiles against existing code, enforces the ownership manifest, and
// optionally writes changes. A failing Go test rolls written files back.
func Update(ctx context.Context, model provider.Provider, store cas.Store, source specsource.Identity, root string, write bool) (UpdateResult, error) {
	if model == nil {
		return UpdateResult{}, fmt.Errorf("model provider is required")
	}
	if write && source.IsProposal() {
		return UpdateResult{}, fmt.Errorf("writing updates requires accepted specifications")
	}
	state, err := loadState(ctx, store, source)
	if err != nil {
		return UpdateResult{}, err
	}
	bundle, err := Scan(root)
	if err != nil {
		return UpdateResult{}, err
	}
	spec, err := view.Text(state)
	if err != nil {
		return UpdateResult{}, err
	}
	input, _ := json.Marshal(struct {
		MaterializedSpec string       `json:"materialized_spec"`
		Existing         SourceBundle `json:"existing_go"`
	}{string(spec), bundle})
	response, err := model.Generate(ctx, provider.Request{
		Operation:    "update_go_from_gridspec",
		Instructions: "Update only GridSpec-owned Go files to conform to the supplied CAS-materialized state. Return only strict JSON {\"files\":[{\"path\":string,\"content\":string,\"spec_ids\":[string]}],\"summary\":string}. Never return unowned files, Markdown fences, deletions, or signing material.",
		Input:        string(input), MaxOutputTokens: 65_536,
	})
	if err != nil {
		return UpdateResult{}, err
	}
	generated, err := parseGenerated([]byte(response.Text))
	if err != nil {
		return UpdateResult{}, err
	}
	generated.ProviderModel, generated.ResponseID = response.Model, response.ID
	manifest, err := loadManifest(root)
	if err != nil {
		return UpdateResult{}, err
	}
	owned := make(map[string]ManifestFile)
	for _, file := range manifest.Files {
		owned[file.Path] = file
	}
	result := UpdateResult{Generated: generated}
	for _, file := range generated.Files {
		previous, exists := owned[file.Path]
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		old, readErr := os.ReadFile(path)
		if readErr == nil {
			if !exists {
				return UpdateResult{}, fmt.Errorf("refusing to overwrite unowned file %s", file.Path)
			}
			digest := sha256.Sum256(old)
			if hex.EncodeToString(digest[:]) != previous.SHA256 {
				return UpdateResult{}, fmt.Errorf("owned file %s changed outside GridSpec", file.Path)
			}
			if bytes.Equal(old, []byte(file.Content)) {
				continue
			}
			result.Changes = append(result.Changes, "M "+file.Path)
		} else if os.IsNotExist(readErr) {
			if exists {
				return UpdateResult{}, fmt.Errorf("owned file %s is missing", file.Path)
			}
			if !strings.HasPrefix(file.Path, "gridspec_gen/") {
				return UpdateResult{}, fmt.Errorf("new generated file %s must be under gridspec_gen/", file.Path)
			}
			result.Changes = append(result.Changes, "A "+file.Path)
		} else {
			return UpdateResult{}, readErr
		}
	}
	if !write {
		return result, nil
	}
	backups := make(map[string][]byte)
	existed := make(map[string]bool)
	manifestPath := filepath.Join(root, manifestName)
	backups[manifestPath], _ = os.ReadFile(manifestPath)
	_, statErr := os.Stat(manifestPath)
	existed[manifestPath] = statErr == nil
	for _, file := range generated.Files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		backups[path], _ = os.ReadFile(path)
		_, statErr := os.Stat(path)
		existed[path] = statErr == nil
	}
	rollback := func() {
		for path, data := range backups {
			if existed[path] {
				_ = os.WriteFile(path, data, 0o644)
			} else {
				_ = os.Remove(path)
			}
		}
	}
	if err := writeUpdateSet(root, source, generated, manifest); err != nil {
		rollback()
		return UpdateResult{}, err
	}
	if err := testGo(ctx, root); err != nil {
		rollback()
		return UpdateResult{}, err
	}
	return result, nil
}

// Digest identifies the normalized provider file set, independent of the
// destination directory and ownership-manifest formatting.
func Digest(generated GeneratedSet) (string, error) {
	projection := GeneratedSet{Files: generated.Files, Summary: generated.Summary}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func parseGenerated(data []byte) (GeneratedSet, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var generated GeneratedSet
	if err := decoder.Decode(&generated); err != nil {
		return GeneratedSet{}, fmt.Errorf("decode generated file set: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return GeneratedSet{}, fmt.Errorf("generated response has trailing JSON")
	}
	if len(generated.Files) == 0 {
		return GeneratedSet{}, fmt.Errorf("generated file set is empty")
	}
	seen := make(map[string]bool)
	total := 0
	for index := range generated.Files {
		file := &generated.Files[index]
		total += len(file.Content)
		if total > 32<<20 {
			return GeneratedSet{}, fmt.Errorf("generated file set exceeds 32 MiB")
		}
		clean := filepath.ToSlash(filepath.Clean(file.Path))
		if clean != file.Path || filepath.IsAbs(file.Path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || seen[clean] {
			return GeneratedSet{}, fmt.Errorf("invalid or duplicate generated path %q", file.Path)
		}
		if filepath.Ext(clean) != ".go" && clean != "go.mod" && clean != "go.sum" {
			return GeneratedSet{}, fmt.Errorf("generated path %s is not a Go module file", clean)
		}
		seen[clean] = true
		if filepath.Ext(clean) == ".go" {
			formatted, err := format.Source([]byte(file.Content))
			if err != nil {
				return GeneratedSet{}, fmt.Errorf("gofmt %s: %w", clean, err)
			}
			file.Content = string(formatted)
		}
		sort.Strings(file.SpecIDs)
		for specIndex, specID := range file.SpecIDs {
			if !txnlang.IsIdentifier(specID) || specIndex > 0 && specID == file.SpecIDs[specIndex-1] {
				return GeneratedSet{}, fmt.Errorf("invalid or duplicate spec ID %q for %s", specID, file.Path)
			}
		}
	}
	sort.Slice(generated.Files, func(i, j int) bool { return generated.Files[i].Path < generated.Files[j].Path })
	return generated, nil
}

func manifestSource(source specsource.Identity) ManifestSource {
	return ManifestSource{
		Kind: string(source.Kind), AcceptedCommit: source.AcceptedCommit.String(),
		Event: source.Event.String(), State: source.State.String(),
	}
}

func writeSet(root string, source specsource.Identity, generated GeneratedSet) error {
	manifest := Manifest{Schema: manifestSchemaV2, Source: manifestSource(source)}
	for _, file := range generated.Files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(file.Content), 0o644); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(file.Content))
		manifest.Files = append(manifest.Files, ManifestFile{Path: file.Path, SHA256: hex.EncodeToString(digest[:]), SpecIDs: file.SpecIDs})
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	encoded = append(encoded, '\n')
	return os.WriteFile(filepath.Join(root, manifestName), encoded, 0o644)
}

func writeUpdateSet(root string, source specsource.Identity, generated GeneratedSet, previous Manifest) error {
	byPath := make(map[string]ManifestFile, len(previous.Files)+len(generated.Files))
	for _, file := range previous.Files {
		byPath[file.Path] = file
	}
	for _, file := range generated.Files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(file.Content), 0o644); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(file.Content))
		byPath[file.Path] = ManifestFile{Path: file.Path, SHA256: hex.EncodeToString(digest[:]), SpecIDs: file.SpecIDs}
	}
	manifest := Manifest{Schema: manifestSchemaV2, Source: manifestSource(source)}
	for _, file := range byPath {
		manifest.Files = append(manifest.Files, file)
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	encoded = append(encoded, '\n')
	return os.WriteFile(filepath.Join(root, manifestName), encoded, 0o644)
}

func loadManifest(root string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if os.IsNotExist(err) {
		return Manifest{Schema: manifestSchemaV2}, nil
	}
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid %s", manifestName)
	}
	switch manifest.Schema {
	case manifestSchemaV1:
		if _, err := cid.Parse(manifest.StateCID); err != nil {
			return Manifest{}, fmt.Errorf("invalid %s v1 state CID", manifestName)
		}
	case manifestSchemaV2:
		identity, err := manifest.Source.identity()
		if err != nil {
			return Manifest{}, fmt.Errorf("invalid %s v2 source: %w", manifestName, err)
		}
		if err := identity.Validate(); err != nil {
			return Manifest{}, fmt.Errorf("invalid %s v2 source: %w", manifestName, err)
		}
	default:
		return Manifest{}, fmt.Errorf("invalid %s schema", manifestName)
	}
	return manifest, nil
}

func (source ManifestSource) identity() (specsource.Identity, error) {
	accepted, err := cid.Parse(source.AcceptedCommit)
	if err != nil {
		return specsource.Identity{}, err
	}
	event, err := cid.Parse(source.Event)
	if err != nil {
		return specsource.Identity{}, err
	}
	state, err := cid.Parse(source.State)
	if err != nil {
		return specsource.Identity{}, err
	}
	return specsource.Identity{Kind: specsource.Kind(source.Kind), AcceptedCommit: accepted, Event: event, State: state}, nil
}

func testGo(ctx context.Context, root string) error {
	command := exec.CommandContext(ctx, "go", "test", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generated Go tests failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}
