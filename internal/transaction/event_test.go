package transaction

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/specstate"
)

func TestUnsignedAndAuthoredEventsReplay(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	base, err := specstate.Build(ctx, store, specstate.Empty("example.test", "default"))
	if err != nil {
		t.Fatal(err)
	}
	program := []byte("gridspec 1\nput graph g { kind = \"algorithm\"; status = \"active\"; directed = true }\n")
	unsigned, result, err := Build(ctx, store, BuildOptions{Base: base, Program: program})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(ctx, store, unsigned.CID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Proof != nil || !verified.Result.Equals(result.CID) {
		t.Fatalf("unexpected unsigned verification: %#v", verified)
	}
	if _, err := Verify(ctx, readOnlyStore{Store: store}, unsigned.CID); err != nil {
		t.Fatalf("read-only verification attempted publication: %v", err)
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	agentID, _ := gridwire.RawCID([]byte("test agent"))
	authored, _, err := Build(ctx, store, BuildOptions{Previous: unsigned.CID, Base: result, Program: []byte("gridspec 1\nassert exists graph g\n"), AgentID: agentID, Signer: private})
	if err != nil {
		t.Fatal(err)
	}
	verified, err = Verify(ctx, store, authored.CID)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.SignatureValid || !verified.Signer.Equals(agentID) {
		t.Fatalf("authored proof not verified: %#v", verified)
	}
}

func TestVerifierMemoizesCheckedEvents(t *testing.T) {
	ctx := context.Background()
	memory := cas.NewMemory()
	base, err := specstate.Build(ctx, memory, specstate.Empty("example.test", "default"))
	if err != nil {
		t.Fatal(err)
	}
	first, state, err := Build(ctx, memory, BuildOptions{Base: base, Program: []byte("gridspec 1\nput graph g { kind = \"descriptive\"; status = \"active\" }\n")})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Build(ctx, memory, BuildOptions{Previous: first.CID, Base: state, Program: []byte("gridspec 1\nassert exists graph g\n")})
	if err != nil {
		t.Fatal(err)
	}
	store := &countingStore{Store: memory}
	verifier := NewVerifier()
	verified, err := verifier.Verify(ctx, store, second.CID)
	if err != nil {
		t.Fatal(err)
	}
	reads := store.gets
	verified.Required = nil
	again, err := verifier.Verify(ctx, store, second.CID)
	if err != nil {
		t.Fatal(err)
	}
	if store.gets != reads {
		t.Fatalf("memoized verification performed %d additional reads", store.gets-reads)
	}
	if len(again.Required) == 0 {
		t.Fatal("caller mutation changed cached verification")
	}
}

type readOnlyStore struct{ cas.Store }

func (readOnlyStore) Put(context.Context, cid.Cid, []byte) error {
	return errors.New("read-only store")
}

type countingStore struct {
	cas.Store
	gets int
}

func (store *countingStore) Get(ctx context.Context, blockCID cid.Cid) ([]byte, error) {
	store.gets++
	return store.Store.Get(ctx, blockCID)
}
