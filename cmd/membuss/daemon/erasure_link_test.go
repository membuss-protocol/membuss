package daemon

import (
	"bytes"
	"context"
	"testing"

	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/ingest"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// TestLinkLeavesToRoot ingests a real multi-chunk file with erasure
// enabled, then verifies the linkage pass records every manifest-bearing
// raw leaf back to the ingest root.
func TestLinkLeavesToRoot(t *testing.T) {
	bs := store.NewMemstore()

	payload := bytes.Repeat([]byte("membuss-erasure-linkage"), 256) // 5888 bytes
	res, err := ingest.IngestFile(context.Background(), bs, bytes.NewReader(payload), ingest.Options{
		Name:          "link-fixture.bin",
		ChunkSize:     1024,
		EnableErasure: true,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	root := res.MID

	if n := linkLeavesToRoot(bs, root); n == 0 {
		t.Fatal("linkLeavesToRoot linked zero leaves")
	}

	var leavesWithManifest int
	walkErr := store.Walk(bs, root, func(m mid.MID, leaf bool) error {
		if !leaf || m.Codec() != mid.CodecRaw {
			return nil
		}
		mf, err := erasure.GetManifest(bs, m)
		if err != nil || mf == nil {
			return nil
		}
		leavesWithManifest++

		got, gerr := erasure.GetManifestRoot(bs, m)
		if gerr != nil {
			t.Fatalf("read linkage row for %s: %v", m, gerr)
		}
		if got.IsZero() || got.String() != root.String() {
			t.Fatalf("leaf %s linked to %s, want root %s", m, got, root)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if leavesWithManifest == 0 {
		t.Fatal("fixture produced no erasure manifests on leaves")
	}
}

// TestLinkLeavesToRoot_NoManifestNoRow verifies leaves without an
// erasure manifest produce no linkage row, and that a nil store or
// zero root is a safe no-op.
func TestLinkLeavesToRoot_NoManifestNoRow(t *testing.T) {
	bs := store.NewMemstore()

	payload := bytes.Repeat([]byte("plain"), 100)
	res, err := ingest.IngestFile(context.Background(), bs, bytes.NewReader(payload), ingest.Options{
		Name:      "plain-fixture.bin",
		ChunkSize: 4096,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if n := linkLeavesToRoot(bs, res.MID); n != 0 {
		t.Fatalf("linked %d rows for a non-erasure ingest", n)
	}

	if n := linkLeavesToRoot(nil, res.MID); n != 0 {
		t.Fatal("nil store must be a no-op")
	}
	if n := linkLeavesToRoot(bs, mid.MID{}); n != 0 {
		t.Fatal("zero root must be a no-op")
	}

	// A MID with no linkage row reads back as zero MID, nil error.
	leaf := mid.FromBytes([]byte("unmanifested-leaf"))
	got, gerr := erasure.GetManifestRoot(bs, leaf)
	if gerr != nil || !got.IsZero() {
		t.Fatalf("missing row: got (%s, %v), want zero MID", got, gerr)
	}
}
