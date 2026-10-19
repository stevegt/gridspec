// Package commitv3 implements the frozen Grid Signed Commit Version 3 wire
// protocol. Git carriers and GridSpec policy are intentionally outside it.
package commitv3

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/refset"
	"github.com/promisegrid/gridspec/internal/transaction"
)

const ProtocolCID = "bafkreiahc3kqb4uz52wgkna3bdlj5crx6abh4hlotuvhohuvi7bitcb7mi"

type Proof struct {
	AgentID   cbor.Tag `cbor:"agent_id"`
	Algorithm string   `cbor:"algorithm"`
	PublicKey []byte   `cbor:"public_key"`
	Signature []byte   `cbor:"signature"`
}

type proofHeader struct {
	AgentID   cbor.Tag `cbor:"agent_id"`
	Algorithm string   `cbor:"algorithm"`
	PublicKey []byte   `cbor:"public_key"`
}

type Verification struct {
	RetrievalError error
	ContentError   error
	SignatureError error
	IdentityError  error
}

type Commit struct {
	CID          cid.Cid
	Predecessors []cid.Cid
	Heads        map[string]cid.Cid
	Metadata     map[string]any
	AgentID      cid.Cid
	PublicKey    ed25519.PublicKey
	Proof        Proof
	Verification Verification
	closure      []cid.Cid
}

func (commit Commit) Closure() []cid.Cid { return append([]cid.Cid(nil), commit.closure...) }

type BuildOptions struct {
	Predecessors []cid.Cid
	Heads        map[string]cid.Cid
	Metadata     map[string]any
	AgentID      cid.Cid
	Signer       crypto.Signer
}

func Build(ctx context.Context, store cas.Store, options BuildOptions) (Commit, error) {
	if store == nil || options.Signer == nil || !options.AgentID.Defined() || len(options.Heads) == 0 {
		return Commit{}, fmt.Errorf("store, signer, AgentID, and nonempty complete heads map are required")
	}
	predecessors, err := canonicalPredecessors(options.Predecessors)
	if err != nil {
		return Commit{}, err
	}
	publicKey, ok := options.Signer.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Commit{}, fmt.Errorf("commit signer must expose Ed25519 public key")
	}
	headValues := make(map[string][]byte, len(options.Heads))
	for name, head := range options.Heads {
		if name == "" || !utf8.ValidString(name) || !head.Defined() || head.Prefix().Codec != gridwire.GridCBORCodec {
			return Commit{}, fmt.Errorf("timeline names and Grid-CBOR heads must be valid")
		}
		encoded, _ := gridwire.EncodeCID(head)
		headValues[name] = encoded
	}
	heads, err := refset.Build(ctx, store, headValues)
	if err != nil {
		return Commit{}, fmt.Errorf("build heads: %w", err)
	}
	metadataValues := make(map[string][]byte, len(options.Metadata))
	for key, value := range options.Metadata {
		if key == "" || !utf8.ValidString(key) {
			return Commit{}, fmt.Errorf("metadata keys must be nonempty UTF-8")
		}
		encoded, err := gridwire.Encode(value)
		if err != nil {
			return Commit{}, fmt.Errorf("encode metadata %q: %w", key, err)
		}
		metadataValues[key] = encoded
	}
	metadata, err := refset.Build(ctx, store, metadataValues)
	if err != nil {
		return Commit{}, fmt.Errorf("build metadata: %w", err)
	}
	commit := Commit{
		Predecessors: predecessors,
		Heads:        cloneHeads(options.Heads),
		Metadata:     cloneMetadata(options.Metadata),
		AgentID:      options.AgentID,
		PublicKey:    bytes.Clone(publicKey),
	}
	header := proofHeader{AgentID: gridwire.TagCID(options.AgentID), Algorithm: "Ed25519", PublicKey: bytes.Clone(publicKey)}
	headerBytes, _ := gridwire.Encode(header)
	projection, err := encode(commit, heads.Root(), metadata.Root(), headerBytes)
	if err != nil {
		return Commit{}, err
	}
	signature, err := options.Signer.Sign(rand.Reader, projection, crypto.Hash(0))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, projection, signature) {
		return Commit{}, fmt.Errorf("sign Grid commit: invalid Ed25519 result")
	}
	proof := Proof{AgentID: header.AgentID, Algorithm: header.Algorithm, PublicKey: header.PublicKey, Signature: bytes.Clone(signature)}
	proofBytes, _ := gridwire.Encode(proof)
	encoded, err := encode(commit, heads.Root(), metadata.Root(), proofBytes)
	if err != nil {
		return Commit{}, err
	}
	commitCID, err := gridwire.GridCID(encoded)
	if err != nil {
		return Commit{}, err
	}
	if err := store.Put(ctx, commitCID, encoded); err != nil {
		return Commit{}, err
	}
	commit.CID, commit.Proof = commitCID, proof
	commit.Verification.SignatureError = nil
	closure := []cid.Cid{commitCID}
	closure = append(closure, heads.Closure()...)
	closure = append(closure, metadata.Closure()...)
	verifier := newVerifier(nil)
	for _, predecessor := range predecessors {
		verified, err := verifier.verify(ctx, store, predecessor)
		if err != nil {
			return Commit{}, fmt.Errorf("verify predecessor %s: %w", predecessor, err)
		}
		closure = append(closure, verified.Closure()...)
	}
	for _, head := range commit.Heads {
		event, err := verifier.transactions.Verify(ctx, store, head)
		if err != nil {
			return Commit{}, fmt.Errorf("verify selected head %s: %w", head, err)
		}
		closure = append(closure, event.Closure()...)
	}
	commit.closure = canonicalCIDs(closure)
	return commit, nil
}

