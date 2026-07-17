package db

import (
	"context"
	"bytes"
	"testing"

	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
)

// TestPebbleDatastore_UseAfterClose verifies that operations
// invoked after Close return ErrClosed instead of panicking.
// This guards the shutdown race where an in-flight request (e.g.
// a hijacked WebSocket handler still driving a DHT lookup) reaches
// the datastore after Close: Pebble panics on use-after-close, so
// the wrapper must convert that into a recoverable error.
func TestPebbleDatastore_UseAfterClose(t *testing.T) {
	ctx := context.Background()
	pds, err := NewPebbleDatastore("", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	key := ds.NewKey("/k")
	if err := pds.Put(ctx, key, []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := pds.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Second Close is idempotent.
	if err := pds.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	if _, err := pds.Get(ctx, key); err != ErrClosed {
		t.Errorf("Get after close: got %v, want ErrClosed", err)
	}
	if _, err := pds.Has(ctx, key); err != ErrClosed {
		t.Errorf("Has after close: got %v, want ErrClosed", err)
	}
	if _, err := pds.GetSize(ctx, key); err != ErrClosed {
		t.Errorf("GetSize after close: got %v, want ErrClosed", err)
	}
	if err := pds.Put(ctx, key, []byte("v")); err != ErrClosed {
		t.Errorf("Put after close: got %v, want ErrClosed", err)
	}
	if err := pds.Delete(ctx, key); err != ErrClosed {
		t.Errorf("Delete after close: got %v, want ErrClosed", err)
	}
	if _, err := pds.Query(ctx, dsq.Query{}); err != ErrClosed {
		t.Errorf("Query after close: got %v, want ErrClosed", err)
	}
	if _, err := pds.Batch(ctx); err != ErrClosed {
		t.Errorf("Batch after close: got %v, want ErrClosed", err)
	}
}

func TestPebbleDatastore_BasicOps(t *testing.T) {
	ctx := context.Background()
	pds, err := NewPebbleDatastore("", true) // in-memory
	if err != nil {
		t.Fatalf("failed to create Pebble datastore: %v", err)
	}
	defer pds.Close()

	key := ds.NewKey("/test/key1")
	val := []byte("hello world")

	// Get missing key
	_, err = pds.Get(ctx, key)
	if err != ds.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Put
	err = pds.Put(ctx, key, val)
	if err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	// Has
	has, err := pds.Has(ctx, key)
	if err != nil || !has {
		t.Fatalf("expected key to exist: err=%v, has=%v", err, has)
	}

	// Get
	got, err := pds.Get(ctx, key)
	if err != nil || !bytes.Equal(got, val) {
		t.Fatalf("get returned invalid value: err=%v, got=%q", err, got)
	}

	// Size
	sz, err := pds.GetSize(ctx, key)
	if err != nil || sz != len(val) {
		t.Fatalf("expected size %d, got %d: err=%v", len(val), sz, err)
	}

	// Query
	q := dsq.Query{Prefix: "/test"}
	res, err := pds.Query(ctx, q)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	entries, err := res.Rest()
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: err=%v", len(entries), err)
	}
	if entries[0].Key != key.String() || !bytes.Equal(entries[0].Value, val) {
		t.Fatalf("invalid query entry: %v", entries[0])
	}

	// Delete
	err = pds.Delete(ctx, key)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Has after delete
	has, err = pds.Has(ctx, key)
	if err != nil || has {
		t.Fatalf("expected key to be deleted: err=%v, has=%v", err, has)
	}
}

func TestPebbleDatastore_Batch(t *testing.T) {
	ctx := context.Background()
	pds, err := NewPebbleDatastore("", true) // in-memory
	if err != nil {
		t.Fatalf("failed to create Pebble datastore: %v", err)
	}
	defer pds.Close()

	batch, err := pds.Batch(ctx)
	if err != nil {
		t.Fatalf("batch creation failed: %v", err)
	}

	k1 := ds.NewKey("/test/b1")
	v1 := []byte("val1")
	k2 := ds.NewKey("/test/b2")
	v2 := []byte("val2")

	_ = batch.Put(ctx, k1, v1)
	_ = batch.Put(ctx, k2, v2)

	// Keys shouldn't exist before commit
	has, _ := pds.Has(ctx, k1)
	if has {
		t.Fatal("expected k1 to not exist yet")
	}

	err = batch.Commit(ctx)
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Keys should exist after commit
	got1, err := pds.Get(ctx, k1)
	if err != nil || !bytes.Equal(got1, v1) {
		t.Fatal("failed to retrieve k1 after commit")
	}
}
