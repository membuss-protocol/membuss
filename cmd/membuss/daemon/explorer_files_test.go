// Regression test: content ingested via the gRPC/CLI add path
// (daemonBackend.Add) must appear in the explorer's file list
// (AllStoredMIDs) without the daemon restarting. Previously the
// file list was served from an in-memory allRoots cache that
// only the explorer's own HTTP handlers populated, so CLI/gRPC
// uploads stayed invisible until restart.
package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAllStoredMIDs_IncludesGRPCAddPath(t *testing.T) {
	b := newInMemBackend(t)

	// Write a temp file and ingest it exactly the way the
	// gRPC/CLI `add` command does (sealed root).
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.txt")
	payload := []byte("membuss-cli add <file> --addr host:port\n")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Build the explorer adapter FIRST, seeding its allRoots
	// cache from the (empty) store, exactly like the running
	// daemon at startup. The gRPC add below then happens on a
	// long-lived adapter without ever touching that cache.
	a := newExplorerAdapter(b, false, nil, nil)

	res, err := b.Add(context.Background(), path, "", 0, true /*sealRoot*/, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	files, err := a.AllStoredMIDs(context.Background())
	if err != nil {
		t.Fatalf("AllStoredMIDs: %v", err)
	}

	var found *StoredMIDViewLike
	for i := range files {
		if files[i].MID == res.MID {
			f := StoredMIDViewLike{
				MID:    files[i].MID,
				Name:   files[i].Name,
				Sealed: files[i].Sealed,
				Size:   files[i].Size,
			}
			found = &f
			break
		}
	}
	if found == nil {
		t.Fatalf("gRPC-added MID %s not present in AllStoredMIDs (%d entries)", res.MID, len(files))
	}
	if !found.Sealed {
		t.Errorf("MID %s should be reported Sealed", found.MID)
	}
	if found.Name != "commands.txt" {
		t.Errorf("Name = %q, want %q", found.Name, "commands.txt")
	}
	if found.Size != uint64(len(payload)) {
		t.Errorf("Size = %d, want %d", found.Size, len(payload))
	}
}

// TestAllStoredMIDs_IncludesUnsealedAddPath proves the file list
// also surfaces unsealed uploads (add --no-seal), which persist
// an ObjectInfo root but no seal record.
func TestAllStoredMIDs_IncludesUnsealedAddPath(t *testing.T) {
	b := newInMemBackend(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "draft.bin")
	if err := os.WriteFile(path, []byte("unsealed payload"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	a := newExplorerAdapter(b, false, nil, nil)

	res, err := b.Add(context.Background(), path, "", 0, false /*sealRoot*/, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	files, err := a.AllStoredMIDs(context.Background())
	if err != nil {
		t.Fatalf("AllStoredMIDs: %v", err)
	}

	for _, f := range files {
		if f.MID == res.MID {
			if f.Sealed {
				t.Errorf("MID %s should be reported UNSEALED", f.MID)
			}
			return
		}
	}
	t.Fatalf("unsealed MID %s not present in AllStoredMIDs (%d entries)", res.MID, len(files))
}

// StoredMIDViewLike mirrors the fields we assert on, avoiding an
// import cycle vs. copying the gateway/explorer type wholesale.
type StoredMIDViewLike struct {
	MID    string
	Name   string
	Sealed bool
	Size   uint64
}
