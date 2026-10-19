// Package cas defines GridSpec's exact CID-addressed block boundary.
package cas

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/ipfs/go-cid"
)

// Store retrieves and stores exact bytes under a caller-selected CID.
type Store interface {
	Get(context.Context, cid.Cid) ([]byte, error)
	Put(context.Context, cid.Cid, []byte) error
}

// Overlay directs all writes to memory while falling back to base for reads.
// It lets validators replay deterministic computations without mutating the
// carrier being validated.
type Overlay struct {
	base   Store
	writes *Memory
}

func NewOverlay(base Store) *Overlay {
	return &Overlay{base: base, writes: NewMemory()}
}

func (overlay *Overlay) Get(ctx context.Context, blockCID cid.Cid) ([]byte, error) {
	if overlay == nil || overlay.base == nil {
		return nil, fmt.Errorf("overlay base store is required")
	}
	if data, err := overlay.writes.Get(ctx, blockCID); err == nil {
		return data, nil
	}
	return overlay.base.Get(ctx, blockCID)
}

func (overlay *Overlay) Put(ctx context.Context, blockCID cid.Cid, data []byte) error {
	if overlay == nil || overlay.base == nil {
		return fmt.Errorf("overlay base store is required")
	}
	return overlay.writes.Put(ctx, blockCID, data)
}

// Memory is a concurrency-safe in-memory block store used for transactions and
// tests before a complete closure is published into Git.
type Memory struct {
	mu     sync.RWMutex
	blocks map[string][]byte
}

func NewMemory() *Memory { return &Memory{blocks: make(map[string][]byte)} }

func (m *Memory) Get(ctx context.Context, blockCID cid.Cid) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil || !blockCID.Defined() {
		return nil, fmt.Errorf("store and defined CID are required")
	}
	m.mu.RLock()
	data, ok := m.blocks[blockCID.String()]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("block %s is absent", blockCID)
	}
	return bytes.Clone(data), nil
}

func (m *Memory) Put(ctx context.Context, blockCID cid.Cid, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil || !blockCID.Defined() {
		return fmt.Errorf("store and defined CID are required")
	}
	recomputed, err := blockCID.Prefix().Sum(data)
	if err != nil || !recomputed.Equals(blockCID) {
		return fmt.Errorf("block bytes do not match CID %s", blockCID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.blocks[blockCID.String()]; ok && !bytes.Equal(existing, data) {
		return fmt.Errorf("CID %s already names different bytes", blockCID)
	}
	m.blocks[blockCID.String()] = bytes.Clone(data)
	return nil
}

// CIDs returns a detached snapshot sorted by complete CID bytes.
func (m *Memory) CIDs() []cid.Cid {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]cid.Cid, 0, len(m.blocks))
	for text := range m.blocks {
		parsed, err := cid.Parse(text)
		if err == nil {
			result = append(result, parsed)
		}
	}
	sortCIDs(result)
	return result
}

// Copy copies the named exact blocks from source into destination.
func Copy(ctx context.Context, destination Store, source Store, cids []cid.Cid) error {
	for _, blockCID := range cids {
		data, err := source.Get(ctx, blockCID)
		if err != nil {
			return err
		}
		if err := destination.Put(ctx, blockCID, data); err != nil {
			return err
		}
	}
	return nil
}

func sortCIDs(values []cid.Cid) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && bytes.Compare(values[j].Bytes(), values[j-1].Bytes()) < 0; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
