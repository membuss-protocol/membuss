package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nnlgsakib/membuss/core/store"
)

func TestCrossInterfaceIngestParity(t *testing.T) {
	tempDir := t.TempDir()
	dbStore, err := store.NewMemStore(store.Options{Path: filepath.Join(tempDir, "store")})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	defer dbStore.Close()

	d := &daemonBackend{store: dbStore}
	apiAdap := &apiAdapter{b: d}
	expAdap := &explorerAdapter{b: d, allRoots: make(map[string]struct{})}

	payload := []byte("cross interface canonical ingestion test payload 1234567890")
	fileName := "sample.txt"

	// 1. gRPC / CLI ingestion
	tmpFilePath := filepath.Join(tempDir, fileName)
	if err := writeFileBytes(tmpFilePath, payload); err != nil {
		t.Fatalf("writeFileBytes: %v", err)
	}

	grpcRes, err := d.AddWithProgress(context.Background(), tmpFilePath, "", 0, true, fileName, "", nil)
	if err != nil {
		t.Fatalf("AddWithProgress (gRPC): %v", err)
	}

	// 2. Node API ingestion
	apiRes, err := apiAdap.AddFile(context.Background(), fileName, bytes.NewReader(payload), false)
	if err != nil {
		t.Fatalf("AddFile (Node API): %v", err)
	}

	// 3. Web Explorer ingestion
	expRes, err := expAdap.Add(context.Background(), fileName, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Add (Web Explorer): %v", err)
	}

	// Cross-Interface Parity Assertions
	if grpcRes.MID != apiRes.MID {
		t.Errorf("gRPC MID (%s) != API MID (%s)", grpcRes.MID, apiRes.MID)
	}

	if apiRes.MID != expRes.MID {
		t.Errorf("API MID (%s) != Explorer MID (%s)", apiRes.MID, expRes.MID)
	}

	if grpcRes.Size != apiRes.Size || apiRes.Size != expRes.Size {
		t.Errorf("Size mismatch across interfaces: gRPC=%d API=%d Exp=%d", grpcRes.Size, apiRes.Size, expRes.Size)
	}

	if grpcRes.Blocks != apiRes.Blocks || apiRes.Blocks != expRes.Blocks {
		t.Errorf("Block count mismatch across interfaces: gRPC=%d API=%d Exp=%d", grpcRes.Blocks, apiRes.Blocks, expRes.Blocks)
	}
}

func writeFileBytes(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
