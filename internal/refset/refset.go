// Package refset implements the frozen Grid Patricia reference-set protocol.
// Its bytes intentionally match github.com/promisegrid/grid/refset.
package refset

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

const ProtocolCID = "bafkreigfym46boug7d7bk6cjb5hjgrsm3sbse7mlgmayh6pta32ixrukne"

type entry struct {
	key      []byte
	value    []byte
	valueCID cid.Cid
}

type node struct {
	path     []byte
	valueCID cid.Cid
	children []child
}

type child struct {
	branch byte
	node   *node
	cid    cid.Cid
}

type storedBlock struct {
	cid  cid.Cid
	data []byte
}

// Set is a fully validated immutable mapping and its reachable block closure.
type Set struct {
	root    cid.Cid
	entries []entry
	closure []cid.Cid
}

func (s *Set) Root() cid.Cid {
	if s == nil {
		return cid.Undef
	}
	return s.root
}

func (s *Set) Get(key []byte) ([]byte, bool) {
	if s == nil || len(key) == 0 {
		return nil, false
	}
	position := sort.Search(len(s.entries), func(i int) bool {
		return bytes.Compare(s.entries[i].key, key) >= 0
	})
	if position == len(s.entries) || !bytes.Equal(s.entries[position].key, key) {
		return nil, false
	}
	return bytes.Clone(s.entries[position].value), true
}

