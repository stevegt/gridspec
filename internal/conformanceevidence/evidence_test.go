package conformanceevidence

import (
	"context"
	"testing"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
)

func TestBuildAndVerify(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	commitBytes := []byte("commit")
	commit, _ := gridwire.GridCID(commitBytes)
	if err := store.Put(ctx, commit, commitBytes); err != nil {
		t.Fatal(err)
	}
	built, err := Build(ctx, store, BuildOptions{
		AcceptedCommit: commit, ImplementationDigest: "sha256:abc",
		CheckerConfiguration: []byte("config"), EvidenceResults: []byte("results"),
		Exclusions: []byte("[]"), Diagnostics: []byte("[]"), Report: []byte("report"),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(ctx, store, built.CID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.ImplementationDigest != "sha256:abc" || len(verified.Closure()) != 7 {
		t.Fatalf("evidence = %#v", verified)
	}
}
