package db

import (
	"context"
	"errors"
	"strings"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
)

var _ ds.Batching = (*PebbleDatastore)(nil)

// PebbleDatastore implements the IPFS Datastore and Batching interfaces using Pebble.
type PebbleDatastore struct {
	db *pebble.DB
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
	return pd.db.Set(key.Bytes(), val, pebble.Sync)
}

// Delete removes the key and its value.
func (pd *PebbleDatastore) Delete(ctx context.Context, key ds.Key) error {
	err := pd.db.Delete(key.Bytes(), pebble.Sync)
	if err != nil && errors.Is(err, pebble.ErrNotFound) {
		return nil
	}
	return err
}

// Query performs a query on the datastore.
func (pd *PebbleDatastore) Query(ctx context.Context, q dsq.Query) (dsq.Results, error) {
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

// Close closes the underlying Pebble database.
func (pd *PebbleDatastore) Close() error {
	return pd.db.Close()
}

// Batch returns a new batching write interface.
func (pd *PebbleDatastore) Batch(ctx context.Context) (ds.Batch, error) {
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
	return pb.b.Set(key.Bytes(), val, pebble.Sync)
}

func (pb *pebbleBatch) Delete(ctx context.Context, key ds.Key) error {
	return pb.b.Delete(key.Bytes(), pebble.Sync)
}

func (pb *pebbleBatch) Commit(ctx context.Context) error {
	return pb.b.Commit(pebble.Sync)
}
