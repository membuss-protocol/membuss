// Package store: db-backed implementation of the Store and
// Blockstore interfaces.
package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/db"
	"github.com/nnlgsakib/membuss/core/mid"
)

// Store is the full surface: it embeds Blockstore and adds
// seal records and GC. Implementations MUST be safe for
// concurrent use.
type Store interface {
	Blockstore

	// Seal marks the given MID (and, if recursive is true, every
	// MID reachable from it via the DAG) as protected from GC.
	Seal(root mid.MID, recursive bool) error

	// Unseal removes the seal record for the given MID.
	Unseal(root mid.MID) error

	// IsSealed reports whether a direct seal record exists for m.
	IsSealed(m mid.MID) (bool, error)

	// AllSealed returns every MID that has a direct seal record.
	AllSealed() ([]mid.MID, error)

	// AllBlocks returns every MID that has a block or DAG
	// record in the store.
	AllBlocks() ([]mid.MID, error)

	// AllObjectMIDs returns every MID that has an ObjectInfo metadata record
	// where IsRoot is true.
	AllObjectMIDs() ([]mid.MID, error)

	// PutMeta stores an arbitrary key/value pair under the /m/ namespace.
	PutMeta(key string, value []byte) error

	// GetMeta returns the value previously stored under key, or
	// ErrNotFound if absent.
	GetMeta(key string) ([]byte, error)

	// GC walks every sealed DAG root, collects the reachable
	// MID set, and deletes every key in the store that is NOT
	// in that set.
	GC(ctx context.Context) (uint64, error)

	// GCWithMinAge is like GC but only deletes blocks whose
	// commit timestamp is older than minAge.
	GCWithMinAge(ctx context.Context, minAge time.Duration) (uint64, error)

	// Size returns the approximate size of the block data in the store.
	Size() (uint64, error)

	// Close releases all resources held by the store.
	Close() error

	// DropAll deletes every key and value in the store, resetting it to empty.
	DropAll() error

	// IterateBlocks invokes fn for every block/DAG MID in the store.
	IterateBlocks(fn func(mid.MID) error) error

	// IterateSealed invokes fn for every sealed root MID in the store.
	IterateSealed(fn func(mid.MID) error) error
}

// MemStore is the db-backed Store implementation.
type MemStore struct {
	db         *db.DB
	bloom      *bloomIndex
	blocksPath string

	mu       sync.RWMutex
	wg       sync.WaitGroup
	closed   bool
	dropping bool

	// gcMu serializes garbage-collection runs. It is a leaf lock,
	// distinct from mu (which guards the store lifecycle): mu is an
	// RWMutex whose RLock is shared, so it does not prevent two GC
	// runs from overlapping. gcMu ensures at most one GC walks and
	// deletes at a time, so concurrent callers run sequentially
	// against current state instead of racing on deletion.
	gcMu sync.Mutex

	// gcTracker protects concurrent block writes and recent unsealed uploads from GC deletion.
	gcTracker *gcWriteTracker

	// Hooks receives store operation interceptors from the plugin framework.
	Hooks StoreHooks
}

// SetHooks sets the store hooks interceptor interface.
func (s *MemStore) SetHooks(hooks StoreHooks) {
	s.Hooks = hooks
}

// Options configures a MemStore at construction time.
type Options struct {
	// Path is the on-disk directory DB will use. Required
	// unless InMemory is true.
	Path string

	// BlocksPath is the on-disk directory where block payloads are stored
	// in an IPFS-like flat directory structure. If empty and Path is set,
	// defaults to filepath.Join(filepath.Dir(Path), "blocks").
	BlocksPath string

	// InMemory, if true, makes DB use an in-memory VFS
	// backend and ignores Path. Used for tests.
	InMemory bool

	// ReadOnly opens the store in read-only mode.
	ReadOnly bool

	// Logger, if non-nil, is passed to Pebble.
	Logger db.Logger

	// Bloom configures the in-memory bloom filter that backs
	// Has() lookups (Phase 13).
	Bloom BloomConfig
}

