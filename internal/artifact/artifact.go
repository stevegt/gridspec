// Package artifact implements unsigned, deterministic derived-artifact
// messages. Artifacts describe compiler output and never select spec state.
package artifact

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
)

const ProtocolCID = "bafkreiasth6ndvmh3kfwiovo443uegjtti7tqfvxbdjzh4lgy5vqk3ltjy"

type Artifact struct {
	CID                  cid.Cid
	AcceptedCommit       cid.Cid
	Target               string
	Generator            string
	Inputs               []cid.Cid
	OutputDigest         string
	VerificationEvidence cid.Cid
	closure              []cid.Cid
}

func (artifact Artifact) Closure() []cid.Cid { return append([]cid.Cid(nil), artifact.closure...) }

type BuildOptions struct {
	AcceptedCommit       cid.Cid
	Target               string
	Generator            string
	Inputs               []cid.Cid
	OutputDigest         string
	VerificationEvidence []byte
}

func Build(ctx context.Context, store cas.Store, options BuildOptions) (Artifact, error) {
	if store == nil || !options.AcceptedCommit.Defined() || options.AcceptedCommit.Prefix().Codec != gridwire.GridCBORCodec || options.Target == "" || options.Generator == "" || options.OutputDigest == "" || len(options.VerificationEvidence) == 0 {
		return Artifact{}, fmt.Errorf("accepted commit, target, generator, output digest, and verification evidence are required")
	}
	inputs, err := canonicalInputs(options.Inputs)
	if err != nil {
		return Artifact{}, err
	}
	evidenceCID, err := gridwire.RawCID(options.VerificationEvidence)
	if err != nil {
		return Artifact{}, err
	}
	if err := store.Put(ctx, evidenceCID, options.VerificationEvidence); err != nil {
		return Artifact{}, err
	}
	value := Artifact{
		AcceptedCommit: options.AcceptedCommit, Target: options.Target,
		Generator: options.Generator, Inputs: inputs, OutputDigest: options.OutputDigest,
		VerificationEvidence: evidenceCID,
	}
	encoded, err := encode(value)
	if err != nil {
		return Artifact{}, err
	}
	value.CID, err = gridwire.GridCID(encoded)
	if err != nil {
		return Artifact{}, err
	}
	if err := store.Put(ctx, value.CID, encoded); err != nil {
		return Artifact{}, err
	}
	value.closure = append([]cid.Cid{value.CID, options.AcceptedCommit, evidenceCID}, inputs...)
	value.closure = uniqueSorted(value.closure)
	return value, nil
}

func Verify(ctx context.Context, store cas.Store, artifactCID cid.Cid) (Artifact, error) {
	data, err := store.Get(ctx, artifactCID)
	if err != nil {
		return Artifact{}, err
	}
	recomputed, err := gridwire.GridCID(data)
	if err != nil || !recomputed.Equals(artifactCID) {
		return Artifact{}, fmt.Errorf("artifact bytes do not match CID %s", artifactCID)
	}
	envelope, err := gridwire.ParseEnvelope(data)
	if err != nil || envelope.Protocol.String() != ProtocolCID || len(envelope.Slots) != 6 {
		return Artifact{}, fmt.Errorf("artifact must use the closed GridSpec artifact v1 protocol")
	}
	value := Artifact{CID: artifactCID}
	if value.AcceptedCommit, err = gridwire.DecodeCID(envelope.Slots[0]); err != nil || value.AcceptedCommit.Prefix().Codec != gridwire.GridCBORCodec {
		return Artifact{}, fmt.Errorf("invalid accepted commit link")
	}
	if err := gridwire.Canonical(envelope.Slots[1], &value.Target); err != nil || value.Target == "" {
		return Artifact{}, fmt.Errorf("invalid artifact target")
	}
	if err := gridwire.Canonical(envelope.Slots[2], &value.Generator); err != nil || value.Generator == "" {
		return Artifact{}, fmt.Errorf("invalid artifact generator")
	}
	var tags []cbor.Tag
	if err := gridwire.Canonical(envelope.Slots[3], &tags); err != nil {
		return Artifact{}, err
	}
	for _, tag := range tags {
		input, err := gridwire.ParseCIDTag(tag)
		if err != nil {
			return Artifact{}, err
		}
		value.Inputs = append(value.Inputs, input)
	}
	canonical, err := canonicalInputs(value.Inputs)
	if err != nil || !equalCIDs(canonical, value.Inputs) {
		return Artifact{}, fmt.Errorf("artifact inputs are not canonical")
	}
	if err := gridwire.Canonical(envelope.Slots[4], &value.OutputDigest); err != nil || value.OutputDigest == "" {
		return Artifact{}, fmt.Errorf("invalid output digest")
	}
	if value.VerificationEvidence, err = gridwire.DecodeCID(envelope.Slots[5]); err != nil {
		return Artifact{}, fmt.Errorf("invalid verification evidence link")
	}
	closure := []cid.Cid{value.CID, value.AcceptedCommit, value.VerificationEvidence}
	closure = append(closure, value.Inputs...)
	for _, linked := range closure[1:] {
		data, err := store.Get(ctx, linked)
		if err != nil {
			return Artifact{}, fmt.Errorf("artifact closure: %w", err)
		}
		actual, err := linked.Prefix().Sum(data)
		if err != nil || !actual.Equals(linked) {
			return Artifact{}, fmt.Errorf("artifact closure block %s is corrupt", linked)
		}
	}
	value.closure = uniqueSorted(closure)
	return value, nil
}

func encode(value Artifact) ([]byte, error) {
	accepted, _ := gridwire.EncodeCID(value.AcceptedCommit)
	target, _ := gridwire.Encode(value.Target)
	generator, _ := gridwire.Encode(value.Generator)
	inputs := make([]any, len(value.Inputs))
	for index, input := range value.Inputs {
		inputs[index] = gridwire.TagCID(input)
	}
	inputBytes, _ := gridwire.Encode(inputs)
	digest, _ := gridwire.Encode(value.OutputDigest)
	evidence, _ := gridwire.EncodeCID(value.VerificationEvidence)
	return gridwire.BuildEnvelope(gridwire.ParseProtocolCID(ProtocolCID), accepted, target, generator, inputBytes, digest, evidence)
}

func canonicalInputs(values []cid.Cid) ([]cid.Cid, error) {
	result := append([]cid.Cid(nil), values...)
	seen := make(map[string]bool, len(result))
	for _, value := range result {
		if !value.Defined() || value.Version() != 1 || seen[value.String()] {
			return nil, fmt.Errorf("artifact inputs must be unique CIDv1 values")
		}
		seen[value.String()] = true
	}
	sort.Slice(result, func(i, j int) bool { return bytes.Compare(result[i].Bytes(), result[j].Bytes()) < 0 })
	return result, nil
}

func uniqueSorted(values []cid.Cid) []cid.Cid {
	byText := make(map[string]cid.Cid, len(values))
	for _, value := range values {
		byText[value.String()] = value
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
