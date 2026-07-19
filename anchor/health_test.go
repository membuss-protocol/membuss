package anchor

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestHealth_ReachableAnchorSurvives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h1 := newTestHost(t)
	t.Cleanup(func() { _ = h1.Close() })
	h2 := newTestHost(t)
	t.Cleanup(func() { _ = h2.Close() })

	e := &AnchorEngine{
		cfg:         Config{Host: h1, HealthEvery: DefaultHealthEvery},
		logger:      nopLogger{},
		anchors:     make(map[peer.ID]peer.AddrInfo),
		attempts:    make(map[string]time.Time),
		sticky:      make(map[peer.ID]struct{}),
		healthFails: make(map[peer.ID]int),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}

	// h2 is a real, reachable anchor.
	ai := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	e.AddAnchor(ai)

	e.checkAnchorHealth(ctx)

	if _, ok := e.anchorByID(h2.ID()); !ok {
		t.Fatalf("reachable anchor was pruned")
	}
	if got := e.failCount(h2.ID()); got != 0 {
		t.Errorf("reachable anchor fail count: got %d want 0", got)
	}
}

func TestHealth_UnreachableAnchorPrunedAfterThreshold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	h1 := newTestHost(t)
	t.Cleanup(func() { _ = h1.Close() })

	e := &AnchorEngine{
		cfg:         Config{Host: h1, HealthEvery: DefaultHealthEvery},
		logger:      nopLogger{},
		anchors:     make(map[peer.ID]peer.AddrInfo),
		attempts:    make(map[string]time.Time),
		sticky:      make(map[peer.ID]struct{}),
		healthFails: make(map[peer.ID]int),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}

	// A dead anchor: a second host that we create then immediately
	// close so its addresses are unreachable.
	dead := newTestHost(t)
	deadInfo := peer.AddrInfo{ID: dead.ID(), Addrs: dead.Addrs()}
	_ = dead.Close()
	e.AddAnchor(deadInfo)

	// Below threshold: still present, fail count climbing.
	for i := 1; i < anchorFailThreshold; i++ {
		e.checkAnchorHealth(ctx)
		if _, ok := e.anchorByID(dead.ID()); !ok {
			t.Fatalf("anchor pruned early after %d checks (threshold %d)", i, anchorFailThreshold)
		}
		if got := e.failCount(dead.ID()); got != i {
			t.Errorf("after %d checks: fail count got %d want %d", i, got, i)
		}
	}

	// The threshold-th failure prunes it.
	e.checkAnchorHealth(ctx)
	if _, ok := e.anchorByID(dead.ID()); ok {
		t.Fatalf("unreachable anchor not pruned after %d checks", anchorFailThreshold)
	}
	// Peerstore addresses must be cleared on prune.
	if addrs := h1.Peerstore().Addrs(dead.ID()); len(addrs) != 0 {
		t.Errorf("peerstore still has %d addrs for pruned anchor", len(addrs))
	}
}

func TestHealth_StickyAnchorNeverPruned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	h1 := newTestHost(t)
	t.Cleanup(func() { _ = h1.Close() })

	e := &AnchorEngine{
		cfg:         Config{Host: h1, HealthEvery: DefaultHealthEvery},
		logger:      nopLogger{},
		anchors:     make(map[peer.ID]peer.AddrInfo),
		attempts:    make(map[string]time.Time),
		sticky:      make(map[peer.ID]struct{}),
		healthFails: make(map[peer.ID]int),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}

	dead := newTestHost(t)
	deadInfo := peer.AddrInfo{ID: dead.ID(), Addrs: dead.Addrs()}
	_ = dead.Close()
	e.AddAnchor(deadInfo)
	// Mark it sticky (as a bootstrap anchor would be).
	e.mu.Lock()
	e.sticky[dead.ID()] = struct{}{}
	e.mu.Unlock()

	// Run well past the threshold; a sticky anchor must survive.
	for i := 0; i < anchorFailThreshold+3; i++ {
		e.checkAnchorHealth(ctx)
	}
	if _, ok := e.anchorByID(dead.ID()); !ok {
		t.Fatalf("sticky anchor was pruned")
	}
}

func TestHealth_RecoveryResetsFailCount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	h1 := newTestHost(t)
	t.Cleanup(func() { _ = h1.Close() })
	h2 := newTestHost(t)
	t.Cleanup(func() { _ = h2.Close() })

	e := &AnchorEngine{
		cfg:         Config{Host: h1, HealthEvery: DefaultHealthEvery},
		logger:      nopLogger{},
		anchors:     make(map[peer.ID]peer.AddrInfo),
		attempts:    make(map[string]time.Time),
		sticky:      make(map[peer.ID]struct{}),
		healthFails: make(map[peer.ID]int),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}

	ai := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	e.AddAnchor(ai)

	// Seed a couple of prior failures (as if it had been flaky).
	e.mu.Lock()
	e.healthFails[h2.ID()] = anchorFailThreshold - 1
	e.mu.Unlock()

	// h2 is reachable, so the next check should reset the counter and
	// keep the anchor rather than pruning it.
	e.checkAnchorHealth(ctx)
	if _, ok := e.anchorByID(h2.ID()); !ok {
		t.Fatalf("recovered anchor was pruned")
	}
	if got := e.failCount(h2.ID()); got != 0 {
		t.Errorf("fail count after recovery: got %d want 0", got)
	}
}

// --- small test-only accessors ---

func (e *AnchorEngine) anchorByID(id peer.ID) (peer.AddrInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ai, ok := e.anchors[id]
	return ai, ok
}

func (e *AnchorEngine) failCount(id peer.ID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.healthFails[id]
}
