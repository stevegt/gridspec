package commitv3

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/refset"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/transaction"
)

func TestSignedCompleteSelection(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	base, err := specstate.Build(ctx, store, specstate.Empty("example.test", "default"))
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := transaction.Build(ctx, store, transaction.BuildOptions{Base: base, Program: []byte("gridspec 1\nput graph g { kind = \"algorithm\"; directed = true; status = \"active\" }\n")})
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	agentID, _ := gridwire.RawCID([]byte("agent genesis"))
	commit, err := Build(ctx, store, BuildOptions{
		Heads: map[string]cid.Cid{"spec/example.test": event.CID}, Metadata: map[string]any{"comment": "initial"}, AgentID: agentID, Signer: private,
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(ctx, store, commit.CID, func(got cid.Cid, key ed25519.PublicKey) error {
		if !got.Equals(agentID) {
			t.Fatalf("wrong AgentID: %s", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Heads["spec/example.test"].Equals(event.CID) || len(verified.Closure()) == 0 {
		t.Fatalf("unexpected commit: %#v", verified)
	}
}

func TestCdintGridWireVector(t *testing.T) {
	ctx := context.Background()
	store := cas.NewMemory()
	head, err := gridwire.GridCID([]byte("grid-spec-event-vector"))
	if err != nil {
		t.Fatal(err)
	}
	headValue, _ := gridwire.EncodeCID(head)
	heads, err := refset.Build(ctx, store, map[string][]byte{"spec/example.test": headValue})
	if err != nil {
		t.Fatal(err)
	}
	comment, _ := gridwire.Encode("vector")
	metadata, err := refset.Build(ctx, store, map[string][]byte{"comment": comment})
	if err != nil {
		t.Fatal(err)
	}
	agentID, _ := gridwire.RawCID([]byte("grid-spec-agent-vector"))
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	public := private.Public().(ed25519.PublicKey)
	header := proofHeader{AgentID: gridwire.TagCID(agentID), Algorithm: "Ed25519", PublicKey: public}
	headerBytes, _ := gridwire.Encode(header)
	commit := Commit{AgentID: agentID, PublicKey: public}
	projection, err := encode(commit, heads.Root(), metadata.Root(), headerBytes)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := private.Sign(rand.Reader, projection, crypto.Hash(0))
	if err != nil {
		t.Fatal(err)
	}
	proofBytes, _ := gridwire.Encode(Proof{AgentID: header.AgentID, Algorithm: header.Algorithm, PublicKey: header.PublicKey, Signature: signature})
	complete, err := encode(commit, heads.Root(), metadata.Root(), proofBytes)
	if err != nil {
		t.Fatal(err)
	}
	commitCID, _ := gridwire.GridCID(complete)
	if got, want := heads.Root().String(), "bafyreibps3zkiij4lkwuvntxfoopkdqej2z725262ztoezb22vm55zkssa"; got != want {
		t.Fatalf("heads root %s, want %s", got, want)
	}
	if got, want := metadata.Root().String(), "bafyreiattfb4ciyacqbi674unb45lapbl4ko2d2bew76zi4f67itnihkbm"; got != want {
		t.Fatalf("metadata root %s, want %s", got, want)
	}
	if got, want := commitCID.String(), "bagtsaerawvz4lj2y4rlfj54ndr3dc6ux6pxykwmrjk562cxpm6os3q7dt73q"; got != want {
		t.Fatalf("commit CID %s, want %s", got, want)
	}
	wantBytes := "da6772696485d82a582500015512200716d500f299eeac65341b08d69e8a37f0027e1d6e9d2a771e9547c289883f6280d82a582500017112202f96f2a4213c5aad4ab6772b9cf50e044eb3fd775ed666e2643ad559dee55290d82a58250001711220139943c1230014028f7f946879d581e15f14ed0f4125bfeca385f7d136a0ea0ba4686167656e745f6964d82a582500015512207f12f6b67c0bdca7f32a7069fb935ac22c516200db224b46e91fae1c0108e72d69616c676f726974686d6745643235353139697369676e6174757265584052997167bc5761cf754349d9507380301a52275ba35305b73a504c1076223d87cdf3b685cc703f68ac61530fe9ef61de6846fcef4130310f6d13bfd25e0309016a7075626c69635f6b657958202152f8d19b791d24453242e15f2eab6cb7cffa7b6a5ed30097960e069881db12"
	if got := hex.EncodeToString(complete); got != wantBytes {
		t.Fatalf("commit bytes differ from cdint-grid vector\n got %s\nwant %s", got, wantBytes)
	}
}
