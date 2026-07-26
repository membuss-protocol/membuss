package anchor

import (
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
)

func TestDeltaSync_BloomFilter(t *testing.T) {
	m1 := mid.FromBytes([]byte("mid-item-1"))
	m2 := mid.FromBytes([]byte("mid-item-2"))
	m3 := mid.FromBytes([]byte("mid-item-3"))
	m4 := mid.FromBytes([]byte("mid-item-4"))

	known := []mid.MID{m1, m2}
	bloomBytes, hashes := BuildInventoryBloom(known)
	if len(bloomBytes) == 0 {
		t.Fatalf("expected non-empty bloom filter bytes")
	}

	allSealed := []mid.MID{m1, m2, m3, m4}
	delta := FilterDelta(allSealed, bloomBytes, hashes)

	// FilterDelta must contain m3 and m4, and must NOT contain m1 or m2
	foundM3 := false
	foundM4 := false
	for _, d := range delta {
		if d.String() == m1.String() || d.String() == m2.String() {
			t.Errorf("delta should not include already-known MID %s", d)
		}
		if d.String() == m3.String() {
			foundM3 = true
		}
		if d.String() == m4.String() {
			foundM4 = true
		}
	}

	if !foundM3 || !foundM4 {
		t.Fatalf("delta missing new items m3 or m4: got %v", delta)
	}
}

func TestDeltaSync_NilBloomReturnsAll(t *testing.T) {
	m1 := mid.FromBytes([]byte("mid-item-1"))
	allSealed := []mid.MID{m1}

	delta := FilterDelta(allSealed, nil, 0)
	if len(delta) != 1 {
		t.Fatalf("expected all items returned when bloomBytes is nil, got %d", len(delta))
	}
}