// NewMemStore opens (or creates) a MemStore at opts.Path.
func NewMemStore(opts Options) (*MemStore, error) {
	if !opts.InMemory && filepath.Clean(opts.Path) == "" {
		return nil, errors.New("store: empty path")
	}

	pdb, err := db.Open(db.Options{
		Path:     opts.Path,
		InMemory: opts.InMemory,
		ReadOnly: opts.ReadOnly,
		Logger:   opts.Logger,
	})
	if err != nil {
		return nil, err
	}

	s := &MemStore{
		db:        pdb,
		gcTracker: newGCWriteTracker(0),
	}
	if !opts.InMemory {
		if opts.BlocksPath != "" {
			s.blocksPath = opts.BlocksPath
		} else if opts.Path != "" {
			s.blocksPath = filepath.Join(filepath.Dir(filepath.Clean(opts.Path)), "blocks")
		}
	}

	if err := s.initBloom(opts.Bloom); err != nil {
		_ = pdb.Close()
		return nil, fmt.Errorf("store: init bloom: %w", err)
	}
	return s, nil
}

func (s *MemStore) blockPath(m mid.MID) string {
	if s.blocksPath == "" {
		return ""
	}
	mStr := m.String()
	if len(mStr) < 3 {
		return filepath.Join(s.blocksPath, "xx", mStr+".data")
	}
	// next-to-last two characters
	prefix := mStr[len(mStr)-3 : len(mStr)-1]
	return filepath.Join(s.blocksPath, prefix, mStr+".data")
}

// initBloom brings up the bloom index.
func (s *MemStore) initBloom(cfg BloomConfig) error {
	if cfg.Disabled {
		s.bloom = nil
		return nil
	}
	idx, err := newBloomIndex(cfg)
	if err != nil {
		return err
	}
	if err := idx.fromDB(s.db); err != nil {
		return err
	}
	s.bloom = idx
	return nil
}

// LargeBlockThreshold is the payload size threshold (1 MiB) above which
// block data is written to external flat file storage when blocksPath is set.
// Small and medium blocks (< 1 MiB) are stored directly in Pebble SSTables.
const LargeBlockThreshold = 1024 * 1024

// Put stores data under the given MID.
func (s *MemStore) Put(m mid.MID, data []byte) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()
	if m.IsZero() {
		return errors.New("store: zero MID")
	}
	if s.Hooks != nil {
		blk, err := s.Hooks.TriggerBeforeBlockPut(context.Background(), &Block{MID: m, Data: data})
		if err != nil {
			return err
		}
		if blk != nil {
			m = blk.MID
			data = blk.Data
		}
	}
	if err := verifyContent(m, data); err != nil {
		return err
	}

	b := s.db.NewBatch()
	defer b.Close()

	if s.blocksPath != "" && len(data) >= LargeBlockThreshold {
		// Write block data to flat file on disk for large blobs (>= 1MB)
		fpath := s.blockPath(m)
		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return fmt.Errorf("store: create block dir: %w", err)
		}
		// Write atomically to avoid corrupted files on crash
		tmpFile := fpath + ".tmp"
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			return fmt.Errorf("store: write block file: %w", err)
		}
		if err := os.Rename(tmpFile, fpath); err != nil {
			_ = os.Remove(tmpFile)
			return fmt.Errorf("store: rename block file: %w", err)
		}

		// Store length as 8-byte uint64 in Pebble value
		valBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(valBytes, uint64(len(data)))
		if err := b.Set(db.BlockKey(m), valBytes); err != nil {
			return err
		}
	} else {
		if err := b.Set(db.BlockKey(m), data); err != nil {
			return err
		}
	}

	if err := db.PutTimestamp(b, m); err != nil {
		return err
	}
	if err := b.Commit(); err != nil {
		return err
	}

	if s.bloom != nil {
		s.bloom.add(m)
	}
	if s.gcTracker != nil {
		s.gcTracker.RecordWrite(m)
	}
	if s.Hooks != nil {
		s.Hooks.TriggerAfterBlockPut(context.Background(), m, int64(len(data)))
	}
	return nil
}

