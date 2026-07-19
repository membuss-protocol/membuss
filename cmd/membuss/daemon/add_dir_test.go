// Tests that daemonBackend.AddDirWithProgress walks a real
// directory tree, ingests it as a single MemFS DIR root, reports
// aggregate byte progress, seals, and produces a resolvable tree
// whose files download byte-for-byte. This is the local half of
// the AddDirStream gRPC used by `membuss-cli add <dir>`.
package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
)

// treeEntry is one child of a MemFS directory, flattened for the
// tree-collection helper.
type treeEntry struct {
	name  string
	mid   mid.MID
	isDir bool
}

// memfsList resolves dirMID and returns its immediate children.
func memfsList(t *testing.T, b *daemonBackend, dirMID mid.MID) ([]treeEntry, error) {
	t.Helper()
	node, err := memfs.NewResolver(b.store).Resolve(context.Background(), dirMID)
	if err != nil {
		return nil, err
	}
	entries := node.EntriesValue()
	out := make([]treeEntry, len(entries))
	for i, e := range entries {
		out[i] = treeEntry{name: e.Name, mid: e.Mid, isDir: e.Type == memfs.TypeDir}
	}
	return out, nil
}

func TestAddDirWithProgress_IngestsTree(t *testing.T) {
	b := newInMemBackend(t)

	// Build a small tree: two top-level files and a nested dir.
	root := t.TempDir()
	files := map[string][]byte{
		"a.txt":         []byte("alpha"),
		"b.bin":         []byte("betabetabeta"),
		"sub/c.txt":     []byte("gamma gamma"),
		"sub/deep/d.md": []byte("# delta\n"),
	}
	var wantTotal uint64
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		wantTotal += uint64(len(data))
	}

	// Capture progress. The counter must be monotonic and land on
	// total == processed at the end.
	var maxProcessed, lastTotal uint64
	progressFn := func(processed, total uint64) {
		if processed > maxProcessed {
			maxProcessed = processed
		}
		lastTotal = total
	}

	res, err := b.AddDirWithProgress(context.Background(), root, "", 0, true, "mytree", progressFn)
	if err != nil {
		t.Fatalf("AddDirWithProgress: %v", err)
	}

	rootMID, err := mid.Parse(res.MID)
	if err != nil {
		t.Fatalf("parse root mid: %v", err)
	}
	if rootMID.Codec() != mid.CodecMemFS {
		t.Fatalf("root is not a MemFS node: codec %#x", rootMID.Codec())
	}
	if !res.Sealed {
		t.Errorf("expected sealed root")
	}
	if res.Name != "mytree" {
		t.Errorf("name = %q, want mytree", res.Name)
	}
	if res.MimeType != "inode/directory" {
		t.Errorf("mime = %q, want inode/directory", res.MimeType)
	}
	if res.Size != wantTotal {
		t.Errorf("size = %d, want %d (sum of file sizes)", res.Size, wantTotal)
	}

	// Progress denominator is the tree total; final processed count
	// reaches it.
	if lastTotal != wantTotal {
		t.Errorf("progress total = %d, want %d", lastTotal, wantTotal)
	}
	if maxProcessed != wantTotal {
		t.Errorf("max processed = %d, want %d", maxProcessed, wantTotal)
	}

	// The sealed root must be resolvable and its files must come
	// back byte-for-byte. Walk the MemFS tree via ls.
	got := collectTreeFiles(t, b, rootMID, "")
	wantPaths := make([]string, 0, len(files))
	for rel := range files {
		wantPaths = append(wantPaths, rel)
	}
	sort.Strings(wantPaths)
	gotPaths := make([]string, 0, len(got))
	for rel := range got {
		gotPaths = append(gotPaths, rel)
	}
	sort.Strings(gotPaths)
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("tree files = %v, want %v", gotPaths, wantPaths)
	}
	for rel, want := range files {
		if string(got[rel]) != string(want) {
			t.Errorf("file %s = %q, want %q", rel, got[rel], want)
		}
	}
}

// collectTreeFiles recursively resolves a MemFS DIR into a map of
// slash-path -> file bytes, using the same Ls / Get surface the
// explorer and CLI use.
func collectTreeFiles(t *testing.T, b *daemonBackend, dirMID mid.MID, prefix string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	entries, err := memfsList(t, b, dirMID)
	if err != nil {
		t.Fatalf("ls %s: %v", dirMID, err)
	}
	for _, e := range entries {
		rel := e.name
		if prefix != "" {
			rel = prefix + "/" + e.name
		}
		if e.isDir {
			for k, v := range collectTreeFiles(t, b, e.mid, rel) {
				out[k] = v
			}
			continue
		}
		out[rel] = readAllFromGet(t, b, e.mid)
	}
	return out
}

func TestAddDirWithProgress_EmptyDirRejected(t *testing.T) {
	b := newInMemBackend(t)
	root := t.TempDir()
	if _, err := b.AddDirWithProgress(context.Background(), root, "", 0, true, "", nil); err == nil {
		t.Fatal("expected an error for a directory with no files")
	}
}

func TestAddDirWithProgress_NotADir(t *testing.T) {
	b := newInMemBackend(t)
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := b.AddDirWithProgress(context.Background(), f, "", 0, true, "", nil); err == nil {
		t.Fatal("expected an error when path is a regular file")
	}
}
