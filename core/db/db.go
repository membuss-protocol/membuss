package db

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/nnlgsakib/membuss/core/mid"
)

// Key-prefix bytes.
const (
	PrefixBlock = "/b/"
	PrefixDAG   = "/d/"
	PrefixSeal  = "/s/"
	PrefixMeta  = "/m/"
)

// Logger is the interface for DB logging.
type Logger interface {
	Infof(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
}

// Options configures the DB.
type Options struct {
	Path     string
	InMemory bool
	ReadOnly bool
	Logger   Logger
}

// ErrClosed is returned by DB and PebbleDatastore operations
// invoked after Close. Pebble panics on use-after-close; this
// wrapper converts that into a recoverable error so an operation
// racing shutdown (e.g. a hijacked WebSocket handler still driving
// a DHT lookup) fails cleanly instead of crashing the process.
var ErrClosed = errors.New("db: closed")

// DB wraps the Pebble DB instance.
//
// A sync.RWMutex guards against use-after-close: read/write
// operations take the read lock and check the closed flag, while
// Close takes the write lock. This guarantees no operation is
// mid-flight when pebble.Close runs, so the "pebble: closed"
// panic cannot occur during graceful shutdown.
type DB struct {
	mu     sync.RWMutex
	pebble *pebble.DB
	closed bool
}

// Open opens a Pebble DB.
func Open(opts Options) (*DB, error) {
	if !opts.InMemory && filepath.Clean(opts.Path) == "" {
		return nil, errors.New("db: empty path")
	}

	pebbleOpts := &pebble.Options{
		ReadOnly: opts.ReadOnly,
	}
	if opts.InMemory {
		pebbleOpts.FS = vfs.NewMem()
	}
	if opts.Logger != nil {
		pebbleOpts.Logger = opts.Logger
	}

	pdb, err := pebble.Open(opts.Path, pebbleOpts)
	if err != nil {
		return nil, fmt.Errorf("db: open at %q: %w", opts.Path, err)
	}

	return &DB{pebble: pdb}, nil
}

// Close closes the Pebble DB. It is idempotent and takes the
// write lock so it cannot run concurrently with any in-flight
// operation, preventing the "pebble: closed" panic.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	return db.pebble.Close()
}

// Get retrieves the value for key. Copied to prevent transient memory issues.
func (db *DB) Get(key []byte) ([]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, ErrClosed
	}
	val, closer, err := db.pebble.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer closer.Close()
	out := make([]byte, len(val))
	copy(out, val)
	return out, nil
}

// Has checks key existence via iterator seek.
func (db *DB) Has(key []byte) (bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return false, ErrClosed
	}
	it, err := db.pebble.NewIter(nil)
	if err != nil {
		return false, err
	}
	defer it.Close()
	it.SeekGE(key)
	if it.Valid() && bytesEqual(it.Key(), key) {
		return true, nil
	}
	return false, nil
}

// Set writes a key/value pair.
func (db *DB) Set(key, value []byte) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return ErrClosed
	}
	return db.pebble.Set(key, value, pebble.Sync)
}

// Delete deletes a key.
func (db *DB) Delete(key []byte) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return ErrClosed
	}
	return db.pebble.Delete(key, pebble.Sync)
}

// Batch wraps Pebble batch operations.
type Batch struct {
	b *pebble.Batch
}

// NewBatch returns a new batch.
func (db *DB) NewBatch() *Batch {
	return &Batch{b: db.pebble.NewBatch()}
}

// Set adds a set operation to the batch.
func (b *Batch) Set(key, value []byte) error {
	return b.b.Set(key, value, pebble.Sync)
}

// Delete adds a delete operation to the batch.
func (b *Batch) Delete(key []byte) error {
	return b.b.Delete(key, pebble.Sync)
}

// Commit commits the batch.
func (b *Batch) Commit() error {
	return b.b.Commit(pebble.Sync)
}

// Close closes the batch.
func (b *Batch) Close() error {
	return b.b.Close()
}

// Iterator wraps Pebble iterator.
type Iterator struct {
	it *pebble.Iterator
}

// NewIter returns a new iterator.
func (db *DB) NewIter() (*Iterator, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, ErrClosed
	}
	it, err := db.pebble.NewIter(nil)
	if err != nil {
		return nil, err
	}
	return &Iterator{it: it}, nil
}

func (it *Iterator) First() bool            { return it.it.First() }
func (it *Iterator) Valid() bool            { return it.it.Valid() }
func (it *Iterator) Next() bool             { return it.it.Next() }
func (it *Iterator) SeekGE(key []byte) bool { return it.it.SeekGE(key) }
func (it *Iterator) Key() []byte            { return it.it.Key() }
func (it *Iterator) Value() []byte          { return it.it.Value() }
func (it *Iterator) Close() error           { return it.it.Close() }

// ErrNotFound is returned when key is not found.
var ErrNotFound = errors.New("db: key not found")

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Key format utilities
func BlockKey(m mid.MID) []byte {
	return append([]byte(PrefixBlock), m.Bytes()...)
}

func DagKey(m mid.MID) []byte {
	return append([]byte(PrefixDAG), m.Bytes()...)
}

func SealKey(m mid.MID) []byte {
	return append([]byte(PrefixSeal), m.Bytes()...)
}

func MetaKey(k string) []byte {
	return append([]byte(PrefixMeta), []byte(k)...)
}

// Timestamp utilities
func TimestampKey(m mid.MID) []byte {
	return MetaKey("ts/" + m.String())
}

func PutTimestamp(w *Batch, m mid.MID) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(time.Now().Unix()))
	return w.Set(TimestampKey(m), buf[:])
}

func ReadTimestamp(r *DB, m mid.MID) (uint64, error) {
	val, err := r.Get(TimestampKey(m))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if len(val) < 8 {
		return 0, errors.New("db: corrupt timestamp size")
	}
	return binary.BigEndian.Uint64(val), nil
}
