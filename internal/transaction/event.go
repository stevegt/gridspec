// Package transaction binds exact GridSpec programs to deterministic state
// transitions. Unsigned proposals and authored events have distinct pCIDs.
package transaction

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sort"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/txnlang"
)

const (
	LanguageProtocolCID = "bafkreiag4as5cd5lmd2zjv3vocbixczrbrrzqwa5uxbg6w47w4nhqtdriq"
	UnsignedProtocolCID = "bafkreibr7qlhtebidocxgsrktev34r2i3yw2ug656a7gtg2upqfpmde2tm"
	AuthoredProtocolCID = "bafkreic7upqan2eqeclsuarh3yumxs55kkv5yrdy3eduxgase75ezxl7c4"
)

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

type Event struct {
	CID            cid.Cid
	Protocol       cid.Cid
	Previous       cid.Cid
	Base           cid.Cid
	Language       cid.Cid
	Program        cid.Cid
	Result         cid.Cid
	Required       []cid.Cid
	Proof          *Proof
	Signer         cid.Cid
	SignatureValid bool
	closure        []cid.Cid
}

func (event Event) Closure() []cid.Cid { return append([]cid.Cid(nil), event.closure...) }

type BuildOptions struct {
	Previous cid.Cid
	Base     specstate.State
	Program  []byte
	Required []cid.Cid
	AgentID  cid.Cid
	Signer   crypto.Signer
}

// Build parses and atomically replays a program, then stores either an unsigned
// proposal event or an authored event when Signer is supplied.
func Build(ctx context.Context, store cas.Store, options BuildOptions) (Event, specstate.State, error) {
	if store == nil || !options.Base.CID.Defined() || len(options.Program) == 0 {
		return Event{}, specstate.State{}, fmt.Errorf("store, materialized base state, and program are required")
	}
	parsed, err := txnlang.Parse(options.Program)
	if err != nil {
		return Event{}, specstate.State{}, err
	}
	result, err := specstate.Apply(options.Base, parsed)
	if err != nil {
		return Event{}, specstate.State{}, err
	}
	result, err = specstate.Build(ctx, store, result)
	if err != nil {
		return Event{}, specstate.State{}, err
	}
	programCID, err := gridwire.RawCID(options.Program)
	if err != nil {
		return Event{}, specstate.State{}, err
	}
	if err := store.Put(ctx, programCID, options.Program); err != nil {
		return Event{}, specstate.State{}, err
	}
	required := append([]cid.Cid{programCID, result.CID}, options.Required...)
	required = canonicalCIDs(required)
	event := Event{
		Previous: options.Previous,
		Base:     options.Base.CID,
		Language: gridwire.ParseProtocolCID(LanguageProtocolCID),
		Program:  programCID,
		Result:   result.CID,
		Required: required,
	}
	var encoded []byte
	if options.Signer == nil {
		event.Protocol = gridwire.ParseProtocolCID(UnsignedProtocolCID)
		encoded, err = encodeEvent(event, nil)
	} else {
		if !options.AgentID.Defined() {
			return Event{}, specstate.State{}, fmt.Errorf("authored event requires AgentID")
		}
		event.Protocol = gridwire.ParseProtocolCID(AuthoredProtocolCID)
		publicKey, ok := options.Signer.Public().(ed25519.PublicKey)
		if !ok || len(publicKey) != ed25519.PublicKeySize {
			return Event{}, specstate.State{}, fmt.Errorf("authored event requires Ed25519 signer")
		}
		header := proofHeader{AgentID: gridwire.TagCID(options.AgentID), Algorithm: "Ed25519", PublicKey: bytes.Clone(publicKey)}
		headerBytes, _ := gridwire.Encode(header)
		projection, err := encodeEvent(event, headerBytes)
		if err != nil {
			return Event{}, specstate.State{}, err
		}
		signature, err := options.Signer.Sign(rand.Reader, projection, crypto.Hash(0))
		if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, projection, signature) {
			return Event{}, specstate.State{}, fmt.Errorf("sign authored event: invalid Ed25519 result")
		}
		proof := Proof{AgentID: header.AgentID, Algorithm: header.Algorithm, PublicKey: header.PublicKey, Signature: bytes.Clone(signature)}
		proofBytes, _ := gridwire.Encode(proof)
		encoded, err = encodeEvent(event, proofBytes)
		event.Proof, event.Signer, event.SignatureValid = &proof, options.AgentID, true
	}
	if err != nil {
		return Event{}, specstate.State{}, err
	}
	eventCID, err := gridwire.GridCID(encoded)
	if err != nil {
		return Event{}, specstate.State{}, err
	}
	if err := store.Put(ctx, eventCID, encoded); err != nil {
		return Event{}, specstate.State{}, err
	}
	event.CID = eventCID
	closure := []cid.Cid{eventCID, options.Base.CID, programCID}
	closure = append(closure, options.Base.Closure()...)
	closure = append(closure, result.Closure()...)
	closure = append(closure, required...)
	if options.Previous.Defined() {
		previous, err := Verify(ctx, store, options.Previous)
		if err != nil {
			return Event{}, specstate.State{}, fmt.Errorf("verify previous event: %w", err)
		}
		closure = append(closure, previous.Closure()...)
	}
	event.closure = canonicalCIDs(closure)
	return event, result, nil
}

