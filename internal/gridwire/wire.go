// Package gridwire implements the deterministic Grid-CBOR framing used by
// GridSpec protocols. Protocol meaning remains in the calling package.
package gridwire

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"reflect"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

const (
	GridTag       = uint64(0x67726964)
	CIDTag        = uint64(42)
	GridCBORCodec = uint64(0x1027)
	DAGCBORCodec  = uint64(0x71)
	RawCodec      = uint64(0x55)
)

var (
	enc = mustEncoding()
	dec = mustDecoding()
)

func mustEncoding() cbor.EncMode {
	mode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return mode
}

func mustDecoding() cbor.DecMode {
	mode, err := (cbor.DecOptions{
		DupMapKey:         cbor.DupMapKeyEnforcedAPF,
		IndefLength:       cbor.IndefLengthForbidden,
		ExtraReturnErrors: cbor.ExtraDecErrorUnknownField,
		MaxArrayElements:  1_000_000,
		MaxMapPairs:       1_000_000,
		MaxNestedLevels:   64,
		DefaultMapType:    reflect.TypeOf(map[string]any{}),
	}).DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}

func Encode(value any) ([]byte, error) { return enc.Marshal(value) }

func Decode(data []byte, destination any) error { return dec.Unmarshal(data, destination) }

func Canonical(data []byte, destination any) error {
	if err := dec.Unmarshal(data, destination); err != nil {
		return err
	}
	reencoded, err := enc.Marshal(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(reencoded, data) {
		return fmt.Errorf("CBOR is not deterministic")
	}
	return nil
}

func RawCID(data []byte) (cid.Cid, error)     { return sum(RawCodec, data) }
func GridCID(data []byte) (cid.Cid, error)    { return sum(GridCBORCodec, data) }
func DAGCBORCID(data []byte) (cid.Cid, error) { return sum(DAGCBORCodec, data) }

func sum(codec uint64, data []byte) (cid.Cid, error) {
	return (cid.Prefix{Version: 1, Codec: codec, MhType: multihash.SHA2_256, MhLength: sha256.Size}).Sum(data)
}

func TagCID(value cid.Cid) cbor.Tag {
	return cbor.Tag{Number: CIDTag, Content: append([]byte{0}, value.Bytes()...)}
}

func ParseCIDTag(tag cbor.Tag) (cid.Cid, error) {
	encoded, ok := tag.Content.([]byte)
	if tag.Number != CIDTag || !ok || len(encoded) < 2 || encoded[0] != 0 {
		return cid.Undef, fmt.Errorf("invalid tag-42 CID")
	}
	parsed, err := cid.Cast(encoded[1:])
	if err != nil || parsed.Version() != 1 || !bytes.Equal(parsed.Bytes(), encoded[1:]) {
		return cid.Undef, fmt.Errorf("invalid canonical CID")
	}
	return parsed, nil
}

func EncodeCID(value cid.Cid) ([]byte, error) { return enc.Marshal(TagCID(value)) }

func DecodeCID(data []byte) (cid.Cid, error) {
	var tag cbor.Tag
	if err := Canonical(data, &tag); err != nil {
		return cid.Undef, err
	}
	return ParseCIDTag(tag)
}

// BuildEnvelope creates a deterministic grid([pCID, ...slots]) value. Every
// slot must contain exactly one already-canonical CBOR item.
func BuildEnvelope(protocol cid.Cid, slots ...[]byte) ([]byte, error) {
	if !protocol.Defined() || protocol.Version() != 1 {
		return nil, fmt.Errorf("defined CIDv1 protocol is required")
	}
	protocolBytes, err := enc.Marshal(TagCID(protocol))
	if err != nil {
		return nil, err
	}
	items := make([]cbor.RawMessage, len(slots)+1)
	items[0] = protocolBytes
	for index, slot := range slots {
		var value any
		if err := Canonical(slot, &value); err != nil {
			return nil, fmt.Errorf("slot %d: %w", index+1, err)
		}
		items[index+1] = bytes.Clone(slot)
	}
	array, err := enc.Marshal(items)
	if err != nil {
		return nil, err
	}
	return (cbor.RawTag{Number: GridTag, Content: array}).MarshalCBOR()
}

type Envelope struct {
	Protocol cid.Cid
	Slots    [][]byte
}

func ParseEnvelope(data []byte) (Envelope, error) {
	var rawTag cbor.RawTag
	if err := dec.Unmarshal(data, &rawTag); err != nil {
		return Envelope{}, fmt.Errorf("decode Grid tag: %w", err)
	}
	if rawTag.Number != GridTag {
		return Envelope{}, fmt.Errorf("expected Grid tag %d", GridTag)
	}
	var items []cbor.RawMessage
	if err := dec.Unmarshal(rawTag.Content, &items); err != nil || len(items) == 0 {
		return Envelope{}, fmt.Errorf("Grid content must be a nonempty array")
	}
	protocol, err := DecodeCID(items[0])
	if err != nil {
		return Envelope{}, fmt.Errorf("decode pCID: %w", err)
	}
	result := Envelope{Protocol: protocol, Slots: make([][]byte, len(items)-1)}
	for index, item := range items[1:] {
		var value any
		if err := Canonical(item, &value); err != nil {
			return Envelope{}, fmt.Errorf("slot %d: %w", index+1, err)
		}
		result.Slots[index] = bytes.Clone(item)
	}
	rebuilt, err := BuildEnvelope(result.Protocol, result.Slots...)
	if err != nil || !bytes.Equal(rebuilt, data) {
		return Envelope{}, fmt.Errorf("Grid envelope is not deterministic")
	}
	return result, nil
}

func ParseProtocolCID(text string) cid.Cid {
	parsed, err := cid.Parse(text)
	if err != nil {
		panic(err)
	}
	return parsed
}
