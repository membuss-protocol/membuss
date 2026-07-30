// Phase 13: in-memory bloom filter cache for the local
// Mem-Store. The filter lets Has short-circuit before
// touching BadgerDB, which is the hot path for "do I have
// this block?" lookups during Memex fan-out, GC, and
// gateway traffic.
//
// The filter covers both the /b/ (raw block) and /d/
// (DAG-node) namespaces; Has in the store looks in both,
// so the filter must match that contract.
//
// The filter is not a substitute for the DB:
//   - it can produce false positives (it will claim a
//     block is present when it is not), and
//   - the DB is the source of truth for actual content.
//
// What the filter gives us is the opposite guarantee:
// when it says "definitely absent", we never have to open
// a BadgerDB transaction. That cuts the worst-case Has()
// from one View round-trip to a single RAM test when the
// MID is not local.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nnlgsakib/membuss/core/db"

	"github.com/nnlgsakib/membuss/core/mid"
)

// BloomConfig configures the in-memory bloom filter that
// backs Has() on the BadgerDB MemStore.
//
// Defaults applied when zero values are passed:
//   Capacity  = 10_000_000
//   FPRate    = 0.001   (0.1%)
type BloomConfig struct {
	// Capacity is the expected number of MIDs the store
	// will hold. Used to size the underlying bit vector.
	// Larger values use more RAM but keep the false
	// positive rate below FPRate for longer.
	Capacity uint

	// FPRate is the target false positive rate. Typical
	// values are 0.001 (0.1%) or 0.0001 (0.01%).
	FPRate float64

	// Disabled, when true, turns the filter off entirely.
	// Has() will then fall through to BadgerDB on every
	// call. Useful for tests that want to exercise the
	// raw DB path.
	Disabled bool

	// SnapshotPath is the on-disk file the filter is
	// loaded from on startup and written to on Close.
	// Empty disables persistence (the filter is rebuilt
	// from BadgerDB on every startup).
	SnapshotPath string
}

// DefaultBloomConfig returns safe production defaults.
func DefaultBloomConfig() BloomConfig {
	return BloomConfig{
		Capacity:     10_000_000,
		FPRate:       0.001,
		SnapshotPath: "",
	}
}

// bloomIndex is a thread-safe in-memory counting bloom filter
// wrapper. It owns the underlying CountingBloomFilter and
// the snapshot path; the MemStore holds one and calls
// into it from Put/Delete/Has.
type bloomIndex struct {
	mu       sync.RWMutex
	cbf      *CountingBloomFilter
	capacity uint
	fpRate   float64
	path     string
	closed   bool
}

// newBloomIndex constructs a fresh counting bloom filter with the
// requested capacity / FPRate. If path points at an
// existing readable file the filter is loaded from it;
// otherwise the filter is returned empty and the caller
// is expected to add MIDs as it sees them.
func newBloomIndex(cfg BloomConfig) (*bloomIndex, error) {
	if cfg.Capacity == 0 {
		cfg.Capacity = 10_000_000
	}
	if cfg.FPRate <= 0 {
		cfg.FPRate = 0.001
	}

	idx := &bloomIndex{
		capacity: cfg.Capacity,
		fpRate:   cfg.FPRate,
		path:     cfg.SnapshotPath,
	}

	if cfg.SnapshotPath != "" {
		if data, err := os.ReadFile(cfg.SnapshotPath); err == nil {
			cbf := &CountingBloomFilter{}
			if uerr := cbf.UnmarshalBinary(data); uerr == nil {
				idx.cbf = cbf
				return idx, nil
			}
			// Fall through: snapshot was corrupt; build a fresh one
		}
	}

	idx.cbf = NewCountingBloomFilter(cfg.Capacity, cfg.FPRate)
	return idx, nil
}

// fromDB walks every block/DAG key in the store and adds
// the corresponding MID to the filter. Used on startup
// when no snapshot file is available.
func (b *bloomIndex) fromDB(database *db.DB) error {
	if database == nil {
		return errors.New("bloom: nil db")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cbf == nil {
		b.cbf = NewCountingBloomFilter(b.capacity, b.fpRate)
	}
	it, err := database.NewIter()
	if err != nil {
		return err
	}
	defer it.Close()
	for _, prefix := range []string{db.PrefixBlock, db.PrefixDAG} {
		p := []byte(prefix)
		for it.SeekGE(p); it.Valid() && bytes.HasPrefix(it.Key(), p); it.Next() {
			raw := append([]byte(nil), it.Key()...)
			if len(raw) <= len(p) {
				continue
			}
			b.cbf.Add(raw[len(p):])
		}
	}
	return nil
}

// maybeTest is the RAM-only check. It returns false if
// the filter is sure the MID is absent and true otherwise.
func (b *bloomIndex) maybeTest(m mid.MID) bool {
	if b == nil || m.IsZero() {
		return true
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.cbf == nil {
		return true
	}
	return b.cbf.Test(m.Bytes())
}

// add records m in the filter. Safe to call concurrently.
func (b *bloomIndex) add(m mid.MID) {
	if b == nil || m.IsZero() {
		return
	}
	b.mu.Lock()
	if b.cbf != nil {
		b.cbf.Add(m.Bytes())
	}
	b.mu.Unlock()
}

// recordDelete removes m from the counting bloom filter in O(1) time.
func (b *bloomIndex) recordDelete(m mid.MID) {
	if b == nil || m.IsZero() {
		return
	}
	b.mu.Lock()
	if b.cbf != nil {
		b.cbf.Remove(m.Bytes())
	}
	b.mu.Unlock()
}

// rebuildFromDB performs an immediate rebuild of the counting bloom filter from the DB.
func (b *bloomIndex) rebuildFromDB(database *db.DB) error {
	if b == nil || database == nil {
		return nil
	}
	return b.performRebuild(database)
}

func (b *bloomIndex) performRebuild(database *db.DB) error {
	if database == nil {
		return errors.New("bloom: nil db")
	}
	fresh := NewCountingBloomFilter(b.capacity, b.fpRate)
	it, err := database.NewIter()
	if err != nil {
		return err
	}
	defer it.Close()
	for _, prefix := range []string{db.PrefixBlock, db.PrefixDAG} {
		p := []byte(prefix)
		for it.SeekGE(p); it.Valid() && bytes.HasPrefix(it.Key(), p); it.Next() {
			raw := append([]byte(nil), it.Key()...)
			if len(raw) <= len(p) {
				continue
			}
			fresh.Add(raw[len(p):])
		}
	}
	b.mu.Lock()
	b.cbf = fresh
	b.mu.Unlock()
	return nil
}

func (b *bloomIndex) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	return b.saveSnapshot()
}

// saveSnapshot serializes the filter to disk. A nil path is a no-op.
func (b *bloomIndex) saveSnapshot() error {
	if b == nil || b.path == "" {
		return nil
	}
	b.mu.RLock()
	if b.cbf == nil {
		b.mu.RUnlock()
		return nil
	}
	data, err := b.cbf.MarshalBinary()
	b.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("bloom: marshal: %w", err)
	}
	if dir := filepath.Dir(b.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bloom: mkdir %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(b.path, data, 0o644); err != nil {
		return fmt.Errorf("bloom: write %q: %w", b.path, err)
	}
	return nil
}

// capacityHint returns the configured capacity.
func (b *bloomIndex) capacityHint() uint {
	if b == nil {
		return 0
	}
	return b.capacity
}

// fpRateHint returns the configured false-positive rate.
func (b *bloomIndex) fpRateHint() float64 {
	if b == nil {
		return 0
	}
	return b.fpRate
}