func encodeEvent(event Event, proof []byte) ([]byte, error) {
	previous, _ := gridwire.Encode(nil)
	if event.Previous.Defined() {
		previous, _ = gridwire.EncodeCID(event.Previous)
	}
	base, _ := gridwire.EncodeCID(event.Base)
	language, _ := gridwire.EncodeCID(event.Language)
	program, _ := gridwire.EncodeCID(event.Program)
	result, _ := gridwire.EncodeCID(event.Result)
	requiredValues := make([]any, len(event.Required))
	for index, value := range event.Required {
		requiredValues[index] = gridwire.TagCID(value)
	}
	required, _ := gridwire.Encode(requiredValues)
	slots := [][]byte{previous, base, language, program, result, required}
	if proof != nil {
		slots = append(slots, proof)
	}
	return gridwire.BuildEnvelope(event.Protocol, slots...)
}

// Verify validates content, optional authorship proof, complete state closure,
// and deterministic replay. Identity trust remains a caller-local decision.
func Verify(ctx context.Context, store cas.Store, eventCID cid.Cid) (Event, error) {
	return NewVerifier().Verify(ctx, store, eventCID)
}

// Verifier memoizes fully checked events for one larger verification operation.
// It is intentionally not safe for concurrent use.
type Verifier struct {
	active map[string]bool
	memo   map[string]Event
}

func NewVerifier() *Verifier {
	return &Verifier{active: make(map[string]bool), memo: make(map[string]Event)}
}

func (verifier *Verifier) Verify(ctx context.Context, store cas.Store, eventCID cid.Cid) (Event, error) {
	if verifier == nil {
		verifier = NewVerifier()
	}
	if verifier.active == nil {
		verifier.active = make(map[string]bool)
	}
	if verifier.memo == nil {
		verifier.memo = make(map[string]Event)
	}
	return verifier.verify(ctx, store, eventCID)
}

