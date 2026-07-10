package host

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
)

// TestHost_NATStatus_DefaultUnknown checks that a freshly
// constructed in-process host reports "unknown" reachability
// when no AutoNAT probe has been run. We compare case-
// insensitively because the underlying libp2p String()
// method capitalises its verdict ("Unknown").
func TestHost_NATStatus_DefaultUnknown(t *testing.T) {
	h, err := NewHost(Config{InProcess: true})
	if err != nil {
		t.Fatalf("in-process host: %v", err)
	}
	defer h.Close()
	got := strings.ToLower(h.NATStatus())
	if got != "unknown" {
		t.Fatalf("NATStatus = %q, want %q", got, "unknown")
	}
	if h.IsPublic() {
		t.Fatal("IsPublic = true on in-process host")
	}
	if h.IsPrivate() {
		t.Fatal("IsPrivate = true on in-process host")
	}
}

// TestHost_WaitForNAT_ImmediateNoWait asserts that a 0/negative
// timeout returns immediately with the current status.
func TestHost_WaitForNAT_ImmediateNoWait(t *testing.T) {
	h, err := NewHost(Config{InProcess: true})
	if err != nil {
		t.Fatalf("in-process host: %v", err)
	}
	defer h.Close()

	start := time.Now()
	got, err := h.WaitForNAT(context.Background(), 0)
	if err != nil {
		t.Fatalf("WaitForNAT: %v", err)
	}
	if strings.ToLower(got) != "unknown" {
		t.Fatalf("got %q, want unknown", got)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("WaitForNAT blocked %s on 0 timeout", elapsed)
	}
}

// TestHost_WaitForNAT_TimeoutFires checks that a short
// timeout on a host that never gets an AutoNAT verdict
// returns ctx.DeadlineExceeded (or context-deadline-exceeded
// wrapped) without hanging.
func TestHost_WaitForNAT_TimeoutFires(t *testing.T) {
	h, err := NewHost(Config{InProcess: true})
	if err != nil {
		t.Fatalf("in-process host: %v", err)
	}
	defer h.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	got, err := h.WaitForNAT(ctx, 200*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("WaitForNAT returned nil err, want timeout; got=%q", got)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("returned too early: %s", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took too long: %s", elapsed)
	}
}

// TestHost_PersistentIdentity_NATFieldsDontBreak verifies
// that the new Phase 11 fields in Config do not break the
// existing persistent-identity smoke test (the existing
// TestNewHost_PersistentIdentity already exercises this;
// this test is a more explicit regression guard).
func TestHost_PersistentIdentity_NATFieldsDontBreak(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHost(Config{
		DataDir:              dir,
		RelayService:         false,
		RelayMaxConns:        128,
		RelayMaxReservations: 128,
		RelayBandwidthMB:     16,
		ForceRelay:           false,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()
	if h.ID().String() == "" {
		t.Fatal("empty peer id")
	}
}

func TestHost_ForceRelayReportsPrivate(t *testing.T) {
	h, err := NewHost(Config{
		DataDir:     t.TempDir(),
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		ForceRelay:  true,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	status, err := h.WaitForNAT(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("WaitForNAT: %v", err)
	}
	if status != "private" || !h.IsPrivate() {
		t.Fatalf("status = %q, private = %v", status, h.IsPrivate())
	}
}

func TestHost_WaitForNATObservesTransition(t *testing.T) {
	h, err := NewHost(Config{InProcess: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	done := make(chan string, 1)
	go func() {
		status, _ := h.WaitForNAT(context.Background(), time.Second)
		done <- status
	}()
	h.setReachability(network.ReachabilityPublic)

	select {
	case status := <-done:
		if status != "public" {
			t.Fatalf("status = %q, want public", status)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForNAT did not observe reachability event")
	}
}

func TestHost_NATStatusTracksAllTransitions(t *testing.T) {
	h, err := NewHost(Config{InProcess: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	for _, step := range []struct {
		reachability network.Reachability
		status       string
	}{
		{network.ReachabilityUnknown, "unknown"},
		{network.ReachabilityPrivate, "private"},
		{network.ReachabilityPublic, "public"},
	} {
		h.setReachability(step.reachability)
		if got := h.NATStatus(); got != step.status {
			t.Fatalf("NATStatus = %q, want %q", got, step.status)
		}
	}
}

func TestBuildNATOptions_EnablesProductionTraversalStack(t *testing.T) {
	opts, err := buildNATOptions(Config{RelayService: true})
	if err != nil {
		t.Fatalf("buildNATOptions: %v", err)
	}
	var cfg libp2p.Config
	if err := cfg.Apply(opts...); err != nil {
		t.Fatalf("apply NAT options: %v", err)
	}
	if !cfg.EnableHolePunching {
		t.Error("DCUtR/hole punching is disabled")
	}
	if !cfg.EnableAutoNATv2 {
		t.Error("AutoNAT v2 is disabled")
	}
	if cfg.NATManager == nil {
		t.Error("UPnP/NAT-PMP port mapping is disabled")
	}
	if !cfg.EnableRelayService {
		t.Error("Circuit Relay v2 service is disabled")
	}
}

func TestBuildNATOptions_RejectsRelayBudgetOverflow(t *testing.T) {
	_, err := buildNATOptions(Config{RelayService: true, RelayBandwidthMB: math.MaxInt})
	if err == nil {
		t.Fatal("expected relay bandwidth overflow error")
	}
}
