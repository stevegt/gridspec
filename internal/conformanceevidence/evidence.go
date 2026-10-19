// Package conformanceevidence stores bounded implementation evidence as an
// unsigned deterministic Grid message.
package conformanceevidence

import (
	"context"
	"fmt"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/cas"
	"github.com/promisegrid/gridspec/internal/gridwire"
)

const ProtocolCID = "bafkreieoo4wajvap53gtqgfalfpag2zbszxrh77rxbsnesexq33oz4gowu"

type Evidence struct {
	CID                  cid.Cid
	AcceptedCommit       cid.Cid
	ImplementationDigest string
	CheckerConfiguration cid.Cid
	EvidenceResults      cid.Cid
	Exclusions           cid.Cid
	Diagnostics          cid.Cid
	Report               cid.Cid
	closure              []cid.Cid
}

func (evidence Evidence) Closure() []cid.Cid { return append([]cid.Cid(nil), evidence.closure...) }

type BuildOptions struct {
	AcceptedCommit       cid.Cid
	ImplementationDigest string
	CheckerConfiguration []byte
	EvidenceResults      []byte
	Exclusions           []byte
	Diagnostics          []byte
	Report               []byte
}

func Build(ctx context.Context, store cas.Store, options BuildOptions) (Evidence, error) {
	if store == nil || !options.AcceptedCommit.Defined() || options.AcceptedCommit.Prefix().Codec != gridwire.GridCBORCodec || options.ImplementationDigest == "" {
		return Evidence{}, fmt.Errorf("store, accepted Grid commit, and implementation digest are required")
	}
	blocks := [][]byte{options.CheckerConfiguration, options.EvidenceResults, options.Exclusions, options.Diagnostics, options.Report}
	links := make([]cid.Cid, len(blocks))
	for index, block := range blocks {
		if len(block) == 0 {
			return Evidence{}, fmt.Errorf("conformance evidence block %d is empty", index)
		}
		links[index], _ = gridwire.RawCID(block)
		if err := store.Put(ctx, links[index], block); err != nil {
			return Evidence{}, err
		}
	}
	value := Evidence{
		AcceptedCommit: options.AcceptedCommit, ImplementationDigest: options.ImplementationDigest,
		CheckerConfiguration: links[0], EvidenceResults: links[1], Exclusions: links[2],
		Diagnostics: links[3], Report: links[4],
	}
	encoded, err := encode(value)
	if err != nil {
		return Evidence{}, err
	}
	value.CID, err = gridwire.GridCID(encoded)
	if err != nil {
		return Evidence{}, err
	}
	if err := store.Put(ctx, value.CID, encoded); err != nil {
		return Evidence{}, err
	}
	value.closure = append([]cid.Cid{value.CID, value.AcceptedCommit}, links...)
	return value, nil
}

func Verify(ctx context.Context, store cas.Store, evidenceCID cid.Cid) (Evidence, error) {
	data, err := store.Get(ctx, evidenceCID)
	if err != nil {
		return Evidence{}, err
	}
	actual, err := gridwire.GridCID(data)
	if err != nil || !actual.Equals(evidenceCID) {
		return Evidence{}, fmt.Errorf("conformance evidence bytes do not match CID")
	}
	envelope, err := gridwire.ParseEnvelope(data)
	if err != nil || envelope.Protocol.String() != ProtocolCID || len(envelope.Slots) != 7 {
		return Evidence{}, fmt.Errorf("evidence must use the closed GridSpec conformance-evidence v1 protocol")
	}
	value := Evidence{CID: evidenceCID}
	if value.AcceptedCommit, err = gridwire.DecodeCID(envelope.Slots[0]); err != nil || value.AcceptedCommit.Prefix().Codec != gridwire.GridCBORCodec {
		return Evidence{}, fmt.Errorf("invalid accepted commit")
	}
	if err := gridwire.Canonical(envelope.Slots[1], &value.ImplementationDigest); err != nil || value.ImplementationDigest == "" {
		return Evidence{}, fmt.Errorf("invalid implementation digest")
	}
	linked := []*cid.Cid{&value.CheckerConfiguration, &value.EvidenceResults, &value.Exclusions, &value.Diagnostics, &value.Report}
	for index := range linked {
		*linked[index], err = gridwire.DecodeCID(envelope.Slots[index+2])
		if err != nil {
			return Evidence{}, fmt.Errorf("invalid evidence link %d", index)
		}
	}
	value.closure = []cid.Cid{value.CID, value.AcceptedCommit, value.CheckerConfiguration, value.EvidenceResults, value.Exclusions, value.Diagnostics, value.Report}
	for _, link := range value.closure[1:] {
		block, err := store.Get(ctx, link)
		if err != nil {
			return Evidence{}, err
		}
		recomputed, err := link.Prefix().Sum(block)
		if err != nil || !recomputed.Equals(link) {
			return Evidence{}, fmt.Errorf("corrupt conformance evidence link %s", link)
		}
	}
	return value, nil
}

func encode(value Evidence) ([]byte, error) {
	accepted, _ := gridwire.EncodeCID(value.AcceptedCommit)
	digest, _ := gridwire.Encode(value.ImplementationDigest)
	links := []cid.Cid{value.CheckerConfiguration, value.EvidenceResults, value.Exclusions, value.Diagnostics, value.Report}
	slots := [][]byte{accepted, digest}
	for _, link := range links {
		encoded, _ := gridwire.EncodeCID(link)
		slots = append(slots, encoded)
	}
	return gridwire.BuildEnvelope(gridwire.ParseProtocolCID(ProtocolCID), slots...)
}
