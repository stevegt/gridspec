package gitstore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/commitv3"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/transaction"
)

func TestPublishAndReloadCompleteSignedClosure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.name", "GridSpec Test")
	git(t, dir, "config", "user.email", "gridspec@example.invalid")
	repository, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := specstate.Build(ctx, repository, specstate.Empty("example.test/system", "default"))
	if err != nil {
		t.Fatal(err)
	}
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	agentID, _ := gridwire.RawCID(signer.Public().(ed25519.PublicKey))
	event, _, err := transaction.Build(ctx, repository, transaction.BuildOptions{
		Base: base, Program: []byte("gridspec 1\nmodule \"example.test/system\"\nprofile \"default\"\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := commitv3.Build(ctx, repository, commitv3.BuildOptions{
		Heads: map[string]cid.Cid{"main": event.CID}, AgentID: agentID, Signer: signer,
	})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "refs/gridspec/projects/example.test/system/agent"
	carrier, err := repository.Publish(ctx, commit.CID, commit.Closure(), ref, "", "test carrier")
	if err != nil {
		t.Fatal(err)
	}
	if carrier == "" {
		t.Fatal("empty carrier ID")
	}

	reloaded, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	root, loadedCarrier, err := reloaded.AddCarrier(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if loadedCarrier != carrier || !root.Equals(commit.CID) {
		t.Fatalf("loaded carrier=%s root=%s", loadedCarrier, root)
	}
	verified, err := commitv3.Verify(ctx, reloaded, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Heads["main"].Equals(event.CID) || verified.Verification.SignatureError != nil {
		t.Fatalf("bad verified commit: %#v", verified)
	}
}

func TestPublishUsesCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.name", "GridSpec Test")
	git(t, dir, "config", "user.email", "gridspec@example.invalid")
	repository, _ := Open(ctx, dir)
	data := []byte("block")
	root, _ := gridwire.RawCID(data)
	if err := repository.Put(ctx, root, data); err != nil {
		t.Fatal(err)
	}
	const ref = "refs/gridspec/proposals/demo/one"
	first, err := repository.Publish(ctx, root, []cid.Cid{root}, ref, "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Publish(ctx, root, []cid.Cid{root}, ref, "", "stale"); err == nil {
		t.Fatal("stale create unexpectedly replaced an existing ref")
	}
	if second, err := repository.Publish(ctx, root, []cid.Cid{root}, ref, first, "second"); err != nil || second == first {
		t.Fatalf("CAS update: second=%s err=%v", second, err)
	}
}

func TestFetchAndPushOnlyGridSpecRefs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	destinationDir := filepath.Join(root, "destination")
	remoteDir := filepath.Join(root, "remote.git")
	for _, dir := range []string{sourceDir, destinationDir, remoteDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, sourceDir, "init", "-q")
	git(t, destinationDir, "init", "-q")
	git(t, remoteDir, "init", "--bare", "-q")
	source, _ := Open(ctx, sourceDir)
	data := []byte("transported GridSpec block")
	rootCID, _ := gridwire.RawCID(data)
	if err := source.Put(ctx, rootCID, data); err != nil {
		t.Fatal(err)
	}
	const ref = "refs/gridspec/proposals/demo/transport"
	if _, err := source.Publish(ctx, rootCID, []cid.Cid{rootCID}, ref, "", "transport"); err != nil {
		t.Fatal(err)
	}
	git(t, sourceDir, "remote", "add", "origin", remoteDir)
	if err := source.Push(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	git(t, destinationDir, "remote", "add", "origin", remoteDir)
	destination, _ := Open(ctx, destinationDir)
	if err := destination.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	remoteRef := "refs/gridspec/remotes/r-6f726967696e/proposals/demo/transport"
	loadedRoot, _, err := destination.AddCarrier(ctx, remoteRef)
	if err != nil || !loadedRoot.Equals(rootCID) {
		t.Fatalf("root=%s err=%v", loadedRoot, err)
	}
	got, err := destination.Get(ctx, rootCID)
	if err != nil || string(got) != string(data) {
		t.Fatalf("data=%q err=%v", got, err)
	}
}

func git(t *testing.T, dir string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", arguments, output, err)
	}
}
