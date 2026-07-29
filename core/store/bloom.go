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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
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

// bloomIndex is a thread-safe in-memory bloom filter
// wrapper. It owns the underlying bloom.BloomFilter and
// the snapshot path; the MemStore holds one and calls
// into it from Put/Delete/Has.
type bloomIndex struct {
	mu       sync.RWMutex
	filter   *bloom.BloomFilter
	capacity uint
	fpRate   float64
	path     string

	rebuildCh    chan *db.DB
	closeCh      chan struct{}
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
	deletedCount uint64
}

// newBloomIndex constructs a fresh filter with the
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

	idx.rebuildCh = make(chan *db.DB, 1)
	idx.closeCh = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	idx.ctx = ctx
	idx.cancel = cancel

	if cfg.SnapshotPath != "" {
		if data, err := os.ReadFile(cfg.SnapshotPath); err == nil {
			bf := &bloom.BloomFilter{}
			if uerr := bf.UnmarshalBinary(data); uerr == nil {
				idx.filter = bf
				go idx.rebuildWorker()
				return idx, nil
			}
			// Fall through: snapshot was corrupt; build a
			// fresh one and let the next Close rewrite it.
		}
	}

	idx.filter = bloom.NewWithEstimates(cfg.Capacity, cfg.FPRate)
	go idx.rebuildWorker()

	return idx, nil
}

// fromDB walks every block/DAG key in the store and adds
// the corresponding MID to the filter. Used on startup
// when no snapshot file is available. This is a
// one-time, blocking operation; the caller is expected
// to invoke it from a constructor.
func (b *bloomIndex) fromDB(database *db.DB) error {
	if database == nil {
		return errors.New("bloom: nil db")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Ensure we have a live filter.
	if b.filter == nil {
		b.filter = bloom.NewWithEstimates(b.capacity, b.fpRate)
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
			b.filter.Add(raw[len(p):])
		}
	}
	return nil
}

// maybeTest is the RAM-only check. It returns false if
// the filter is sure the MID is absent and true
// otherwise. Callers MUST treat a true result as a hint
// that requires a DB confirmation.
func (b *bloomIndex) maybeTest(m mid.MID) bool {
	if b == nil || m.IsZero() {
		return true
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.filter == nil {
		return true
	}
	return b.filter.Test(m.Bytes())
}

// add records m in the filter. Safe to call concurrently.
func (b *bloomIndex) add(m mid.MID) {
	if b == nil || m.IsZero() {
		return
	}
	b.mu.Lock()
	if b.filter != nil {
		b.filter.Add(m.Bytes())
	}
	b.mu.Unlock()
}

// recordDelete records a block deletion in memory.
// Standard bloom filters do not support item removal, so deleted MIDs remain
// as benign false positives without compromising Has/Get correctness.
// If cumulative deletions exceed 10% of configured capacity, a debounced
// background rebuild is queued.
func (b *bloomIndex) recordDelete(database *db.DB) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.deletedCount++
	threshold := uint64(b.capacity / 10)
	if threshold == 0 {
		threshold = 1000
	}
	shouldRebuild := b.deletedCount >= threshold
	b.mu.Unlock()

	if shouldRebuild {
		_ = b.rebuildFromDB(database)
	}
}

// rebuildFromDB queues an asynchronous, debounced rebuild of the bloom filter.
func (b *bloomIndex) rebuildFromDB(database *db.DB) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("bloom: closed")
	}
	select {
	case b.rebuildCh <- database:
	default:
	}
	return nil
}

func (b *bloomIndex) performRebuild(database *db.DB) error {
	if database == nil {
		return errors.New("bloom: nil db")
	}
	fresh := bloom.NewWithEstimates(b.capacity, b.fpRate)
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
	b.filter = fresh
	b.deletedCount = 0
	b.mu.Unlock()
	return nil
}

func (b *bloomIndex) rebuildWorker() {
	defer close(b.closeCh)
	var lastDb *db.DB
	timer := time.NewTimer(5 * time.Second)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		select {
		case <-b.ctx.Done():
			if lastDb != nil {
				_ = b.performRebuild(lastDb)
			}
			return
		case database := <-b.rebuildCh:
			lastDb = database
			timer.Reset(5 * time.Second)
		case <-timer.C:
			if lastDb != nil {
				_ = b.performRebuild(lastDb)
				lastDb = nil
			}
		}
	}
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
	b.cancel()
	b.mu.Unlock()

	<-b.closeCh
	return b.saveSnapshot()
}

// saveSnapshot serializes the filter to disk. A nil path
// is a no-op. Errors are returned to the caller; the
// store treats them as warnings (the on-disk state is
// advisory, the source of truth is BadgerDB).
func (b *bloomIndex) saveSnapshot() error {
	if b == nil || b.path == "" {
		return nil
	}
	b.mu.RLock()
	data, err := b.filter.MarshalBinary()
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
