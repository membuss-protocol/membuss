package memex_v2

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// waitFor polls cond until true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("waitFor: condition not met before deadline")
}

// newPushPair wires a sending engine to a receiving engine over real libp2p
// hosts (newTestHost pattern). recvCfg lets tests inject receiver Config
// options such as AcceptUnsolicited.
func newPushPair(t *testing.T, recvCfg func(*Config)) (engSend, engRecv *Engine, bsRecv *store.Memstore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	hRecv := newTestHost(t)
	hSend := newTestHost(t)
	t.Cleanup(func() { _ = hRecv.Close() })
	t.Cleanup(func() { _ = hSend.Close() })

	// Sender connects toward the receiver so PushBlocksTo can open streams.
	if err := hSend.Connect(ctx, peer.AddrInfo{ID: hRecv.ID(), Addrs: hRecv.Addrs()}); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	bsRecv = store.NewMemstore()
	cfgRecv := Config{Host: hRecv, Blockstore: bsRecv}
	if recvCfg != nil {
		recvCfg(&cfgRecv)
	}
	engRecv, err := New(cfgRecv)
	if err != nil {
		t.Fatalf("failed to create receiving engine: %v", err)
	}
	engRecv.Start()
	t.Cleanup(engRecv.Stop)

	engSend, err = New(Config{Host: hSend, Blockstore: store.NewMemstore()})
	if err != nil {
		t.Fatalf("failed to create sending engine: %v", err)
	}
	engSend.Start()
	t.Cleanup(engSend.Stop)

	return engSend, engRecv, bsRecv
}

func TestPushBlocksTo_Roundtrip(t *testing.T) {
	ctx := context.Background()

	engSend, engRecv, bsRecv := newPushPair(t, nil)

	data := []byte("targeted-shard-payload")
	m := mid.FromBytes(data)

	if err := engSend.PushBlocksTo(ctx, engRecv.host.ID(), []store.Block{{MID: m, Data: data}}); err != nil {
		t.Fatalf("PushBlocksTo: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		has, _ := bsRecv.Has(m)
		return has
	})
}

func TestPushBlocksTo_GateRejectsUnsolicited(t *testing.T) {
	ctx := context.Background()

	var policyFrom peer.ID
	var policyMID mid.MID
	engSend, engRecv, bsRecv := newPushPair(t, func(c *Config) {
		c.AcceptUnsolicited = func(from peer.ID, m mid.MID) bool {
			policyFrom = from
			policyMID = m
			return false
		}
	})

	data := []byte("unsolicited-fill-data")
	m := mid.FromBytes(data)

	// The receiver resets its inbound stream once the gate fires, so the
	// send-side write may fail; only the receive-side outcome matters here.
	_ = engSend.PushBlocksTo(ctx, engRecv.host.ID(), []store.Block{{MID: m, Data: data}})

	waitFor(t, 3*time.Second, func() bool { return engRecv.RejectedUnsolicited() == 1 })

	if has, _ := bsRecv.Has(m); has {
		t.Fatal("rejected block must not be persisted")
	}
	if !policyMID.Equal(m) {
		t.Errorf("policy saw MID %s, want %s", policyMID, m)
	}
	if policyFrom != engSend.host.ID() {
		t.Errorf("policy saw from=%s, want sender %s", policyFrom, engSend.host.ID())
	}
	if got := engRecv.host.Network().Connectedness(engSend.host.ID()); got != network.Connected {
		t.Errorf("hosts should remain connected after rejection, got %v", got)
	}
	if engRecv.RejectedUnsolicited() != 1 {
		t.Errorf("RejectedUnsolicited()=%d, want 1", engRecv.RejectedUnsolicited())
	}
}

func TestPushBlocksTo_GateAcceptsUnsolicited(t *testing.T) {
	ctx := context.Background()

	calls := 0
	engSend, engRecv, bsRecv := newPushPair(t, func(c *Config) {
		c.AcceptUnsolicited = func(from peer.ID, m mid.MID) bool {
			calls++
			return true
		}
	})

	data := []byte("accepted-shard-payload")
	m := mid.FromBytes(data)

	if err := engSend.PushBlocksTo(ctx, engRecv.host.ID(), []store.Block{{MID: m, Data: data}}); err != nil {
		t.Fatalf("PushBlocksTo: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		has, _ := bsRecv.Has(m)
		return has
	})
	if calls == 0 {
		t.Error("policy was never consulted")
	}
}