func encode(commit Commit, headsRoot, metadataRoot cid.Cid, proof []byte) ([]byte, error) {
	predecessors := make([]any, len(commit.Predecessors))
	for index, predecessor := range commit.Predecessors {
		predecessors[index] = gridwire.TagCID(predecessor)
	}
	predecessorBytes, _ := gridwire.Encode(predecessors)
	heads, _ := gridwire.EncodeCID(headsRoot)
	metadata, _ := gridwire.EncodeCID(metadataRoot)
	return gridwire.BuildEnvelope(gridwire.ParseProtocolCID(ProtocolCID), predecessorBytes, heads, metadata, proof)
}

// IdentityBinder performs receiver-local AgentID/key trust binding. A nil
// binder leaves IdentityError unset without asserting trust.
type IdentityBinder func(agentID cid.Cid, publicKey ed25519.PublicKey) error

func Verify(ctx context.Context, store cas.Store, commitCID cid.Cid, bind IdentityBinder) (Commit, error) {
	return newVerifier(bind).verify(ctx, store, commitCID)
}

type verifier struct {
	bind         IdentityBinder
	active       map[string]bool
	memo         map[string]Commit
	transactions *transaction.Verifier
}

func newVerifier(bind IdentityBinder) *verifier {
	return &verifier{
		bind: bind, active: make(map[string]bool), memo: make(map[string]Commit),
		transactions: transaction.NewVerifier(),
	}
}

