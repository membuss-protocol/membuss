package anchor

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

func TestSealedCache_HitsCacheWithinTTL(t *testing.T) {
	cache := NewSealedCache(100 * time.Millisecond)
	m1 := mid.FromBytes([]byte("cached-mid-1"))

	var fetchCalls int64
	fetcher := func() ([]mid.MID, error) {
		atomic.AddInt64(&fetchCalls, 1)
		return []mid.MID{m1}, nil
	}

	// First call populates cache
	res1, err := cache.GetSealed(fetcher)
	if err != nil || len(res1) != 1 {
		t.Fatalf("first call failed: %v", err)
	}

	// Second call within TTL hits cache
	res2, err := cache.GetSealed(fetcher)
	if err != nil || len(res2) != 1 {
		t.Fatalf("second call failed: %v", err)
	}

	if atomic.LoadInt64(&fetchCalls) != 1 {
		t.Fatalf("expected 1 fetch call, got %d", fetchCalls)
	}

	// Wait for TTL expiration
	time.Sleep(150 * time.Millisecond)

	// Third call after TTL refetches
	_, err = cache.GetSealed(fetcher)
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}

	if atomic.LoadInt64(&fetchCalls) != 2 {
		t.Fatalf("expected 2 fetch calls after TTL expiry, got %d", fetchCalls)
	}
}

func TestSealedCache_Add(t *testing.T) {
	cache := NewSealedCache(1 * time.Minute)
	m1 := mid.FromBytes([]byte("mid-1"))
	m2 := mid.FromBytes([]byte("mid-2"))

	fetcher := func() ([]mid.MID, error) {
		return []mid.MID{m1}, nil
	}

	_, _ = cache.GetSealed(fetcher)
	cache.Add(m2)

	// Direct check without fetcher
	got, _ := cache.GetSealed(func() ([]mid.MID, error) {
		return nil, errors.New("should not be called")
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 MIDs after Add, got %d", len(got))
	}
}
