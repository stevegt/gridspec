package artifact

import (
	"context"
	"testing"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
)

func TestBuildAndVerify(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	acceptedData := []byte("accepted commit placeholder")
	accepted, _ := gridwire.GridCID(acceptedData)
	if err := store.Put(ctx, accepted, acceptedData); err != nil {
		t.Fatal(err)
	}
	inputData := []byte("accepted state")
	input, _ := gridwire.RawCID(inputData)
	if err := store.Put(ctx, input, inputData); err != nil {
		t.Fatal(err)
	}
	built, err := Build(ctx, store, BuildOptions{
		AcceptedCommit: accepted, Target: "go", Generator: "static/test",
		Inputs: []cid.Cid{input}, OutputDigest: "sha256:abc",
		VerificationEvidence: []byte("go test ./... passed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(ctx, store, built.CID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Target != "go" || verified.Generator != "static/test" || len(verified.Closure()) != 4 {
		t.Fatalf("artifact = %#v", verified)
	}
}