// PutBatch stores multiple blocks atomically using a single DB write transaction.
func (s *MemStore) PutBatch(blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()

	b := s.db.NewBatch()
	defer b.Close()

	for i := range blocks {
		m := blocks[i].MID
		data := blocks[i].Data
		if m.IsZero() {
			return errors.New("store: zero MID in batch")
		}
		if s.Hooks != nil {
			blk, err := s.Hooks.TriggerBeforeBlockPut(context.Background(), &Block{MID: m, Data: data})
			if err != nil {
				return err
			}
			if blk != nil {
				m = blk.MID
				data = blk.Data
			}
		}
		if err := verifyContent(m, data); err != nil {
			return err
		}

		if s.blocksPath != "" && len(data) >= LargeBlockThreshold {
			fpath := s.blockPath(m)
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return fmt.Errorf("store: create block dir: %w", err)
			}
			tmpFile := fpath + ".tmp"
			if err := os.WriteFile(tmpFile, data, 0644); err != nil {
				return fmt.Errorf("store: write block file: %w", err)
			}
			if err := os.Rename(tmpFile, fpath); err != nil {
				_ = os.Remove(tmpFile)
				return fmt.Errorf("store: rename block file: %w", err)
			}

			valBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(valBytes, uint64(len(data)))
			if err := b.Set(db.BlockKey(m), valBytes); err != nil {
				return err
			}
		} else {
			if err := b.Set(db.BlockKey(m), data); err != nil {
				return err
			}
		}

		if err := db.PutTimestamp(b, m); err != nil {
			return err
		}
	}

	if err := b.Commit(); err != nil {
		return err
	}

	for i := range blocks {
		m := blocks[i].MID
		dataLen := int64(len(blocks[i].Data))
		if s.bloom != nil {
			s.bloom.add(m)
		}
		if s.gcTracker != nil {
			s.gcTracker.RecordWrite(m)
		}
		if s.Hooks != nil {
			s.Hooks.TriggerAfterBlockPut(context.Background(), m, dataLen)
		}
	}
	return nil
}

// PutDAG stores data under the given MID as a DAG node.
func (s *MemStore) PutDAG(m mid.MID, data []byte) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()
	if m.IsZero() {
		return errors.New("store: zero MID")
	}
	if err := verifyContent(m, data); err != nil {
		return err
	}

	b := s.db.NewBatch()
	defer b.Close()

	if s.blocksPath != "" && len(data) >= LargeBlockThreshold {
		// Write block data to flat file on disk for large blobs (>= 1MB)
		fpath := s.blockPath(m)
		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return fmt.Errorf("store: create block dir: %w", err)
		}
		// Write atomically to avoid corrupted files on crash
		tmpFile := fpath + ".tmp"
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			return fmt.Errorf("store: write block file: %w", err)
		}
		if err := os.Rename(tmpFile, fpath); err != nil {
			_ = os.Remove(tmpFile)
			return fmt.Errorf("store: rename block file: %w", err)
		}

		// Store length as 8-byte uint64 in Pebble value
		valBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(valBytes, uint64(len(data)))
		if err := b.Set(db.DagKey(m), valBytes); err != nil {
			return err
		}
	} else {
		if err := b.Set(db.DagKey(m), data); err != nil {
			return err
		}
	}

	if err := db.PutTimestamp(b, m); err != nil {
		return err
	}
	if err := b.Commit(); err != nil {
		return err
	}

	if s.bloom != nil {
		s.bloom.add(m)
	}
	if s.gcTracker != nil {
		s.gcTracker.RecordWrite(m)
	}
	return nil
}

// Get returns the bytes stored under m, looking in BOTH the
// block and DAG namespaces.
func (s *MemStore) Get(m mid.MID) ([]byte, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.exit()
	if m.IsZero() {
		return nil, errors.New("store: zero MID")
	}
	if s.Hooks != nil {
		var err error
		m, err = s.Hooks.TriggerBeforeBlockGet(context.Background(), m)
		if err != nil {
			return nil, err
		}
	}
	if s.bloom != nil && !s.bloom.maybeTest(m) {
		return nil, ErrNotFound
	}

	var data []byte
	var val []byte
	var valErr error

	if b, err := s.db.Get(db.BlockKey(m)); err == nil {
		val = b
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	} else if d, err := s.db.Get(db.DagKey(m)); err == nil {
		val = d
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	} else {
		valErr = ErrNotFound
	}

	if valErr != nil {
		return nil, valErr
	}

	if s.blocksPath != "" && len(val) == 8 {
		fpath := s.blockPath(m)
		if d, err := os.ReadFile(fpath); err == nil {
			data = d
		} else if os.IsNotExist(err) {
			data = val
		} else {
			return nil, err
		}
	} else {
		data = val
	}

	if s.Hooks != nil {
		return s.Hooks.TriggerAfterBlockGet(context.Background(), m, data)
	}
	return data, nil
}