func (s *Set) Scan(visit func(key, value []byte) error) error {
	if s == nil || visit == nil {
		return fmt.Errorf("reference set and callback are required")
	}
	for _, current := range s.entries {
		if err := visit(bytes.Clone(current.key), bytes.Clone(current.value)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Set) Closure() []cid.Cid {
	if s == nil {
		return nil
	}
	return append([]cid.Cid(nil), s.closure...)
}

// Build stores values, Patricia nodes, and the pCID-bearing wrapper.
func Build(ctx context.Context, store cas.Store, values map[string][]byte) (*Set, error) {
	if store == nil {
		return nil, fmt.Errorf("block store is required")
	}
	logical := make([]entry, 0, len(values))
	for key, value := range values {
		if len(key) == 0 {
			return nil, fmt.Errorf("reference key must be nonempty")
		}
		logical = append(logical, entry{key: []byte(key), value: bytes.Clone(value)})
	}
	sort.Slice(logical, func(i, j int) bool { return bytes.Compare(logical[i].key, logical[j].key) < 0 })

	rootNode, blocks, err := buildBlocks(logical)
	if err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if err := store.Put(ctx, block.cid, block.data); err != nil {
			return nil, err
		}
	}
	protocol := gridwire.ParseProtocolCID(ProtocolCID)
	wrapper, err := gridwire.Encode([]any{gridwire.TagCID(protocol), gridwire.TagCID(rootNode)})
	if err != nil {
		return nil, err
	}
	root, err := gridwire.DAGCBORCID(wrapper)
	if err != nil {
		return nil, err
	}
	if err := store.Put(ctx, root, wrapper); err != nil {
		return nil, err
	}
	opened, err := Open(ctx, store, root)
	if err != nil {
		return nil, err
	}
	return opened, nil
}

func buildBlocks(entries []entry) (cid.Cid, []storedBlock, error) {
	logical := make([]entry, len(entries))
	blocks := make([]storedBlock, 0, len(entries)*2+1)
	for index, current := range entries {
		valueCID, err := gridwire.RawCID(current.value)
		if err != nil {
			return cid.Undef, nil, err
		}
		logical[index] = entry{key: bytes.Clone(current.key), value: bytes.Clone(current.value), valueCID: valueCID}
		blocks = append(blocks, storedBlock{cid: valueCID, data: bytes.Clone(current.value)})
	}
	var root *node
	if len(logical) == 0 {
		root = &node{}
	} else {
		root = buildNode(logical, 0)
	}
	rootCID, err := encodeNode(root, &blocks)
	return rootCID, blocks, err
}

func buildNode(entries []entry, offset int) *node {
	shared := len(entries[0].key) - offset
	for _, current := range entries[1:] {
		limit := len(current.key) - offset
		if limit < shared {
			shared = limit
		}
		position := 0
		for position < shared && entries[0].key[offset+position] == current.key[offset+position] {
			position++
		}
		shared = position
	}
	current := &node{path: bytes.Clone(entries[0].key[offset : offset+shared])}
	nextOffset := offset + shared
	remaining := entries
	if len(entries[0].key) == nextOffset {
		current.valueCID = entries[0].valueCID
		remaining = entries[1:]
	}
	for start := 0; start < len(remaining); {
		branch := remaining[start].key[nextOffset]
		end := start + 1
		for end < len(remaining) && remaining[end].key[nextOffset] == branch {
			end++
		}
		current.children = append(current.children, child{
			branch: branch,
			node:   buildNode(remaining[start:end], nextOffset+1),
		})
		start = end
	}
	return current
}

func encodeNode(current *node, blocks *[]storedBlock) (cid.Cid, error) {
	children := make([]any, len(current.children))
	for index := range current.children {
		childCID, err := encodeNode(current.children[index].node, blocks)
		if err != nil {
			return cid.Undef, err
		}
		current.children[index].cid = childCID
		children[index] = []any{uint64(current.children[index].branch), gridwire.TagCID(childCID)}
	}
	var value any
	if current.valueCID.Defined() {
		value = gridwire.TagCID(current.valueCID)
	}
	encoded, err := gridwire.Encode([]any{current.path, value, children})
	if err != nil {
		return cid.Undef, fmt.Errorf("encode Patricia node: %w", err)
	}
	nodeCID, err := gridwire.DAGCBORCID(encoded)
	if err != nil {
		return cid.Undef, err
	}
	*blocks = append(*blocks, storedBlock{cid: nodeCID, data: encoded})
	return nodeCID, nil
}

// Open validates the complete reference-set closure and canonical structure.
func Open(ctx context.Context, store cas.Store, root cid.Cid) (*Set, error) {
	if store == nil || !isCodec(root, gridwire.DAGCBORCodec) {
		return nil, fmt.Errorf("store and DAG-CBOR root are required")
	}
	wrapper, err := exact(ctx, store, root)
	if err != nil {
		return nil, err
	}
	var fields []cbor.RawMessage
	if err := gridwire.Decode(wrapper, &fields); err != nil || len(fields) != 2 {
		return nil, fmt.Errorf("reference-set root must be a closed two-item array")
	}
	protocol, err := gridwire.DecodeCID(fields[0])
	if err != nil || protocol.String() != ProtocolCID {
		return nil, fmt.Errorf("unsupported reference-set protocol")
	}
	rootNode, err := gridwire.DecodeCID(fields[1])
	if err != nil || !isCodec(rootNode, gridwire.DAGCBORCodec) {
		return nil, fmt.Errorf("invalid reference-set root node")
	}
	canonical, err := gridwire.Encode([]any{gridwire.TagCID(protocol), gridwire.TagCID(rootNode)})
	if err != nil || !bytes.Equal(canonical, wrapper) {
		return nil, fmt.Errorf("reference-set root is not deterministic DAG-CBOR")
	}

	entries := make([]entry, 0)
	closure := []cid.Cid{root}
	seenNodes := make(map[string]struct{})
	seenValues := make(map[string]struct{})
	seenKeys := make(map[string]struct{})
	if err := readNode(ctx, store, rootNode, nil, true, seenNodes, seenValues, seenKeys, &entries, &closure); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].key, entries[j].key) < 0 })
	rebuiltRoot, _, err := buildBlocks(entries)
	if err != nil || !rebuiltRoot.Equals(rootNode) {
		return nil, fmt.Errorf("Patricia tree is not canonical")
	}
	sort.Slice(closure, func(i, j int) bool { return bytes.Compare(closure[i].Bytes(), closure[j].Bytes()) < 0 })
	return &Set{root: root, entries: entries, closure: closure}, nil
}