func (verifier *verifier) verify(ctx context.Context, store cas.Store, commitCID cid.Cid) (Commit, error) {
	key := commitCID.String()
	if cached, ok := verifier.memo[key]; ok {
		return cloneCommit(cached), nil
	}
	if verifier.active[key] {
		return Commit{}, fmt.Errorf("commit predecessor cycle at %s", commitCID)
	}
	verifier.active[key] = true
	defer delete(verifier.active, key)
	commit := Commit{CID: commitCID, Heads: make(map[string]cid.Cid), Metadata: make(map[string]any)}
	encoded, err := store.Get(ctx, commitCID)
	if err != nil {
		commit.Verification.RetrievalError = err
		return commit, err
	}
	recomputed, err := gridwire.GridCID(encoded)
	if err != nil || !recomputed.Equals(commitCID) {
		commit.Verification.ContentError = fmt.Errorf("commit bytes do not match CID %s", commitCID)
		return commit, commit.Verification.ContentError
	}
	envelope, err := gridwire.ParseEnvelope(encoded)
	if err != nil || envelope.Protocol.String() != ProtocolCID || len(envelope.Slots) != 4 {
		commit.Verification.ContentError = fmt.Errorf("commit must use exact five-slot Grid Commit v3 protocol")
		return commit, commit.Verification.ContentError
	}
	var predecessorTags []cbor.Tag
	if err := gridwire.Canonical(envelope.Slots[0], &predecessorTags); err != nil {
		return commit, fmt.Errorf("decode predecessors: %w", err)
	}
	for _, tag := range predecessorTags {
		predecessor, err := gridwire.ParseCIDTag(tag)
		if err != nil || predecessor.Prefix().Codec != gridwire.GridCBORCodec {
			return commit, fmt.Errorf("invalid predecessor CID")
		}
		commit.Predecessors = append(commit.Predecessors, predecessor)
	}
	canonical, err := canonicalPredecessors(commit.Predecessors)
	if err != nil || !equalCIDs(canonical, commit.Predecessors) {
		return commit, fmt.Errorf("predecessors are not canonical")
	}
	headsRoot, err := gridwire.DecodeCID(envelope.Slots[1])
	if err != nil {
		return commit, err
	}
	metadataRoot, err := gridwire.DecodeCID(envelope.Slots[2])
	if err != nil {
		return commit, err
	}
	var proof Proof
	if err := gridwire.Canonical(envelope.Slots[3], &proof); err != nil {
		return commit, fmt.Errorf("decode proof: %w", err)
	}
	commit.Proof = proof
	commit.AgentID, err = gridwire.ParseCIDTag(proof.AgentID)
	if err != nil || proof.Algorithm != "Ed25519" || len(proof.PublicKey) != ed25519.PublicKeySize || len(proof.Signature) != ed25519.SignatureSize {
		commit.Verification.SignatureError = fmt.Errorf("commit proof shape is invalid")
		return commit, commit.Verification.SignatureError
	}
	commit.PublicKey = bytes.Clone(proof.PublicKey)
	header, _ := gridwire.Encode(proofHeader{AgentID: proof.AgentID, Algorithm: proof.Algorithm, PublicKey: proof.PublicKey})
	projection, err := encode(commit, headsRoot, metadataRoot, header)
	if err != nil || !ed25519.Verify(proof.PublicKey, projection, proof.Signature) {
		commit.Verification.SignatureError = fmt.Errorf("commit signature is invalid")
		return commit, commit.Verification.SignatureError
	}
	if verifier.bind != nil {
		commit.Verification.IdentityError = verifier.bind(commit.AgentID, commit.PublicKey)
	}
	heads, err := refset.Open(ctx, store, headsRoot)
	if err != nil {
		return commit, fmt.Errorf("open commit heads: %w", err)
	}
	if err := heads.Scan(func(key, value []byte) error {
		if len(key) == 0 {
			return fmt.Errorf("timeline name must be nonempty")
		}
		head, err := gridwire.DecodeCID(value)
		if err != nil || head.Prefix().Codec != gridwire.GridCBORCodec {
			return fmt.Errorf("timeline head must be Grid-CBOR CID")
		}
		commit.Heads[string(key)] = head
		return nil
	}); err != nil {
		return commit, err
	}
	if len(commit.Heads) == 0 {
		return commit, fmt.Errorf("commit heads map is empty")
	}
	metadata, err := refset.Open(ctx, store, metadataRoot)
	if err != nil {
		return commit, fmt.Errorf("open commit metadata: %w", err)
	}
	if err := metadata.Scan(func(key, value []byte) error {
		if len(key) == 0 || !utf8.Valid(key) {
			return fmt.Errorf("metadata key must be nonempty UTF-8")
		}
		var decoded any
		if err := gridwire.Canonical(value, &decoded); err != nil {
			return err
		}
		commit.Metadata[string(key)] = decoded
		return nil
	}); err != nil {
		return commit, err
	}
	closure := []cid.Cid{commitCID}
	closure = append(closure, heads.Closure()...)
	closure = append(closure, metadata.Closure()...)
	for _, predecessor := range commit.Predecessors {
		verified, err := verifier.verify(ctx, store, predecessor)
		if err != nil {
			return commit, err
		}
		closure = append(closure, verified.Closure()...)
	}
	for _, head := range commit.Heads {
		event, err := verifier.transactions.Verify(ctx, store, head)
		if err != nil {
			return commit, fmt.Errorf("verify selected event %s: %w", head, err)
		}
		closure = append(closure, event.Closure()...)
	}
	commit.closure = canonicalCIDs(closure)
	verifier.memo[key] = cloneCommit(commit)
	return cloneCommit(commit), nil
}

func cloneCommit(commit Commit) Commit {
	result := commit
	result.Predecessors = append([]cid.Cid(nil), commit.Predecessors...)
	result.Heads = cloneHeads(commit.Heads)
	result.Metadata = cloneMetadata(commit.Metadata)
	result.PublicKey = bytes.Clone(commit.PublicKey)
	result.Proof.PublicKey = bytes.Clone(commit.Proof.PublicKey)
	result.Proof.Signature = bytes.Clone(commit.Proof.Signature)
	result.closure = append([]cid.Cid(nil), commit.closure...)
	return result
}

func canonicalPredecessors(values []cid.Cid) ([]cid.Cid, error) {
	result := append([]cid.Cid(nil), values...)
	seen := make(map[string]bool, len(result))
	for _, value := range result {
		if !value.Defined() || value.Prefix().Codec != gridwire.GridCBORCodec || seen[value.String()] {
			return nil, fmt.Errorf("predecessors must be unique Grid-CBOR CIDs")
		}
		seen[value.String()] = true
	}
	if len(result) > 2 {
		sort.Slice(result[1:], func(i, j int) bool {
			return bytes.Compare(result[i+1].Bytes(), result[j+1].Bytes()) < 0
		})
	}
	return result, nil
}

func canonicalCIDs(values []cid.Cid) []cid.Cid {
	byText := make(map[string]cid.Cid, len(values))
	for _, value := range values {
		if value.Defined() {
			byText[value.String()] = value
		}
	}
	result := make([]cid.Cid, 0, len(byText))
	for _, value := range byText {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return bytes.Compare(result[i].Bytes(), result[j].Bytes()) < 0 })
	return result
}

func equalCIDs(left, right []cid.Cid) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !left[index].Equals(right[index]) {
			return false
		}
	}
	return true
}

func cloneHeads(input map[string]cid.Cid) map[string]cid.Cid {
	result := make(map[string]cid.Cid, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneMetadata(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