// GetDAG returns the bytes stored under m in the DAG namespace only.
func (s *MemStore) GetDAG(m mid.MID) ([]byte, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.exit()
	if m.IsZero() {
		return nil, errors.New("store: zero MID")
	}

	val, err := s.db.Get(db.DagKey(m))
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}

	if s.blocksPath != "" && len(val) == 8 {
		fpath := s.blockPath(m)
		if d, err := os.ReadFile(fpath); err == nil {
			return d, nil
		} else if os.IsNotExist(err) {
			return val, nil
		} else {
			return nil, err
		}
	}
	return val, nil
}

// Has reports whether a block OR a DAG node exists for m.
func (s *MemStore) Has(m mid.MID) (bool, error) {
	if err := s.enter(); err != nil {
		return false, err
	}
	defer s.exit()
	if m.IsZero() {
		return false, errors.New("store: zero MID")
	}
	if s.bloom != nil && !s.bloom.maybeTest(m) {
		return false, nil
	}
	if found, err := s.db.Has(db.BlockKey(m)); err == nil && found {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return s.db.Has(db.DagKey(m))
}

// HasDAG reports whether a DAG node exists for m.
func (s *MemStore) HasDAG(m mid.MID) (bool, error) {
	if err := s.enter(); err != nil {
		return false, err
	}
	defer s.exit()
	if m.IsZero() {
		return false, errors.New("store: zero MID")
	}
	if s.bloom != nil && !s.bloom.maybeTest(m) {
		return false, nil
	}
	return s.db.Has(db.DagKey(m))
}

// Delete removes the block for m from BOTH namespaces.
func (s *MemStore) Delete(m mid.MID) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()

	b := s.db.NewBatch()
	defer b.Close()
	_ = b.Delete(db.BlockKey(m))
	_ = b.Delete(db.DagKey(m))
	_ = b.Delete(db.MetaKey("obj/" + m.String()))
	_ = b.Delete(db.MetaKey("ts/" + m.String()))

	if err := b.Commit(); err != nil {
		return err
	}

	if s.blocksPath != "" {
		_ = os.Remove(s.blockPath(m))
	}

	if s.bloom != nil {
		s.bloom.recordDelete(m)
	}
	if s.Hooks != nil {
		s.Hooks.TriggerAfterBlockDel(context.Background(), m)
	}
	return nil
}

// DeleteDAG removes the DAG node for m.
func (s *MemStore) DeleteDAG(m mid.MID) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()

	b := s.db.NewBatch()
	defer b.Close()
	_ = b.Delete(db.DagKey(m))
	_ = b.Delete(db.MetaKey("obj/" + m.String()))
	_ = b.Delete(db.MetaKey("ts/" + m.String()))

	if err := b.Commit(); err != nil {
		return err
	}

	if s.blocksPath != "" {
		_ = os.Remove(s.blockPath(m))
	}

	if s.bloom != nil {
		s.bloom.recordDelete(m)
	}
	return nil
}

func (s *MemStore) enter() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.dropping || s.db == nil {
		return errors.New("store: closed")
	}
	s.wg.Add(1)
	return nil
}

func (s *MemStore) exit() {
	s.wg.Done()
}

// Close releases the underlying DB handle.
func (s *MemStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.wg.Wait()

	var snapErr error
	if s.bloom != nil {
		snapErr = s.bloom.Close()
	}
	dbErr := s.db.Close()
	s.db = nil
	s.bloom = nil
	if dbErr != nil {
		return dbErr
	}
	return snapErr
}

