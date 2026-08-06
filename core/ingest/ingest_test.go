package ingest_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nnlgsakib/membuss/core/ingest"
	"github.com/nnlgsakib/membuss/core/store"
)

func TestIngestFile_CanonicalDeterminism(t *testing.T) {
	s1, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore 1: %v", err)
	}
	defer s1.Close()

	s2, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore 2: %v", err)
	}
	defer s2.Close()

	payload := []byte("hello canonical membuss ingestion world 12345")

	res1, err := ingest.IngestFile(context.Background(), s1, bytes.NewReader(payload), ingest.Options{
		Name: "test.txt",
	})
	if err != nil {
		t.Fatalf("IngestFile 1: %v", err)
	}

	res2, err := ingest.IngestFile(context.Background(), s2, bytes.NewReader(payload), ingest.Options{
		Name: "test.txt",
	})
	if err != nil {
		t.Fatalf("IngestFile 2: %v", err)
	}

	if res1.MID.String() != res2.MID.String() {
		t.Errorf("MID mismatch: %s vs %s", res1.MID, res2.MID)
	}

	if res1.Size != uint64(len(payload)) {
		t.Errorf("Size mismatch: want %d, got %d", len(payload), res1.Size)
	}

	if res1.Blocks != 1 {
		t.Errorf("Blocks mismatch: want 1, got %d", res1.Blocks)
	}
}

func TestIngestFile_RawDAGVsMemFS(t *testing.T) {
	s, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	defer s.Close()

	payload := []byte("payload for raw vs memfs comparison")

	memfsRes, err := ingest.IngestFile(context.Background(), s, bytes.NewReader(payload), ingest.Options{
		Name:   "raw.txt",
		RawDAG: false,
	})
	if err != nil {
		t.Fatalf("MemFS ingest failed: %v", err)
	}

	rawRes, err := ingest.IngestFile(context.Background(), s, bytes.NewReader(payload), ingest.Options{
		Name:   "raw.txt",
		RawDAG: true,
	})
	if err != nil {
		t.Fatalf("RawDAG ingest failed: %v", err)
	}

	if memfsRes.MID.String() == rawRes.MID.String() {
		t.Errorf("MemFS and RawDAG MIDs should differ: got both %s", memfsRes.MID)
	}
}

func TestIngestFile_ProgressTracking(t *testing.T) {
	s, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatalf("NewMemStore: %v", err)
	}
	defer s.Close()

	payload := []byte("progress tracking test bytes")

	var reported uint64
	_, err = ingest.IngestFile(context.Background(), s, bytes.NewReader(payload), ingest.Options{
		Name: "progress.txt",
		ProgressFn: func(processed, total uint64) {
			reported = processed
		},
	})
	if err != nil {
		t.Fatalf("IngestFile progress: %v", err)
	}

	if reported != uint64(len(payload)) {
		t.Errorf("Progress mismatch: want %d, got %d", len(payload), reported)
	}
}
