package db

import (
	"errors"
	"testing"
)

// TestDB_UseAfterClose verifies the DB wrapper returns ErrClosed
// (never panics) when an operation races Close. Pebble panics on
// use-after-close; the RWMutex guard converts that into a
// recoverable error so an in-flight request racing graceful
// shutdown fails cleanly instead of crashing the process.
func TestDB_UseAfterClose(t *testing.T) {
	db, err := Open(Options{InMemory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	key := []byte("/b/some-key")
	if err := db.Set(key, []byte("v")); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close is idempotent.
	if err := db.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	if _, err := db.Get(key); !errors.Is(err, ErrClosed) {
		t.Errorf("Get after close: got %v, want ErrClosed", err)
	}
	if _, err := db.Has(key); !errors.Is(err, ErrClosed) {
		t.Errorf("Has after close: got %v, want ErrClosed", err)
	}
	if err := db.Set(key, []byte("v")); !errors.Is(err, ErrClosed) {
		t.Errorf("Set after close: got %v, want ErrClosed", err)
	}
	if err := db.Delete(key); !errors.Is(err, ErrClosed) {
		t.Errorf("Delete after close: got %v, want ErrClosed", err)
	}
	if _, err := db.NewIter(); !errors.Is(err, ErrClosed) {
		t.Errorf("NewIter after close: got %v, want ErrClosed", err)
	}
}