// DropAll deletes all keys and values in the store, resetting it to empty.
func (s *MemStore) DropAll() error {
	s.mu.Lock()
	if s.closed || s.dropping {
		s.mu.Unlock()
		return errors.New("store closed or dropping")
	}
	s.dropping = true
	s.mu.Unlock()

	s.wg.Wait()

	batch := s.db.NewBatch()
	defer batch.Close()

	it, err := s.db.NewIter()
	if err != nil {
		s.mu.Lock()
		s.dropping = false
		s.mu.Unlock()
		return err
	}
	for it.First(); it.Valid(); it.Next() {
		_ = batch.Delete(it.Key())
	}
	_ = it.Close()

	err = batch.Commit()

	if err == nil && s.blocksPath != "" {
		_ = os.RemoveAll(s.blocksPath)
		_ = os.MkdirAll(s.blocksPath, 0755)
	}

	if err == nil && s.bloom != nil {
		err = s.bloom.rebuildFromDB(s.db)
	}

	s.mu.Lock()
	s.dropping = false
	s.mu.Unlock()

	if err != nil {
		return fmt.Errorf("store: drop all failed: %w", err)
	}
	return nil
}

// Size returns the size of block keys.
func (s *MemStore) Size() (uint64, error) {
	if err := s.enter(); err != nil {
		return 0, err
	}
	defer s.exit()
	var total uint64
	seen := make(map[string]struct{})
	it, err := s.db.NewIter()
	if err != nil {
		return 0, err
	}
	defer it.Close()

	for _, prefix := range [][]byte{[]byte(db.PrefixBlock), []byte(db.PrefixDAG)} {
		for it.SeekGE(prefix); it.Valid() && bytes.HasPrefix(it.Key(), prefix); it.Next() {
			keyStr := string(it.Key())
			if _, ok := seen[keyStr]; ok {
				continue
			}
			seen[keyStr] = struct{}{}

			val := it.Value()
			if s.blocksPath != "" {
				if len(val) == 8 {
					total += binary.BigEndian.Uint64(val)
				} else {
					total += uint64(len(val))
				}
			} else {
				total += uint64(len(val))
			}
		}
	}
	return total, nil
}

// verifyContent checks that the data's SHA-256 digest matches the MID's digest.
func verifyContent(m mid.MID, data []byte) error {
	want, err := m.DigestBytes()
	if err != nil {
		return fmt.Errorf("store: claim MID has no digest: %w", err)
	}
	got := mid.FromBytes(data)
	gotDigest, err := got.DigestBytes()
	if err != nil {
		return fmt.Errorf("store: derive digest: %w", err)
	}
	if !bytes.Equal(want, gotDigest) {
		return fmt.Errorf("store: data does not hash to claimed MID %s", m.String())
	}
	return nil
}

func (s *MemStore) GetSize(m mid.MID) (int64, error) {
	if err := s.enter(); err != nil {
		return 0, err
	}
	defer s.exit()
	val, err := s.db.Get(db.BlockKey(m))
	if errors.Is(err, db.ErrNotFound) {
		val, err = s.db.Get(db.DagKey(m))
	}
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if s.blocksPath != "" && len(val) == 8 {
		return int64(binary.BigEndian.Uint64(val)), nil
	}
	return int64(len(val)), nil
}

// AllBlocks returns every MID that has a block or DAG record in the store.
func (s *MemStore) AllBlocks() ([]mid.MID, error) {
	var out []mid.MID
	err := s.IterateBlocks(func(m mid.MID) error {
		out = append(out, m)
		return nil
	})
	return out, err
}

func (s *MemStore) IterateBlocks(fn func(mid.MID) error) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()
	seen := make(map[string]struct{})
	it, err := s.db.NewIter()
	if err != nil {
		return err
	}
	defer it.Close()
	for _, prefix := range [][]byte{[]byte(db.PrefixBlock), []byte(db.PrefixDAG)} {
		for it.SeekGE(prefix); it.Valid() && bytes.HasPrefix(it.Key(), prefix); it.Next() {
			raw := append([]byte(nil), it.Key()...)
			raw = raw[len(prefix):]
			m, err := mid.FromMultihash(mid.CodecRaw, raw)
			if err != nil {
				continue
			}
			key := m.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if err := fn(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// PutMeta stores an arbitrary key/value pair under the "/m/" namespace.
func (s *MemStore) PutMeta(key string, value []byte) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()
	if key == "" {
		return errors.New("store: empty meta key")
	}
	return s.db.Set(db.MetaKey(key), value)
}

// GetMeta returns the value previously stored under key.
func (s *MemStore) GetMeta(key string) ([]byte, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.exit()
	val, err := s.db.Get(db.MetaKey(key))
	if errors.Is(err, db.ErrNotFound) {
		return nil, ErrNotFound
	}
	return val, err
}
