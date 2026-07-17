package herald

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// TestHerald_RootsAnnouncesEntryNodesNotLeaves verifies that the
// roots strategy announces sealed roots and reachable MemFS entry
// nodes (directories + file envelopes) but NOT the raw content
// leaves or DAGPB intermediates. This is the behaviour that keeps
// DHT load proportional to the number of addressable objects
// rather than to total bytes stored.
func TestHerald_RootsAnnouncesEntryNodesNotLeaves(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bs, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = bs.Close() })

	// Build a directory with two multi-block files. Each file is
	// large enough to span several raw leaves, so the leaf count
	// dominates the entry-node count.
	b := memfs.NewBuilder(bs)
	fileA, err := b.AddFile("a.bin", bytes.NewReader(bytes.Repeat([]byte{0x01}, 3*memfs.DefaultBlockSize)), 0o644, time.Unix(1700000000, 0), "application/octet-stream")
	if err != nil {
		t.Fatalf("AddFile a: %v", err)
	}
	fileB, err := b.AddFile("b.bin", bytes.NewReader(bytes.Repeat([]byte{0x02}, 2*memfs.DefaultBlockSize)), 0o644, time.Unix(1700000000, 0), "application/octet-stream")
	if err != nil {
		t.Fatalf("AddFile b: %v", err)
	}
	dir, err := b.AddDir("dir", []memfs.DirEntry{
		{Name: "a.bin", Mid: fileA.MID, Type: memfs.TypeFile, Size: fileA.Size},
		{Name: "b.bin", Mid: fileB.MID, Type: memfs.TypeFile, Size: fileB.Size},
	}, 0o755, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("AddDir: %v", err)
	}
	if err := bs.Seal(dir.MID, true); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	prov := &fakeProvider{}
	h, err := New(Config{
		Store:    bs,
		DHT:      prov,
		Strategy: StrategyRoots,
		Interval: time.Hour,
		Rate:     1000, // no throttling in test
		Burst:    64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	h.RunOnce(ctx)

	// Expected entry nodes: the DIR root + two FILE envelopes = 3.
	// The raw leaves (5 of them) must NOT be announced.
	prov.mu.Lock()
	announced := make([]mid.MID, len(prov.provided))
	copy(announced, prov.provided)
	prov.mu.Unlock()

	if len(announced) != 3 {
		t.Fatalf("announced %d MIDs, want 3 (dir + 2 file envelopes); got %v", len(announced), announced)
	}
	for _, m := range announced {
		if m.Codec() != mid.CodecMemFS {
			t.Errorf("announced a non-MemFS MID (codec %#x): %s", m.Codec(), m)
		}
	}

	// Both file envelopes and the dir root must be present.
	want := map[string]bool{
		dir.MID.String():   false,
		fileA.MID.String(): false,
		fileB.MID.String(): false,
	}
	for _, m := range announced {
		if _, ok := want[m.String()]; ok {
			want[m.String()] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("expected entry node %s to be announced, but it was not", k)
		}
	}
}

// TestHerald_RootsBareFile verifies that a single bare file root
// (not wrapped in a directory) is announced exactly once: the
// FILE envelope, not its raw leaves.
func TestHerald_RootsBareFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bs, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = bs.Close() })

	b := memfs.NewBuilder(bs)
	file, err := b.AddFile("solo.bin", bytes.NewReader(bytes.Repeat([]byte{0x07}, 4*memfs.DefaultBlockSize)), 0o644, time.Unix(1700000000, 0), "application/octet-stream")
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err := bs.Seal(file.MID, true); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	prov := &fakeProvider{}
	h, err := New(Config{
		Store:    bs,
		DHT:      prov,
		Strategy: StrategyRoots,
		Interval: time.Hour,
		Rate:     1000,
		Burst:    64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	h.RunOnce(ctx)

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.provided) != 1 {
		t.Fatalf("announced %d MIDs, want 1 (the file envelope); got %v", len(prov.provided), prov.provided)
	}
	if !prov.provided[0].Equal(file.MID) {
		t.Errorf("announced %s, want file root %s", prov.provided[0], file.MID)
	}
}