func (verifier *Verifier) verify(ctx context.Context, store cas.Store, eventCID cid.Cid) (Event, error) {
	key := eventCID.String()
	if cached, ok := verifier.memo[key]; ok {
		return cloneEvent(cached), nil
	}
	if verifier.active[key] {
		return Event{}, fmt.Errorf("transaction event cycle at %s", eventCID)
	}
	verifier.active[key] = true
	defer delete(verifier.active, key)
	data, err := store.Get(ctx, eventCID)
	if err != nil {
		return Event{}, err
	}
	recomputed, err := gridwire.GridCID(data)
	if err != nil || !recomputed.Equals(eventCID) {
		return Event{}, fmt.Errorf("event bytes do not match CID %s", eventCID)
	}
	envelope, err := gridwire.ParseEnvelope(data)
	if err != nil {
		return Event{}, err
	}
	event := Event{CID: eventCID, Protocol: envelope.Protocol}
	authored := envelope.Protocol.String() == AuthoredProtocolCID
	if envelope.Protocol.String() != UnsignedProtocolCID && !authored {
		return Event{}, fmt.Errorf("unsupported transaction-event pCID %s", envelope.Protocol)
	}
	wantSlots := 6
	if authored {
		wantSlots = 7
	}
	if len(envelope.Slots) != wantSlots {
		return Event{}, fmt.Errorf("transaction event has %d slots; want %d", len(envelope.Slots), wantSlots)
	}
	if !bytes.Equal(envelope.Slots[0], []byte{0xf6}) {
		event.Previous, err = gridwire.DecodeCID(envelope.Slots[0])
		if err != nil {
			return Event{}, err
		}
	}
	if event.Base, err = gridwire.DecodeCID(envelope.Slots[1]); err != nil {
		return Event{}, err
	}
	if event.Language, err = gridwire.DecodeCID(envelope.Slots[2]); err != nil || event.Language.String() != LanguageProtocolCID {
		return Event{}, fmt.Errorf("unsupported transaction language")
	}
	if event.Program, err = gridwire.DecodeCID(envelope.Slots[3]); err != nil || event.Program.Prefix().Codec != gridwire.RawCodec {
		return Event{}, fmt.Errorf("invalid transaction program CID")
	}
	if event.Result, err = gridwire.DecodeCID(envelope.Slots[4]); err != nil {
		return Event{}, err
	}
	var requiredTags []cbor.Tag
	if err := gridwire.Canonical(envelope.Slots[5], &requiredTags); err != nil {
		return Event{}, fmt.Errorf("decode required links: %w", err)
	}
	for _, tag := range requiredTags {
		value, err := gridwire.ParseCIDTag(tag)
		if err != nil {
			return Event{}, err
		}
		event.Required = append(event.Required, value)
	}
	if !equalCIDs(event.Required, canonicalCIDs(event.Required)) {
		return Event{}, fmt.Errorf("required links must be unique and binary-CID sorted")
	}
	if !containsCID(event.Required, event.Program) || !containsCID(event.Required, event.Result) {
		return Event{}, fmt.Errorf("required links must include program and result state")
	}
	for _, required := range event.Required {
		data, err := store.Get(ctx, required)
		if err != nil {
			return Event{}, fmt.Errorf("required block %s: %w", required, err)
		}
		recomputed, err := required.Prefix().Sum(data)
		if err != nil || !recomputed.Equals(required) {
			return Event{}, fmt.Errorf("required block %s is corrupt", required)
		}
	}
	if authored {
		var proof Proof
		if err := gridwire.Canonical(envelope.Slots[6], &proof); err != nil {
			return Event{}, fmt.Errorf("decode authored proof: %w", err)
		}
		signer, err := gridwire.ParseCIDTag(proof.AgentID)
		if err != nil || proof.Algorithm != "Ed25519" || len(proof.PublicKey) != ed25519.PublicKeySize || len(proof.Signature) != ed25519.SignatureSize {
			return Event{}, fmt.Errorf("invalid authored proof shape")
		}
		header, _ := gridwire.Encode(proofHeader{AgentID: proof.AgentID, Algorithm: proof.Algorithm, PublicKey: proof.PublicKey})
		projection, err := encodeEvent(event, header)
		if err != nil || !ed25519.Verify(proof.PublicKey, projection, proof.Signature) {
			return Event{}, fmt.Errorf("authored event signature is invalid")
		}
		event.Proof, event.Signer, event.SignatureValid = &proof, signer, true
	}
	base, err := specstate.Load(ctx, store, event.Base)
	if err != nil {
		return Event{}, fmt.Errorf("load base state: %w", err)
	}
	result, err := specstate.Load(ctx, store, event.Result)
	if err != nil {
		return Event{}, fmt.Errorf("load result state: %w", err)
	}
	program, err := store.Get(ctx, event.Program)
	if err != nil {
		return Event{}, err
	}
	parsed, err := txnlang.Parse(program)
	if err != nil {
		return Event{}, err
	}
	replayed, err := specstate.Apply(base, parsed)
	if err != nil {
		return Event{}, fmt.Errorf("replay transaction: %w", err)
	}
	// Replay is validation, not publication. Derived blocks go to an ephemeral
	// overlay so a read-only Git carrier can be verified safely.
	replayed, err = specstate.Build(ctx, cas.NewOverlay(store), replayed)
	if err != nil {
		return Event{}, fmt.Errorf("materialize replayed transaction result: %w", err)
	}
	if !replayed.CID.Equals(result.CID) {
		return Event{}, fmt.Errorf("transaction result %s does not match replay %s", result.CID, replayed.CID)
	}
	closure := []cid.Cid{event.CID, event.Program}
	closure = append(closure, base.Closure()...)
	closure = append(closure, result.Closure()...)
	closure = append(closure, event.Required...)
	if event.Previous.Defined() {
		previous, err := verifier.verify(ctx, store, event.Previous)
		if err != nil {
			return Event{}, err
		}
		closure = append(closure, previous.Closure()...)
	}
	event.closure = canonicalCIDs(closure)
	verifier.memo[key] = cloneEvent(event)
	return cloneEvent(event), nil
}

func cloneEvent(event Event) Event {
	result := event
	result.Required = append([]cid.Cid(nil), event.Required...)
	result.closure = append([]cid.Cid(nil), event.closure...)
	if event.Proof != nil {
		proof := *event.Proof
		proof.PublicKey = bytes.Clone(event.Proof.PublicKey)
		proof.Signature = bytes.Clone(event.Proof.Signature)
		result.Proof = &proof
	}
	return result
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

func containsCID(values []cid.Cid, wanted cid.Cid) bool {
	for _, value := range values {
		if value.Equals(wanted) {
			return true
		}
	}
	return false
}