func readNode(ctx context.Context, store cas.Store, nodeCID cid.Cid, prefix []byte, root bool, seenNodes, seenValues, seenKeys map[string]struct{}, entries *[]entry, closure *[]cid.Cid) error {
	if !isCodec(nodeCID, gridwire.DAGCBORCodec) {
		return fmt.Errorf("Patricia node %s has unsupported CID profile", nodeCID)
	}
	if _, duplicate := seenNodes[nodeCID.String()]; duplicate {
		return fmt.Errorf("Patricia node %s is linked more than once", nodeCID)
	}
	seenNodes[nodeCID.String()] = struct{}{}
	*closure = append(*closure, nodeCID)
	encoded, err := exact(ctx, store, nodeCID)
	if err != nil {
		return err
	}
	path, valueCID, children, err := decodeNode(encoded)
	if err != nil {
		return fmt.Errorf("decode Patricia node %s: %w", nodeCID, err)
	}
	if !valueCID.Defined() && len(children) == 0 {
		if !root || len(path) != 0 {
			return fmt.Errorf("empty node is valid only as empty-map root")
		}
		return nil
	}
	if !valueCID.Defined() && len(children) < 2 {
		return fmt.Errorf("Patricia node without value must have at least two children")
	}
	keyPrefix := append(bytes.Clone(prefix), path...)
	if valueCID.Defined() {
		if len(keyPrefix) == 0 {
			return fmt.Errorf("Patricia key must be nonempty")
		}
		if _, duplicate := seenKeys[string(keyPrefix)]; duplicate {
			return fmt.Errorf("duplicate Patricia key")
		}
		seenKeys[string(keyPrefix)] = struct{}{}
		if _, duplicate := seenValues[valueCID.String()]; !duplicate {
			seenValues[valueCID.String()] = struct{}{}
			*closure = append(*closure, valueCID)
		}
		value, err := exact(ctx, store, valueCID)
		if err != nil {
			return err
		}
		if !isCodec(valueCID, gridwire.RawCodec) {
			return fmt.Errorf("Patricia value must use raw SHA-256 CID")
		}
		*entries = append(*entries, entry{key: keyPrefix, value: value, valueCID: valueCID})
	}
	for _, branch := range children {
		childPrefix := append(bytes.Clone(keyPrefix), branch.branch)
		if err := readNode(ctx, store, branch.cid, childPrefix, false, seenNodes, seenValues, seenKeys, entries, closure); err != nil {
			return err
		}
	}
	return nil
}

func decodeNode(encoded []byte) ([]byte, cid.Cid, []child, error) {
	var fields []cbor.RawMessage
	if err := gridwire.Decode(encoded, &fields); err != nil || len(fields) != 3 {
		return nil, cid.Undef, nil, fmt.Errorf("node must be a closed three-item array")
	}
	var path []byte
	if err := gridwire.Decode(fields[0], &path); err != nil {
		return nil, cid.Undef, nil, err
	}
	valueCID := cid.Undef
	if !bytes.Equal(fields[1], []byte{0xf6}) {
		var err error
		valueCID, err = gridwire.DecodeCID(fields[1])
		if err != nil || !isCodec(valueCID, gridwire.RawCodec) {
			return nil, cid.Undef, nil, fmt.Errorf("value link must use raw SHA-256 CID")
		}
	}
	var rawChildren []cbor.RawMessage
	if err := gridwire.Decode(fields[2], &rawChildren); err != nil {
		return nil, cid.Undef, nil, err
	}
	children := make([]child, len(rawChildren))
	for index, raw := range rawChildren {
		var pair []cbor.RawMessage
		if err := gridwire.Decode(raw, &pair); err != nil || len(pair) != 2 {
			return nil, cid.Undef, nil, fmt.Errorf("child must be a two-item array")
		}
		var branch uint64
		if err := gridwire.Decode(pair[0], &branch); err != nil || branch > 255 {
			return nil, cid.Undef, nil, fmt.Errorf("child branch must be an unsigned byte")
		}
		if index > 0 && uint64(children[index-1].branch) >= branch {
			return nil, cid.Undef, nil, fmt.Errorf("child branches must be sorted and unique")
		}
		childCID, err := gridwire.DecodeCID(pair[1])
		if err != nil || !isCodec(childCID, gridwire.DAGCBORCodec) {
			return nil, cid.Undef, nil, fmt.Errorf("invalid child CID")
		}
		children[index] = child{branch: byte(branch), cid: childCID}
	}
	canonicalChildren := make([]any, len(children))
	for index, branch := range children {
		canonicalChildren[index] = []any{uint64(branch.branch), gridwire.TagCID(branch.cid)}
	}
	var canonicalValue any
	if valueCID.Defined() {
		canonicalValue = gridwire.TagCID(valueCID)
	}
	canonical, err := gridwire.Encode([]any{path, canonicalValue, canonicalChildren})
	if err != nil || !bytes.Equal(canonical, encoded) {
		return nil, cid.Undef, nil, fmt.Errorf("node is not deterministic DAG-CBOR")
	}
	return path, valueCID, children, nil
}

func exact(ctx context.Context, store cas.Store, requested cid.Cid) ([]byte, error) {
	data, err := store.Get(ctx, requested)
	if err != nil {
		return nil, err
	}
	recomputed, err := requested.Prefix().Sum(data)
	if err != nil || !recomputed.Equals(requested) {
		return nil, fmt.Errorf("block bytes do not match CID %s", requested)
	}
	return data, nil
}

func isCodec(value cid.Cid, codec uint64) bool {
	if !value.Defined() {
		return false
	}
	prefix := value.Prefix()
	return prefix.Version == 1 && prefix.Codec == codec && prefix.MhType == 0x12 && prefix.MhLength == 32
}
