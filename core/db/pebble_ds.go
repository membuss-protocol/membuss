package db

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
)

var _ ds.Batching = (*PebbleDatastore)(nil)

// PebbleDatastore implements the IPFS Datastore and Batching interfaces using Pebble.
//
// A sync.RWMutex guards against use-after-close: every operation
// takes a read lock and checks the closed flag, while Close takes
// the write lock. This guarantees no operation is mid-flight when
// pebble.Close runs, so the "pebble: closed" panic cannot occur
// during graceful shutdown.
type PebbleDatastore struct {
	mu     sync.RWMutex
	db     *pebble.DB
	closed bool
}

// NewPebbleDatastore creates a new Pebble-backed Datastore.
func NewPebbleDatastore(path string, inMemory bool) (*PebbleDatastore, error) {
	pebbleOpts := &pebble.Options{}
	if inMemory {
		pebbleOpts.FS = vfs.NewMem()
	}
	db, err := pebble.Open(path, pebbleOpts)
	if err != nil {
		return nil, err
	}
	return &PebbleDatastore{db: db}, nil
}

// Get retrieves the value for the given key.
func (pd *PebbleDatastore) Get(ctx context.Context, key ds.Key) ([]byte, error) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return nil, ErrClosed
	}
	val, closer, err := pd.db.Get(key.Bytes())
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ds.ErrNotFound
		}
		return nil, err
	}
	defer closer.Close()
	out := make([]byte, len(val))
	copy(out, val)
	return out, nil
}

// Has checks if the given key exists.
func (pd *PebbleDatastore) Has(ctx context.Context, key ds.Key) (bool, error) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return false, ErrClosed
	}
	_, closer, err := pd.db.Get(key.Bytes())
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	closer.Close()
	return true, nil
}

// GetSize returns the size of the value for the given key.
func (pd *PebbleDatastore) GetSize(ctx context.Context, key ds.Key) (int, error) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return -1, ErrClosed
	}
	val, closer, err := pd.db.Get(key.Bytes())
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return -1, ds.ErrNotFound
		}
		return -1, err
	}
	size := len(val)
	closer.Close()
	return size, nil
}

// Put writes the value for the given key.
func (pd *PebbleDatastore) Put(ctx context.Context, key ds.Key, val []byte) error {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return ErrClosed
	}
	return pd.db.Set(key.Bytes(), val, pebble.NoSync)
}

// Delete removes the key and its value.
func (pd *PebbleDatastore) Delete(ctx context.Context, key ds.Key) error {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return ErrClosed
	}
	err := pd.db.Delete(key.Bytes(), pebble.NoSync)
	if err != nil && errors.Is(err, pebble.ErrNotFound) {
		return nil
	}
	return err
}

// Query performs a query on the datastore.
func (pd *PebbleDatastore) Query(ctx context.Context, q dsq.Query) (dsq.Results, error) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return nil, ErrClosed
	}
	it, err := pd.db.NewIter(nil)
	if err != nil {
		return nil, err
	}

	var entries []dsq.Entry
	prefix := q.Prefix
	if prefix != "" {
		if prefix[0] != '/' {
			prefix = "/" + prefix
		}
		it.SeekGE([]byte(prefix))
	} else {
		it.First()
	}

	for ; it.Valid(); it.Next() {
		if ctx.Err() != nil {
			it.Close()
			return nil, ctx.Err()
		}

		k := string(it.Key())
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			break
		}

		entry := dsq.Entry{Key: k}
		if !q.KeysOnly {
			entry.Value = append([]byte(nil), it.Value()...)
		}
		entries = append(entries, entry)
	}
	it.Close()

	return dsq.ResultsWithEntries(q, entries), nil
}

// Sync flushes database writes to disk. Since Pebble DB handles flushing on commits/syncs, this is a no-op.
func (pd *PebbleDatastore) Sync(ctx context.Context, prefix ds.Key) error {
	return nil
}

// Close closes the underlying Pebble database. It is idempotent
// and takes the write lock so it cannot run concurrently with any
// in-flight operation, preventing the "pebble: closed" panic.
func (pd *PebbleDatastore) Close() error {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	if pd.closed {
		return nil
	}
	pd.closed = true
	return pd.db.Close()
}

// Batch returns a new batching write interface.
func (pd *PebbleDatastore) Batch(ctx context.Context) (ds.Batch, error) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	if pd.closed {
		return nil, ErrClosed
	}
	return &pebbleBatch{
		b:  pd.db.NewBatch(),
		ds: pd,
	}, nil
}

type pebbleBatch struct {
	b  *pebble.Batch
	ds *PebbleDatastore
}

func (pb *pebbleBatch) Put(ctx context.Context, key ds.Key, val []byte) error {
	return pb.b.Set(key.Bytes(), val, pebble.NoSync)
}

func (pb *pebbleBatch) Delete(ctx context.Context, key ds.Key) error {
	return pb.b.Delete(key.Bytes(), pebble.NoSync)
}

func (pb *pebbleBatch) Commit(ctx context.Context) error {
	pb.ds.mu.RLock()
	defer pb.ds.mu.RUnlock()
	if pb.ds.closed {
		return ErrClosed
	}
	defer pb.b.Close()
	return pb.b.Commit(pebble.NoSync)
}
