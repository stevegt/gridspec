// Package gitstore stores complete Grid object closures in ordinary Git
// objects. Git is a carrier: the CID named by ROOT, not the carrier commit ID,
// is the semantic identity of a published value.
package gitstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ipfs/go-cid"
)

const rootFilename = "ROOT"

// Repository is both a writable CID store and a reader over one or more
// immutable carrier commits. Newly written blobs are addressable before they
// are published.
type Repository struct {
	Dir    string
	GitDir string

	mu       sync.RWMutex
	staged   map[string]string // CID text -> Git blob object ID
	cache    map[string][]byte // CID text -> CID-verified bytes
	carriers []string          // oldest to newest
}

// Open discovers the containing non-bare Git repository from dir.
func Open(ctx context.Context, dir string) (*Repository, error) {
	if dir == "" {
		dir = "."
	}
	top, err := gitOutput(ctx, dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("discover Git worktree: %w", err)
	}
	gitDir, err := gitOutput(ctx, strings.TrimSpace(string(top)), nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, fmt.Errorf("discover Git directory: %w", err)
	}
	return &Repository{
		Dir: strings.TrimSpace(string(top)), GitDir: strings.TrimSpace(string(gitDir)),
		staged: make(map[string]string), cache: make(map[string][]byte),
	}, nil
}

// Get returns exact block bytes and verifies them against the requested CID.
func (repository *Repository) Get(ctx context.Context, blockCID cid.Cid) ([]byte, error) {
	if repository == nil || !blockCID.Defined() {
		return nil, fmt.Errorf("repository and defined CID are required")
	}
	repository.mu.RLock()
	cached, cachedOK := repository.cache[blockCID.String()]
	objectID := repository.staged[blockCID.String()]
	carriers := append([]string(nil), repository.carriers...)
	repository.mu.RUnlock()
	if cachedOK {
		return bytes.Clone(cached), nil
	}
	var data []byte
	var err error
	if objectID != "" {
		data, err = gitOutput(ctx, repository.Dir, nil, "cat-file", "blob", objectID)
	} else {
		path := blockPath(blockCID)
		for index := len(carriers) - 1; index >= 0; index-- {
			data, err = gitOutput(ctx, repository.Dir, nil, "show", carriers[index]+":"+path)
			if err == nil {
				break
			}
		}
	}
	if err != nil || data == nil {
		return nil, fmt.Errorf("block %s is absent from loaded Git carriers", blockCID)
	}
	recomputed, sumErr := blockCID.Prefix().Sum(data)
	if sumErr != nil || !recomputed.Equals(blockCID) {
		return nil, fmt.Errorf("Git carrier bytes do not match CID %s", blockCID)
	}
	repository.mu.Lock()
	repository.cache[blockCID.String()] = bytes.Clone(data)
	repository.mu.Unlock()
	return bytes.Clone(data), nil
}

