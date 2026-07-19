package store

import (
	"context"
	"crypto/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

// TestMemStoreGC_ConcurrentRunsSerialized launches many GC calls at
// once. Before serialization, overlapping runs would walk and delete
// against a shared store in parallel, racing on deletion bookkeeping
// and duplicating work. gcMu must make them run one-at-a-time, so the
// final state is exactly "sealed blocks survive, unsealed blocks gone"
// regardless of how many callers raced. Run under -race to also catch
// data races in the walk/delete paths.
func TestMemStoreGC_ConcurrentRunsSerialized(t *testing.T) {
	s := newTestStore(t)

	const total = 400
	const sealEvery = 4

	allMIDs := make([]mid.MID, total)
	sealedSet := make(map[string]struct{})
	for i := 0; i < total; i++ {
		data := make([]byte, 256)
		if _, err := rand.Read(data); err != nil {
			t.Fatalf("rand: %v", err)
		}
		m := mid.FromBytes(data)
		if err := s.Put(m, data); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		allMIDs[i] = m
		if i%sealEvery == 0 {
			if err := s.Seal(m, false); err != nil {
				t.Fatalf("Seal %d: %v", i, err)
			}
			sealedSet[m.String()] = struct{}{}
		}
	}

	const runners = 8
	var wg sync.WaitGroup
	var firstErr atomic.Value
	for g := 0; g < runners; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.GC(context.Background()); err != nil {
				firstErr.CompareAndSwap(nil, err)
			}
		}()
	}
	wg.Wait()

	if v := firstErr.Load(); v != nil {
		t.Fatalf("concurrent GC returned error: %v", v.(error))
	}

	// Final state must be exactly the sealed set, proving the racing
	// runs did not corrupt each other's deletions.
	for i, m := range allMIDs {
		ok, err := s.Has(m)
		if err != nil {
			t.Fatalf("Has %d: %v", i, err)
		}
		_, isSealed := sealedSet[m.String()]
		if isSealed && !ok {
			t.Fatalf("sealed block %d (%s) was deleted by concurrent GC", i, m)
		}
		if !isSealed && ok {
			t.Fatalf("unsealed block %d (%s) survived concurrent GC", i, m)
		}
	}
}

// TestMemStoreGC_BlocksWhileLockHeld directly asserts GC waits on
// gcMu: while the lock is held by someone else, a GC call must not
// complete, and it must finish promptly once the lock is released.
// This test is in-package so it can hold the real gcMu the GC path
// takes, proving the serialization rather than inferring it.
func TestMemStoreGC_BlocksWhileLockHeld(t *testing.T) {
	s := newTestStore(t)

	// Seed a little data so GC has real work to do once it proceeds.
	for i := 0; i < 50; i++ {
		data := make([]byte, 256)
		if _, err := rand.Read(data); err != nil {
			t.Fatalf("rand: %v", err)
		}
		m := mid.FromBytes(data)
		if err := s.Put(m, data); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		if i%3 == 0 {
			if err := s.Seal(m, false); err != nil {
				t.Fatalf("Seal %d: %v", i, err)
			}
		}
	}

	// Hold the GC lock, then start a GC. It must block on gcMu.
	s.gcMu.Lock()

	done := make(chan error, 1)
	go func() {
		_, err := s.GC(context.Background())
		done <- err
	}()

	// While we hold the lock, GC must not complete.
	select {
	case <-done:
		s.gcMu.Unlock()
		t.Fatal("GC completed while gcMu was held; runs are not serialized")
	case <-time.After(200 * time.Millisecond):
		// Expected: still blocked.
	}

	// Release the lock; GC should now proceed and finish.
	s.gcMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("GC after unlock: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("GC did not complete after gcMu was released")
	}
}
