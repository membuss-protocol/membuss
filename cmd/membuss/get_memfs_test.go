// Tests that daemonBackend.Get / GetWithProgress correctly
// resolve MemFS FILE nodes (the format produced by the HTTP
// /api/v1/add path and by every directory child), not just
// plain DAGNode trees. A regression here surfaces as a
// downloaded 0-byte file.
package main

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// newInMemBackend returns a daemonBackend backed by an
// in-memory store, with no networking wired up. It is enough
// to exercise the local resolve path of Get / GetWithProgress.
func newInMemBackend(t *testing.T) *daemonBackend {
	t.Helper()
	s, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &daemonBackend{store: s}
}

// addMemFSFile ingests payload as a MemFS FILE node (the same
// path the HTTP add endpoint uses) and returns its root MID.
func addMemFSFile(t *testing.T, b *daemonBackend, name string, payload []byte) mid.MID {
	t.Helper()
	res, err := memfs.NewBuilder(b.store).AddFile(name, bytes.NewReader(payload), 0o644, time.Unix(1700000000, 0), "application/octet-stream")
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if res.MID.Codec() != mid.CodecMemFS {
		t.Fatalf("expected MemFS codec, got %#x", res.MID.Codec())
	}
	return res.MID
}

func readAllFromGet(t *testing.T, b *daemonBackend, m mid.MID) []byte {
	t.Helper()
	rc, err := b.Get(context.Background(), m.String(), 0, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return got
}

// TestGet_MemFSFile_SingleBlock covers a file that fits in one
// chunk (data inlined in the FILE node).
func TestGet_MemFSFile_SingleBlock(t *testing.T) {
	b := newInMemBackend(t)
	payload := bytes.Repeat([]byte("membuss "), 64) // 512 B
	m := addMemFSFile(t, b, "small.txt", payload)

	got := readAllFromGet(t, b, m)
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(got), len(payload))
	}
}

// TestGet_MemFSFile_MultiBlock covers a file spanning several
// raw blocks (FILE node with a list of block references).
func TestGet_MemFSFile_MultiBlock(t *testing.T) {
	b := newInMemBackend(t)
	// 3 blocks at the 256 KiB default chunk size.
	payload := bytes.Repeat([]byte{0xAB}, 3*memfs.DefaultBlockSize+123)
	m := addMemFSFile(t, b, "big.bin", payload)

	got := readAllFromGet(t, b, m)
	if len(got) != len(payload) {
		t.Fatalf("round-trip size mismatch: got %d, want %d", len(got), len(payload))
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round-trip content mismatch")
	}
}

// TestGetWithProgress_MemFSFile makes sure the progress-aware
// path used by the gRPC Get server also resolves MemFS files.
func TestGetWithProgress_MemFSFile(t *testing.T) {
	b := newInMemBackend(t)
	payload := bytes.Repeat([]byte("xyz"), 100000) // ~300 KB, 2 blocks
	m := addMemFSFile(t, b, "prog.bin", payload)

	rc, meta, err := b.GetWithProgress(context.Background(), m.String(), 0, 0, nil)
	if err != nil {
		t.Fatalf("GetWithProgress: %v", err)
	}
	if meta.Name != "prog.bin" {
		t.Fatalf("meta.Name = %q, want prog.bin", meta.Name)
	}
	if meta.Size != uint64(len(payload)) {
		t.Fatalf("meta.Size = %d, want %d", meta.Size, len(payload))
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(got), len(payload))
	}
}

// TestGet_MemFSFile_OffsetLimit exercises the offset/limit
// (byte-range) path over a MemFS file, which the CLI `get
// --offset --limit` flags and gateway Range requests rely on.
func TestGet_MemFSFile_OffsetLimit(t *testing.T) {
	b := newInMemBackend(t)
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	m := addMemFSFile(t, b, "range.txt", payload)

	rc, err := b.Get(context.Background(), m.String(), 10, 6)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if want := payload[10:16]; !bytes.Equal(got, want) {
		t.Fatalf("range mismatch: got %q, want %q", got, want)
	}
}

// TestGet_MemFSDir_Rejected confirms a DIR MID is rejected with
// a clear error rather than silently producing garbage bytes.
// The CLI routes directories through the recursive downloader,
// so a plain Get on a DIR is a caller error.
func TestGet_MemFSDir_Rejected(t *testing.T) {
	b := newInMemBackend(t)
	bld := memfs.NewBuilder(b.store)
	fileRes, err := bld.AddFile("f.txt", bytes.NewReader([]byte("hi")), 0o644, time.Unix(1700000000, 0), "")
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	dirRes, err := bld.AddDir("d", []memfs.DirEntry{
		{Name: "f.txt", Mid: fileRes.MID, Type: memfs.TypeFile, Size: fileRes.Size},
	}, 0o755, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("AddDir: %v", err)
	}
	if _, err := b.Get(context.Background(), dirRes.MID.String(), 0, 0); err == nil {
		t.Fatal("expected error resolving a DIR node via Get, got nil")
	}
}

// TestGet_DAGNode_Regression ensures the codec dispatch did not
// break the legacy plain-DAG resolve path (files added through
// the gRPC Add / dag.Builder path are DAGNode trees, not MemFS).
func TestGet_DAGNode_Regression(t *testing.T) {
	b := newInMemBackend(t)
	// Multi-chunk input so the root is a DAGNode, not a bare leaf.
	payload := bytes.Repeat([]byte{0xCD}, 3*chunk.DefaultBlockSize+7)
	c, err := chunk.NewFixed(chunk.DefaultBlockSize)(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("chunker: %v", err)
	}
	root, err := dag.NewBuilder(b.store).Build(c)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if root.Codec() == mid.CodecMemFS {
		t.Fatal("expected a non-MemFS DAG root")
	}
	got := readAllFromGet(t, b, root)
	if !bytes.Equal(got, payload) {
		t.Fatalf("DAG round-trip mismatch: got %d bytes, want %d bytes", len(got), len(payload))
	}
}
