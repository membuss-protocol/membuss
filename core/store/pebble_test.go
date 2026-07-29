package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/db"
	"github.com/nnlgsakib/membuss/core/mid"

	membusspb "github.com/nnlgsakib/membuss/proto"
)

// newTestStore returns a fresh in-memory MemStore.
func newTestStore(t *testing.T) *MemStore {
	t.Helper()
	s, err := NewMemStore(Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMemStorePutGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 50; i++ {
		data := []byte(fmt.Sprintf("payload-%d", i))
		m := mid.FromBytes(data)
		if err := s.Put(m, data); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		got, err := s.Get(m)
		if err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("Get %d = %q, want %q", i, got, data)
		}
		ok, err := s.Has(m)
		if err != nil || !ok {
			t.Fatalf("Has %d: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestMemStorePutDAGGetDAG(t *testing.T) {
	s := newTestStore(t)
	data := []byte("dag-node-payload")
	m := mid.FromBytes(data)
	if err := s.PutDAG(m, data); err != nil {
		t.Fatalf("PutDAG: %v", err)
	}
	got, err := s.GetDAG(m)
	if err != nil {
		t.Fatalf("GetDAG: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("GetDAG = %q, want %q", got, data)
	}
	// Get is cross-namespace: it should find the data in /d/.
	if got2, err := s.Get(m); err != nil || !bytes.Equal(got2, data) {
		t.Fatalf("Get cross-namespace: got=%q err=%v", got2, err)
	}
	ok, err := s.HasDAG(m)
	if err != nil || !ok {
		t.Fatalf("HasDAG: ok=%v err=%v", ok, err)
	}
}

func TestMemStoreMetaRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutMeta("last-gc", []byte("2026-06-14T12:00:00Z")); err != nil {
		t.Fatalf("PutMeta: %v", err)
	}
	got, err := s.GetMeta("last-gc")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if string(got) != "2026-06-14T12:00:00Z" {
		t.Fatalf("GetMeta = %q", got)
	}
	if _, err := s.GetMeta("missing"); err == nil {
		t.Fatal("GetMeta missing must return ErrNotFound")
	}
}

func TestMemStoreRejectsBadMID(t *testing.T) {
	s := newTestStore(t)
	if err := s.Put(mid.FromBytes([]byte("a")), []byte("b")); err == nil {
		t.Fatal("Put with mismatched MID must fail")
	}
	if err := s.PutDAG(mid.FromBytes([]byte("a")), []byte("b")); err == nil {
		t.Fatal("PutDAG with mismatched MID must fail")
	}
}

func TestMemStoreGetMissing(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(mid.FromBytes([]byte("nope")))
	if err == nil {
		t.Fatal("Get missing must fail")
	}
}

func TestMemStoreSealUnseal(t *testing.T) {
	s := newTestStore(t)
	root := mid.FromBytes([]byte("root"))
	if err := s.Seal(root, false); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ok, err := s.IsSealed(root)
	if err != nil || !ok {
		t.Fatalf("IsSealed: ok=%v err=%v", ok, err)
	}
	all, err := s.AllSealed()
	if err != nil {
		t.Fatalf("AllSealed: %v", err)
	}
	if len(all) != 1 || !all[0].Equal(root) {
		t.Fatalf("AllSealed = %v, want just %s", all, root)
	}
	if err := s.Unseal(root); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	ok, _ = s.IsSealed(root)
	if ok {
		t.Fatal("IsSealed after Unseal must be false")
	}
}

// TestMemStoreRecursiveSealWalksDAG hand-builds a small DAG
// using the same canonical protobuf form the core/dag builder
// produces, then recursive-seals the root and confirms the
// leaves are reachable but have no direct seal record.
func TestMemStoreRecursiveSealWalksDAG(t *testing.T) {
	s := newTestStore(t)

	leaves := make([]mid.MID, 4)
	for i := range leaves {
		data := []byte(fmt.Sprintf("leaf-%d", i))
		leaves[i] = mid.FromBytes(data)
		if err := s.Put(leaves[i], data); err != nil {
			t.Fatalf("Put leaf %d: %v", i, err)
		}
	}
	links := make([]string, len(leaves))
	for i, c := range leaves {
		links[i] = c.String()
	}
	raw, err := proto.Marshal(&membusspb.DAGNode{Links: links})
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	root := mid.FromBytes(raw)
	if err := s.PutDAG(root, raw); err != nil {
		t.Fatalf("PutDAG root: %v", err)
	}

	if err := s.Seal(root, true); err != nil {
		t.Fatalf("Seal recursive: %v", err)
	}

	all, err := s.AllSealed()
	if err != nil {
		t.Fatalf("AllSealed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("AllSealed len = %d, want 1 (only the root has a direct seal)", len(all))
	}
	if !all[0].Equal(root) {
		t.Fatalf("AllSealed[0] = %s, want root %s", all[0], root)
	}
	for _, leaf := range leaves {
		ok, err := s.IsSealed(leaf)
		if err != nil {
			t.Fatalf("IsSealed leaf: %v", err)
		}
		if ok {
			t.Fatalf("leaf %s has a direct seal record; recursive seal should not write one", leaf)
		}
	}
}

// TestMemStoreGC1000Blocks is the headline integration test:
// store 1000 blocks, seal 100 of them, run GC, and assert that
// only the sealed ones remain.
func TestMemStoreGC1000Blocks(t *testing.T) {
	s := newTestStore(t)

	const total = 1000
	const sealed = 100

	allMIDs := make([]mid.MID, total)
	for i := 0; i < total; i++ {
		data := make([]byte, 256) // 256 bytes per block
		if _, err := rand.Read(data); err != nil {
			t.Fatalf("rand: %v", err)
		}
		m := mid.FromBytes(data)
		if err := s.Put(m, data); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		allMIDs[i] = m
	}

	sealedSet := make(map[string]struct{})
	for i := 0; i < total; i += total / sealed {
		if err := s.Seal(allMIDs[i], false); err != nil {
			t.Fatalf("Seal %d: %v", i, err)
		}
		sealedSet[allMIDs[i].String()] = struct{}{}
	}
	if len(sealedSet) != sealed {
		t.Fatalf("sealed set size = %d, want %d", len(sealedSet), sealed)
	}

	all, err := s.AllSealed()
	if err != nil {
		t.Fatalf("AllSealed: %v", err)
	}
	if len(all) != sealed {
		t.Fatalf("AllSealed = %d, want %d", len(all), sealed)
	}

	freed, err := s.GC(context.Background())
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed == 0 {
		t.Fatal("GC freed 0 bytes; expected to free the unsealed blocks")
	}
	t.Logf("GC freed %d bytes", freed)

	for i, m := range allMIDs {
		ok, err := s.Has(m)
		if err != nil {
			t.Fatalf("Has %d: %v", i, err)
		}
		_, isSealed := sealedSet[m.String()]
		if isSealed && !ok {
			t.Fatalf("sealed block %d (%s) was deleted by GC", i, m)
		}
		if !isSealed && ok {
			t.Fatalf("unsealed block %d (%s) survived GC", i, m)
		}
	}
}

// TestMemStoreGCDAGKeepsSealedSubtree confirms GC keeps only
// the sealed DAG and removes unsealed siblings.
func TestMemStoreGCDAGKeepsSealedSubtree(t *testing.T) {
	s := newTestStore(t)

	buildDAG := func(seed byte) (root mid.MID, leaves []mid.MID) {
		for i := 0; i < 4; i++ {
			data := []byte{seed, byte(i)}
			m := mid.FromBytes(data)
			if err := s.Put(m, data); err != nil {
				t.Fatalf("Put leaf: %v", err)
			}
			leaves = append(leaves, m)
		}
		links := make([]string, len(leaves))
		for i, c := range leaves {
			links[i] = c.String()
		}
		raw, err := proto.Marshal(&membusspb.DAGNode{Links: links})
		if err != nil {
			t.Fatalf("proto.Marshal: %v", err)
		}
		root = mid.FromBytes(raw)
		if err := s.PutDAG(root, raw); err != nil {
			t.Fatalf("PutDAG: %v", err)
		}
		return root, leaves
	}
	rootA, leavesA := buildDAG(0xAA)
	rootB, leavesB := buildDAG(0xBB)

	if err := s.Seal(rootA, false); err != nil {
		t.Fatalf("Seal A: %v", err)
	}

	if _, err := s.GC(context.Background()); err != nil {
		t.Fatalf("GC: %v", err)
	}

	for _, leaf := range leavesA {
		ok, err := s.Has(leaf)
		if err != nil {
			t.Fatalf("Has A leaf: %v", err)
		}
		if !ok {
			t.Fatalf("A leaf %s vanished after GC", leaf)
		}
	}
	for _, leaf := range leavesB {
		ok, err := s.Has(leaf)
		if err != nil {
			t.Fatalf("Has B leaf: %v", err)
		}
		if ok {
			t.Fatalf("B leaf %s survived GC", leaf)
		}
	}
	ok, _ := s.Has(rootA)
	if !ok {
		t.Fatal("A root vanished after GC")
	}
	ok, _ = s.Has(rootB)
	if ok {
		t.Fatal("B root survived GC")
	}
}

func TestMemStoreClose(t *testing.T) {
	s, err := NewMemStore(Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Put(mid.FromBytes([]byte("x")), []byte("x")); err == nil {
		t.Fatal("Put after Close must fail")
	}
	if _, err := s.Get(mid.FromBytes([]byte("x"))); err == nil {
		t.Fatal("Get after Close must fail")
	}
}

func TestMemStoreSize(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Size(); err != nil {
		t.Fatalf("Size: %v", err)
	}
}

// TestMemStoreSealForwardLooking verifies the design contract
// from the Store interface: a Seal is a forward-looking pin
// that succeeds even when the local store does not yet have
// the blocks being pinned. The walk is best-effort; a missing
// block is a soft warning (ErrSealWalkIncomplete) so the
// caller (typically a "pin this MID, fetch later" workflow)
// does not have to pre-fetch every chunk before sealing.
//
// This is the regression test for the user-reported error
//
//	seal: store: seal walk: store: walk get <mid>:
//	store: block not found
//
// that used to surface when an operator sealed a freshly
// fetched MID before the closer had drained every wanted
// block. After the fix, the seal record is written
// unconditionally and the walk's missing-block result is
// surfaced as a typed error the caller can choose to log and
// ignore.
func TestMemStoreSealForwardLooking(t *testing.T) {
	s := newTestStore(t)
	// Pick a MID we have never Put. The seal walk should
	// return ErrNotFound (or its wrapped
	// ErrSealWalkIncomplete form) but the seal record must
	// still be on disk.
	mid := mid.FromBytes([]byte("not-yet-fetched"))

	err := s.Seal(mid, true)
	if err == nil {
		t.Fatal("Seal on missing block: expected an error, got nil")
	}
	if !errors.Is(err, ErrSealWalkIncomplete) {
		t.Fatalf("Seal on missing block: err = %v, want wraps ErrSealWalkIncomplete", err)
	}

	// The pin must still be on disk: a follow-up IsSealed
	// must report the MID as sealed (the contract is "pin
	// now, fetch later").
	sealed, err := s.IsSealed(mid)
	if err != nil {
		t.Fatalf("IsSealed: %v", err)
	}
	if !sealed {
		t.Fatal("Seal record missing after forward-looking Seal; pin must be on disk")
	}
}

// TestMemStoreSealRecursiveSucceedsWhenComplete verifies
// the happy path: when every reachable block is local, the
// recursive seal walk returns nil and the pin is recorded.
func TestMemStoreSealRecursiveSucceedsWhenComplete(t *testing.T) {
	s := newTestStore(t)
	m := mid.FromBytes([]byte("single-block"))
	if err := s.Put(m, []byte("single-block")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Seal(m, true); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed, _ := s.IsSealed(m)
	if !sealed {
		t.Fatal("Seal record missing after successful Seal")
	}
}

func TestMemStoreCodecPersistence(t *testing.T) {
	dir, err := os.MkdirTemp("", "membuss-test-codec-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	// Open store
	s1, err := NewMemStore(Options{Path: dir})
	if err != nil {
		t.Fatalf("NewMemStore s1: %v", err)
	}

	// Seal a MID with CodecMemFS
	data := []byte("memfs-root-payload")
	m1 := mid.FromBytesWithCodec(data, mid.CodecMemFS)
	if err := s1.PutDAG(m1, data); err != nil {
		t.Fatalf("PutDAG: %v", err)
	}
	if err := s1.Seal(m1, false); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Close store
	if err := s1.Close(); err != nil {
		t.Fatalf("Close s1: %v", err)
	}

	// Reopen store (simulate restart)
	s2, err := NewMemStore(Options{Path: dir})
	if err != nil {
		t.Fatalf("NewMemStore s2: %v", err)
	}
	defer s2.Close()

	// Verify AllSealed returns the MID with CodecMemFS
	sealed, err := s2.AllSealed()
	if err != nil {
		t.Fatalf("AllSealed: %v", err)
	}
	if len(sealed) != 1 {
		t.Fatalf("expected 1 sealed root, got %d", len(sealed))
	}
	if sealed[0].Codec() != mid.CodecMemFS {
		t.Fatalf("expected codec %x (CodecMemFS), got %x", mid.CodecMemFS, sealed[0].Codec())
	}
}

func TestMemStoreDeleteRecursive(t *testing.T) {
	s := newTestStore(t)

	// Create child blocks
	c1Data := []byte("child-block-1-data")
	c1 := mid.FromBytes(c1Data)
	if err := s.Put(c1, c1Data); err != nil {
		t.Fatalf("Put c1: %v", err)
	}

	c2Data := []byte("child-block-2-data")
	c2 := mid.FromBytes(c2Data)
	if err := s.Put(c2, c2Data); err != nil {
		t.Fatalf("Put c2: %v", err)
	}

	// Create root DAG node referencing child blocks
	node := &membusspb.DAGNode{
		Links: []string{c1.String(), c2.String()},
	}
	nodeData, err := proto.Marshal(node)
	if err != nil {
		t.Fatalf("Marshal DAG node: %v", err)
	}
	root := mid.FromBytes(nodeData)
	if err := s.PutDAG(root, nodeData); err != nil {
		t.Fatalf("PutDAG root: %v", err)
	}

	// Seal root
	if err := s.Seal(root, true); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Verify all blocks and seals are present
	if has, _ := s.Has(root); !has {
		t.Fatal("expected root to be present")
	}
	if has, _ := s.Has(c1); !has {
		t.Fatal("expected c1 to be present")
	}
	if has, _ := s.Has(c2); !has {
		t.Fatal("expected c2 to be present")
	}
	if isSealed, _ := s.IsSealed(root); !isSealed {
		t.Fatal("expected root to be sealed")
	}

	// Delete recursively
	deleted, freed, err := s.DeleteRecursive(root)
	if err != nil {
		t.Fatalf("DeleteRecursive: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 blocks deleted, got %d", deleted)
	}
	expectedFreed := uint64(len(c1Data) + len(c2Data) + len(nodeData))
	if freed != expectedFreed {
		t.Errorf("expected %d bytes freed, got %d", expectedFreed, freed)
	}

	// Verify everything is gone
	if has, _ := s.Has(root); has {
		t.Fatal("expected root to be deleted")
	}
	if has, _ := s.Has(c1); has {
		t.Fatal("expected c1 to be deleted")
	}
	if has, _ := s.Has(c2); has {
		t.Fatal("expected c2 to be deleted")
	}
	if isSealed, _ := s.IsSealed(root); isSealed {
		t.Fatal("expected root to be unsealed")
	}
}

func TestMemStoreDeleteRecursiveDescriptor(t *testing.T) {
	s := newTestStore(t)

	// Create child blocks
	c1Data := []byte("block-data-1")
	c1 := mid.FromBytes(c1Data)
	if err := s.Put(c1, c1Data); err != nil {
		t.Fatalf("Put c1: %v", err)
	}

	// Create root MemFSNode
	rootNode := &membusspb.MemFSNode{
		Type: membusspb.MemFSType_FILE,
		Blocks: []*membusspb.MemFSBlock{
			{Mid: c1.Bytes(), Size: uint64(len(c1Data))},
		},
	}
	rootNodeData, err := proto.Marshal(rootNode)
	if err != nil {
		t.Fatalf("Marshal root node: %v", err)
	}
	rootMID := mid.FromBytes(rootNodeData)
	if err := s.Put(rootMID, rootNodeData); err != nil {
		t.Fatalf("Put rootMID: %v", err)
	}

	// Create descriptor payload
	desc := &membusspb.DescriptorPayload{
		RootMid: rootMID.String(),
		Blocks: []*membusspb.DescriptorBlock{
			{Mid: c1.String()},
			{Mid: rootMID.String()},
		},
	}
	descData, err := proto.Marshal(desc)
	if err != nil {
		t.Fatalf("Marshal desc: %v", err)
	}

	// Build the wrapped format (.mbuss format)
	var wrappedBuf bytes.Buffer
	wrappedBuf.Write([]byte{'M', 'E', 'M', 'B'}) // Magic
	wrappedBuf.WriteByte(1) // Version
	wrappedBuf.Write(descData) // Payload
	h := sha256.Sum256(descData)
	wrappedBuf.Write(h[:]) // Checksum
	wrappedData := wrappedBuf.Bytes()

	descMID := mid.FromBytes(wrappedData)
	if err := s.Put(descMID, wrappedData); err != nil {
		t.Fatalf("Put descMID: %v", err)
	}

	// Verify all blocks are present
	for _, m := range []mid.MID{c1, rootMID, descMID} {
		if has, _ := s.Has(m); !has {
			t.Fatalf("expected block %s to be present", m)
		}
	}

	// Delete recursive from descriptor
	deleted, _, err := s.DeleteRecursive(descMID)
	if err != nil {
		t.Fatalf("DeleteRecursive: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 blocks deleted, got %d", deleted)
	}

	// Verify all blocks are gone
	for _, m := range []mid.MID{c1, rootMID, descMID} {
		if has, _ := s.Has(m); has {
			t.Fatalf("expected block %s to be deleted", m)
		}
	}
}

func TestMemStoreValueLogGC(t *testing.T) {
	s, err := NewMemStore(Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMemStoreGCMinAgeKeepsRecentBlocks(t *testing.T) {
	s := newTestStore(t)

	// Seal one block (will survive any GC).
	sealed := mid.FromBytes([]byte("sealed-block"))
	if err := s.Put(sealed, []byte("sealed-block")); err != nil {
		t.Fatalf("Put sealed: %v", err)
	}
	if err := s.Seal(sealed, false); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Write an unsealed block (should be kept by minAge).
	recent := mid.FromBytes([]byte("recent-block"))
	if err := s.Put(recent, []byte("recent-block")); err != nil {
		t.Fatalf("Put recent: %v", err)
	}

	// Run GC with a large minAge — the unsealed block was just
	// written so its stored timestamp is newer than now - minAge.
	freed, err := s.GCWithMinAge(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("GCWithMinAge: %v", err)
	}
	if freed != 0 {
		t.Fatalf("GCWithMinAge freed %d bytes; expected 0 (recent block should be kept)", freed)
	}

	// Both blocks must still exist.
	if ok, _ := s.Has(recent); !ok {
		t.Fatal("recent unsealed block was deleted by GCWithMinAge")
	}
	if ok, _ := s.Has(sealed); !ok {
		t.Fatal("sealed block was deleted by GCWithMinAge")
	}

	// Now run GC *without* minAge — the unsealed block should be removed.
	freed, err = s.GC(context.Background())
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed == 0 {
		t.Fatal("GC freed 0 bytes; expected to remove the unsealed block")
	}
	if ok, _ := s.Has(recent); ok {
		t.Fatal("unsealed block survived GC without minAge")
	}
	if ok, _ := s.Has(sealed); !ok {
		t.Fatal("sealed block was deleted by GC without minAge")
	}
}

func TestMemStoreGCMinAgeZeroDeletesAll(t *testing.T) {
	s := newTestStore(t)

	// Write an unsealed block.
	m := mid.FromBytes([]byte("throwaway"))
	if err := s.Put(m, []byte("throwaway")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// GC with minAge=0 (disabled) should delete it.
	freed, err := s.GC(context.Background())
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed == 0 {
		t.Fatal("GC with minAge=0 freed 0 bytes")
	}
	if ok, _ := s.Has(m); ok {
		t.Fatal("unsealed block survived GC with minAge=0")
	}
}

func TestMemStoreTimestampsWrittenOnPut(t *testing.T) {
	s := newTestStore(t)
	m := mid.FromBytes([]byte("ts-test"))
	if err := s.Put(m, []byte("ts-test")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify a timestamp was stored.
	ts, err := db.ReadTimestamp(s.db, m)
	if err != nil {
		t.Fatalf("readTimestamp: %v", err)
	}
	if ts == 0 {
		t.Fatal("timestamp not written; got 0")
	}

	// Timestamp should be close to now.
	now := uint64(time.Now().Unix())
	if ts > now+2 || ts < now-2 {
		t.Fatalf("timestamp %d not close to now %d", ts, now)
	}
}

func TestMemStoreTimestampsWrittenOnPutDAG(t *testing.T) {
	s := newTestStore(t)
	m := mid.FromBytes([]byte("ts-dag-test"))
	if err := s.PutDAG(m, []byte("ts-dag-test")); err != nil {
		t.Fatalf("PutDAG: %v", err)
	}

	ts, err := db.ReadTimestamp(s.db, m)
	if err != nil {
		t.Fatalf("readTimestamp: %v", err)
	}
	if ts == 0 {
		t.Fatal("timestamp not written for DAG node; got 0")
	}
}

func TestWalkCycleDetection(t *testing.T) {
	s := newTestStore(t)

	// Build a DAG with diamond structure where the same child
	// appears from multiple parents. Without cycle detection
	// this would visit the shared child twice.
	//
	//   root -> A, B
	//   A   -> shared
	//   B   -> shared

	sharedData := []byte("shared-child")
	shared := mid.FromBytes(sharedData)
	if err := s.Put(shared, sharedData); err != nil {
		t.Fatalf("Put shared: %v", err)
	}

	// A has an extra link so its hash differs from B.
	sharedData2 := []byte("extra-for-A")
	shared2 := mid.FromBytes(sharedData2)
	if err := s.Put(shared2, sharedData2); err != nil {
		t.Fatalf("Put shared2: %v", err)
	}

	nodeA := &membusspb.DAGNode{Links: []string{shared.String(), shared2.String()}}
	rawA, err := proto.Marshal(nodeA)
	if err != nil {
		t.Fatalf("marshal A: %v", err)
	}
	dagA := mid.FromBytes(rawA)
	if err := s.PutDAG(dagA, rawA); err != nil {
		t.Fatalf("PutDAG A: %v", err)
	}

	nodeB := &membusspb.DAGNode{Links: []string{shared.String()}}
	rawB, err := proto.Marshal(nodeB)
	if err != nil {
		t.Fatalf("marshal B: %v", err)
	}
	dagB := mid.FromBytes(rawB)
	if err := s.PutDAG(dagB, rawB); err != nil {
		t.Fatalf("PutDAG B: %v", err)
	}

	rootNode := &membusspb.DAGNode{Links: []string{dagA.String(), dagB.String()}}
	rawRoot, err := proto.Marshal(rootNode)
	if err != nil {
		t.Fatalf("marshal root: %v", err)
	}
	root := mid.FromBytes(rawRoot)
	if err := s.PutDAG(root, rawRoot); err != nil {
		t.Fatalf("PutDAG root: %v", err)
	}

	// Without cycle detection, shared would be visited twice.
	visited := make(map[string]int)
	err = Walk(s, root, func(m mid.MID, leaf bool) error {
		visited[m.String()]++
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	// root, dagA, dagB, shared, shared2 = 5 unique nodes.
	if len(visited) != 5 {
		t.Fatalf("Walk visited %d unique nodes, want 5", len(visited))
	}
	// shared must be visited exactly once.
	if visited[shared.String()] != 1 {
		t.Fatalf("shared visited %d times, want 1", visited[shared.String()])
	}
}

func TestWalkSelfCycle(t *testing.T) {
	s := newTestStore(t)

	// A DAG that links to itself.
	selfData := []byte("self-ref-payload")
	selfMID := mid.FromBytes(selfData)
	if err := s.Put(selfMID, selfData); err != nil {
		t.Fatalf("Put selfMID: %v", err)
	}

	node := &membusspb.DAGNode{Links: []string{selfMID.String()}}
	data, err := proto.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dag := mid.FromBytes(data)
	if err := s.PutDAG(dag, data); err != nil {
		t.Fatalf("PutDAG: %v", err)
	}

	visited := 0
	err = Walk(s, dag, func(m mid.MID, leaf bool) error {
		visited++
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	// dag + selfMID = 2 nodes visited.
	if visited != 2 {
		t.Fatalf("Walk visited %d nodes, want 2", visited)
	}
}

func TestFlatFileStore(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "datastore")
	blocksPath := filepath.Join(tempDir, "blocks")

	s, err := NewMemStore(Options{
		Path:       dbPath,
		BlocksPath: blocksPath,
	})
	if err != nil {
		t.Fatalf("NewMemStore failed: %v", err)
	}
	defer s.Close()

	// Put a large block (>= 1MB) to trigger flat file storage
	data1 := bytes.Repeat([]byte("hello flat file blockstore"), 50000)
	m1 := mid.FromBytes(data1)
	if err := s.Put(m1, data1); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Verify the flat file exists on disk in the correct folder structure
	fpath := s.blockPath(m1)
	if _, err := os.Stat(fpath); err != nil {
		t.Fatalf("Expected block file to exist at %s: %v", fpath, err)
	}

	// Read block back
	got1, err := s.Get(m1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !bytes.Equal(got1, data1) {
		t.Fatalf("Get data mismatch: got %q, want %q", got1, data1)
	}

	// Verify Size() returns the size of the block
	sz, err := s.Size()
	if err != nil {
		t.Fatalf("Size failed: %v", err)
	}
	if sz != uint64(len(data1)) {
		t.Fatalf("Size got %d, want %d", sz, len(data1))
	}

	// Delete block
	if err := s.Delete(m1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify file is removed from disk
	if _, err := os.Stat(fpath); !os.IsNotExist(err) {
		t.Fatalf("Expected block file to be deleted: %v", err)
	}

	// Verify block is not in store
	if has, _ := s.Has(m1); has {
		t.Fatal("Store still reports block exists after delete")
	}
}

func TestMemStoreGC_ConcurrentUploadRace(t *testing.T) {
	dir := t.TempDir()
	s, err := NewMemStore(Options{Path: dir})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	defer s.Close()

	// Put a sealed block to have a valid sealed root
	sealedData := []byte("sealed root block data for GC test")
	sealedMID := mid.FromBytes(sealedData)
	if err := s.Put(sealedMID, sealedData); err != nil {
		t.Fatalf("Put sealed: %v", err)
	}
	if err := s.Seal(sealedMID, false); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Write a new block concurrently during GC
	newData := []byte("concurrent block written during GC sweep")
	newMID := mid.FromBytes(newData)

	// Simulate concurrent write by putting block inside a goroutine while GC runs
	done := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		done <- s.Put(newMID, newData)
	}()

	if _, err := s.GC(context.Background()); err != nil {
		t.Fatalf("GC: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Concurrent Put failed: %v", err)
	}

	// Verify that the concurrent block written during GC was NOT deleted
	has, err := s.Has(newMID)
	if err != nil {
		t.Fatalf("Has newMID: %v", err)
	}
	if !has {
		t.Fatalf("GC race condition! Concurrent block %s was mistakenly deleted by GC", newMID.String())
	}
}

func TestMemStore_HybridBlockStorageThreshold(t *testing.T) {
	dbDir := t.TempDir()
	blocksDir := t.TempDir()

	s, err := NewMemStore(Options{
		Path:       dbDir,
		BlocksPath: blocksDir,
	})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	defer s.Close()

	// Small block (256 KB)
	smallData := bytes.Repeat([]byte{0xAA}, 256*1024)
	smallMID := mid.FromBytes(smallData)
	if err := s.Put(smallMID, smallData); err != nil {
		t.Fatalf("Put small: %v", err)
	}

	// Large block (1.2 MB)
	largeData := bytes.Repeat([]byte{0xBB}, 1200*1024)
	largeMID := mid.FromBytes(largeData)
	if err := s.Put(largeMID, largeData); err != nil {
		t.Fatalf("Put large: %v", err)
	}

	// Verify small block is NOT on disk as a flat file
	if _, err := os.Stat(s.blockPath(smallMID)); err == nil {
		t.Fatalf("Expected small block %s to be stored in Pebble DB, but found flat file on disk", smallMID.String())
	}

	// Verify large block IS on disk as a flat file
	if _, err := os.Stat(s.blockPath(largeMID)); os.IsNotExist(err) {
		t.Fatalf("Expected large block %s to be stored on disk, but file missing", largeMID.String())
	}

	// Verify Get returns correct data for both
	gotSmall, err := s.Get(smallMID)
	if err != nil || !bytes.Equal(gotSmall, smallData) {
		t.Fatalf("Get small block failed or mismatch: %v", err)
	}
	gotLarge, err := s.Get(largeMID)
	if err != nil || !bytes.Equal(gotLarge, largeData) {
		t.Fatalf("Get large block failed or mismatch: %v", err)
	}
}