// Put validates data and stages it in memory. Publish batch-writes any bytes
// that are not already backed by a verified Git blob.
func (repository *Repository) Put(ctx context.Context, blockCID cid.Cid, data []byte) error {
	if repository == nil || !blockCID.Defined() {
		return fmt.Errorf("repository and defined CID are required")
	}
	recomputed, err := blockCID.Prefix().Sum(data)
	if err != nil || !recomputed.Equals(blockCID) {
		return fmt.Errorf("block bytes do not match CID %s", blockCID)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := blockCID.String()
	if existing, ok := repository.cache[key]; ok && !bytes.Equal(existing, data) {
		return fmt.Errorf("CID %s already names different bytes", blockCID)
	}
	repository.cache[key] = bytes.Clone(data)
	return nil
}

// ResolveRef returns the carrier commit selected by ref. A missing ref returns
// an empty string and no error.
func (repository *Repository) ResolveRef(ctx context.Context, ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	if _, err := gitOutput(ctx, repository.Dir, nil, "check-ref-format", ref); err != nil {
		return "", fmt.Errorf("invalid Git ref %q", ref)
	}
	output, err := gitOutput(ctx, repository.Dir, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(output)), nil
}

// AddCarrier validates and mounts an immutable carrier commit for reads.
func (repository *Repository) AddCarrier(ctx context.Context, revision string) (cid.Cid, string, error) {
	commitBytes, err := gitOutput(ctx, repository.Dir, nil, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return cid.Undef, "", fmt.Errorf("resolve carrier %q: %w", revision, err)
	}
	commit := strings.TrimSpace(string(commitBytes))
	if err := repository.validateCarrierTree(ctx, commit); err != nil {
		return cid.Undef, "", err
	}
	rootBytes, err := gitOutput(ctx, repository.Dir, nil, "show", commit+":"+rootFilename)
	if err != nil {
		return cid.Undef, "", fmt.Errorf("read carrier ROOT: %w", err)
	}
	root, err := cid.Parse(strings.TrimSpace(string(rootBytes)))
	if err != nil || root.Version() != 1 {
		return cid.Undef, "", fmt.Errorf("carrier ROOT is not a CIDv1")
	}
	repository.mu.Lock()
	for _, existing := range repository.carriers {
		if existing == commit {
			repository.mu.Unlock()
			return root, commit, nil
		}
	}
	repository.mu.Unlock()
	if err := repository.preloadCarrier(ctx, commit); err != nil {
		return cid.Undef, "", err
	}
	repository.mu.Lock()
	repository.carriers = append(repository.carriers, commit)
	repository.mu.Unlock()
	if _, err := repository.Get(ctx, root); err != nil {
		return cid.Undef, "", fmt.Errorf("carrier omits ROOT block: %w", err)
	}
	return root, commit, nil
}

func (repository *Repository) validateCarrierTree(ctx context.Context, commit string) error {
	encoded, err := gitOutput(ctx, repository.Dir, nil, "ls-tree", "-z", commit)
	if err != nil {
		return fmt.Errorf("read carrier tree: %w", err)
	}
	entries := bytes.Split(encoded, []byte{0})
	seen := make(map[string]string)
	for _, raw := range entries {
		if len(raw) == 0 {
			continue
		}
		parts := bytes.SplitN(raw, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid carrier root tree")
		}
		fields := strings.Fields(string(parts[0]))
		if len(fields) != 3 {
			return fmt.Errorf("invalid carrier root tree")
		}
		seen[string(parts[1])] = fields[1]
	}
	if len(seen) != 2 || seen[rootFilename] != "blob" || seen["blocks"] != "tree" {
		return fmt.Errorf("carrier root tree must contain only ROOT and blocks")
	}
	return nil
}

// preloadCarrier streams all carrier blobs through one git cat-file process.
// Each block is still independently checked against the CID encoded by its
// tree path.
func (repository *Repository) preloadCarrier(ctx context.Context, commit string) error {
	tree, err := gitOutput(ctx, repository.Dir, nil, "ls-tree", "-r", "-z", commit, "--", "blocks")
	if err != nil {
		return fmt.Errorf("index carrier blocks: %w", err)
	}
	type wantedBlock struct {
		cid cid.Cid
		oid string
	}
	var wanted []wantedBlock
	for _, raw := range bytes.Split(tree, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		parts := bytes.SplitN(raw, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid carrier tree entry")
		}
		fields := strings.Fields(string(parts[0]))
		if len(fields) != 3 || fields[1] != "blob" {
			return fmt.Errorf("invalid carrier tree entry")
		}
		path := string(parts[1])
		pieces := strings.Split(path, "/")
		if len(pieces) != 3 || pieces[0] != "blocks" || len(pieces[1]) != 2 {
			return fmt.Errorf("invalid carrier block path %q", path)
		}
		blockCID, err := cid.Parse(pieces[1] + pieces[2])
		if err != nil || blockPath(blockCID) != path {
			return fmt.Errorf("carrier path %q is not a canonical CID path", path)
		}
		wanted = append(wanted, wantedBlock{cid: blockCID, oid: fields[2]})
		if len(wanted) > 1_000_000 {
			return fmt.Errorf("carrier contains too many blocks")
		}
	}
	if len(wanted) == 0 {
		return fmt.Errorf("carrier has no blocks")
	}
	var request strings.Builder
	for _, block := range wanted {
		request.WriteString(block.oid)
		request.WriteByte('\n')
	}
	batch, err := gitOutput(ctx, repository.Dir, []byte(request.String()), "cat-file", "--batch")
	if err != nil {
		return fmt.Errorf("read carrier block batch: %w", err)
	}
	reader := bufio.NewReader(bytes.NewReader(batch))
	loaded := make(map[string][]byte, len(wanted))
	var total int64
	for _, block := range wanted {
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read carrier blob header: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != block.oid || fields[1] != "blob" {
			return fmt.Errorf("unexpected carrier blob header %q", strings.TrimSpace(header))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > 64<<20 {
			return fmt.Errorf("invalid carrier blob size")
		}
		total += size
		if total > 512<<20 {
			return fmt.Errorf("carrier block data exceeds 512 MiB")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return err
		}
		terminator, err := reader.ReadByte()
		if err != nil || terminator != '\n' {
			return fmt.Errorf("invalid git cat-file batch framing")
		}
		recomputed, err := block.cid.Prefix().Sum(data)
		if err != nil || !recomputed.Equals(block.cid) {
			return fmt.Errorf("Git carrier bytes do not match CID %s", block.cid)
		}
		loaded[block.cid.String()] = data
	}
	repository.mu.Lock()
	for key, data := range loaded {
		repository.cache[key] = bytes.Clone(data)
	}
	for _, block := range wanted {
		repository.staged[block.cid.String()] = block.oid
	}
	repository.mu.Unlock()
	return nil
}

// Publish writes a complete, self-contained closure and atomically advances
// ref from expectedCarrier. Pass an empty ref to create an unreferenced carrier.
func (repository *Repository) Publish(ctx context.Context, root cid.Cid, closure []cid.Cid, ref, expectedCarrier, message string) (string, error) {
	if repository == nil || !root.Defined() {
		return "", fmt.Errorf("repository and defined root CID are required")
	}
	if ref != "" {
		if err := ValidateRef(ref); err != nil {
			return "", err
		}
		if _, err := gitOutput(ctx, repository.Dir, nil, "check-ref-format", ref); err != nil {
			return "", fmt.Errorf("invalid Git ref %q", ref)
		}
	}
	byText := make(map[string]cid.Cid, len(closure)+1)
	byText[root.String()] = root
	for _, value := range closure {
		if value.Defined() {
			byText[value.String()] = value
		}
	}
	ordered := make([]cid.Cid, 0, len(byText))
	for _, value := range byText {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	if err := repository.materialize(ctx, ordered); err != nil {
		return "", err
	}

	groups := make(map[string][]treeEntry)
	for _, blockCID := range ordered {
		repository.mu.RLock()
		objectID := repository.staged[blockCID.String()]
		repository.mu.RUnlock()
		text := blockCID.String()
		groups[text[:2]] = append(groups[text[:2]], treeEntry{name: text[2:], objectID: objectID, kind: "blob", mode: "100644"})
	}

	prefixes := make([]string, 0, len(groups))
	for prefix := range groups {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	blockEntries := make([]treeEntry, 0, len(prefixes))
	for _, prefix := range prefixes {
		tree, err := repository.writeTree(ctx, groups[prefix])
		if err != nil {
			return "", err
		}
		blockEntries = append(blockEntries, treeEntry{name: prefix, objectID: tree, kind: "tree", mode: "040000"})
	}
	blocksTree, err := repository.writeTree(ctx, blockEntries)
	if err != nil {
		return "", err
	}
	rootObjectBytes, err := gitOutput(ctx, repository.Dir, []byte(root.String()+"\n"), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	rootTree, err := repository.writeTree(ctx, []treeEntry{
		{name: rootFilename, objectID: strings.TrimSpace(string(rootObjectBytes)), kind: "blob", mode: "100644"},
		{name: "blocks", objectID: blocksTree, kind: "tree", mode: "040000"},
	})
	if err != nil {
		return "", err
	}
	if message == "" {
		message = "GridSpec carrier for " + root.String()
	}
	arguments := []string{"commit-tree", rootTree}
	if expectedCarrier != "" {
		arguments = append(arguments, "-p", expectedCarrier)
	}
	commitBytes, err := gitOutput(ctx, repository.Dir, []byte(message+"\n"), arguments...)
	if err != nil {
		return "", fmt.Errorf("write carrier commit: %w", err)
	}
	commit := strings.TrimSpace(string(commitBytes))
	if ref != "" {
		old := expectedCarrier
		if old == "" {
			old = strings.Repeat("0", len(commit))
		}
		if _, err := gitOutput(ctx, repository.Dir, nil, "update-ref", "-m", "gridspec publish", ref, commit, old); err != nil {
			return "", fmt.Errorf("atomically update %s: %w", ref, err)
		}
	}
	repository.mu.Lock()
	repository.carriers = append(repository.carriers, commit)
	repository.mu.Unlock()
	return commit, nil
}

// materialize writes all newly staged blocks with one Git process. Blocks
// loaded from a carrier already have verified blob IDs and require no write.
func (repository *Repository) materialize(ctx context.Context, blocks []cid.Cid) error {
	type pendingBlock struct {
		cid  cid.Cid
		data []byte
	}
	pending := make([]pendingBlock, 0)
	for _, blockCID := range blocks {
		repository.mu.RLock()
		objectID := repository.staged[blockCID.String()]
		data, ok := repository.cache[blockCID.String()]
		repository.mu.RUnlock()
		if objectID != "" {
			continue
		}
		if !ok {
			return fmt.Errorf("publish incomplete closure: block %s is absent", blockCID)
		}
		recomputed, err := blockCID.Prefix().Sum(data)
		if err != nil || !recomputed.Equals(blockCID) {
			return fmt.Errorf("publish incomplete closure: block %s is corrupt", blockCID)
		}
		pending = append(pending, pendingBlock{cid: blockCID, data: bytes.Clone(data)})
	}
	if len(pending) == 0 {
		return nil
	}

	directory, err := os.MkdirTemp("", "gridspec-git-objects-")
	if err != nil {
		return fmt.Errorf("create Git object staging directory: %w", err)
	}
	defer os.RemoveAll(directory)
	var paths strings.Builder
	for index, block := range pending {
		path := filepath.Join(directory, strconv.Itoa(index))
		if err := os.WriteFile(path, block.data, 0o600); err != nil {
			return fmt.Errorf("stage Git object: %w", err)
		}
		paths.WriteString(path)
		paths.WriteByte('\n')
	}
	output, err := gitOutput(ctx, repository.Dir, []byte(paths.String()), "hash-object", "-w", "--stdin-paths")
	if err != nil {
		return fmt.Errorf("batch-write Git blobs: %w", err)
	}
	objectIDs := strings.Fields(string(output))
	if len(objectIDs) != len(pending) {
		return fmt.Errorf("batch-write Git blobs returned %d object IDs for %d blocks", len(objectIDs), len(pending))
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for index, block := range pending {
		key := block.cid.String()
		if existing := repository.staged[key]; existing != "" && existing != objectIDs[index] {
			return fmt.Errorf("CID %s already names a different Git blob", block.cid)
		}
		repository.staged[key] = objectIDs[index]
	}
	return nil
}

type treeEntry struct {
	mode, kind, objectID, name string
}

func (repository *Repository) writeTree(ctx context.Context, entries []treeEntry) (string, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var input strings.Builder
	for _, entry := range entries {
		if entry.name == "" || strings.ContainsAny(entry.name, "/\x00\n\t") {
			return "", fmt.Errorf("invalid carrier tree name %q", entry.name)
		}
		fmt.Fprintf(&input, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.name)
	}
	output, err := gitOutput(ctx, repository.Dir, []byte(input.String()), "mktree")
	if err != nil {
		return "", fmt.Errorf("write carrier tree: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func blockPath(blockCID cid.Cid) string {
	text := blockCID.String()
	return "blocks/" + text[:2] + "/" + text[2:]
}

// ValidateRef accepts only the dedicated GridSpec namespace and delegates the
// remaining Git ref grammar to check-ref-format.
func ValidateRef(ref string) error {
	if !strings.HasPrefix(ref, "refs/gridspec/") || strings.ContainsAny(ref, "\x00\n\r") {
		return fmt.Errorf("ref must be under refs/gridspec/")
	}
	return nil
}

// Fetch imports GridSpec carriers without changing branches or the worktree.
func (repository *Repository) Fetch(ctx context.Context, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	namespace := "refs/gridspec/remotes/r-" + hex.EncodeToString([]byte(remote))
	_, err := gitOutput(ctx, repository.Dir, nil, "fetch", remote,
		"+refs/gridspec/projects/*:"+namespace+"/projects/*",
		"+refs/gridspec/proposals/*:"+namespace+"/proposals/*",
		"+refs/gridspec/candidates/*:"+namespace+"/candidates/*",
	)
	return err
}

// Push exports all GridSpec carrier refs without pushing source branches.
func (repository *Repository) Push(ctx context.Context, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := gitOutput(ctx, repository.Dir, nil, "push", remote,
		"refs/gridspec/projects/*:refs/gridspec/projects/*",
		"refs/gridspec/proposals/*:refs/gridspec/proposals/*",
		"refs/gridspec/candidates/*:refs/gridspec/candidates/*",
	)
	return err
}

// Config gets a repository-local configuration value. Missing keys return an
// empty value.
func (repository *Repository) Config(ctx context.Context, key string) (string, error) {
	output, err := gitOutput(ctx, repository.Dir, nil, "config", "--local", "--get", key)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(output)), nil
}

// SetConfig writes a repository-local configuration value.
func (repository *Repository) SetConfig(ctx context.Context, key, value string) error {
	if key == "" || strings.ContainsAny(key+value, "\x00\n\r") {
		return fmt.Errorf("invalid Git configuration key or value")
	}
	_, err := gitOutput(ctx, repository.Dir, nil, "config", "--local", key, value)
	return err
}

func gitOutput(ctx context.Context, dir string, input []byte, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = dir
	command.Env = os.Environ()
	if len(arguments) != 0 && arguments[0] == "commit-tree" {
		command.Env = append(command.Env,
			"GIT_AUTHOR_NAME=GridSpec Carrier", "GIT_AUTHOR_EMAIL=gridspec@localhost",
			"GIT_COMMITTER_NAME=GridSpec Carrier", "GIT_COMMITTER_EMAIL=gridspec@localhost",
		)
	}
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return nil, fmt.Errorf("git %s: %s: %w", strings.Join(arguments, " "), message, err)
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
	}
	return output, nil
}
