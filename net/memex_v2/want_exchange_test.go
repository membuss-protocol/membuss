package memex_v2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

type memoryBlockstore struct {
	data map[string][]byte
}

func newMemoryBlockstore() *memoryBlockstore {
	return &memoryBlockstore{data: make(map[string][]byte)}
}

func (m *memoryBlockstore) Put(id mid.MID, data []byte) error {
	m.data[id.String()] = data
	return nil
}

func (m *memoryBlockstore) Get(id mid.MID) ([]byte, error) {
	d, ok := m.data[id.String()]
	if !ok {
		return nil, errors.New("block not found")
	}
	return d, nil
}

func (m *memoryBlockstore) Has(id mid.MID) (bool, error) {
	_, ok := m.data[id.String()]
	return ok, nil
}

func TestPeerWantlistManager(t *testing.T) {
	pwm := newPeerWantlistManager()
	testPeer := peer.ID("peer-1")
	m1 := mid.FromBytes([]byte("block-data-1"))
	m2 := mid.FromBytes([]byte("block-data-2"))

	entries := []*membusspb.WantEntry{
		{Mid: m1.String(), WantType: membusspb.WantType_WANT_HAVE, Priority: 1},
		{Mid: m2.String(), WantType: membusspb.WantType_WANT_BLOCK, Priority: 2},
	}

	pwm.UpdatePeerWantlist(testPeer, entries, nil)

	blockPeers := pwm.GetPeersWantingBlock(m2)
	if len(blockPeers) != 1 || blockPeers[0] != testPeer {
		t.Fatalf("expected peer-1 in wanting block list, got %v", blockPeers)
	}

	bList, hList := pwm.GetPeersWanting(m1)
	if len(hList) != 1 || hList[0] != testPeer {
		t.Fatalf("expected peer-1 in wanting have list, got %v", hList)
	}
	if len(bList) != 0 {
		t.Fatalf("expected empty wanting block list for m1, got %v", bList)
	}

	// Apply cancel for m2
	pwm.UpdatePeerWantlist(testPeer, nil, []string{m2.String()})
	if pwm.HasPeerWant(testPeer, m2) {
		t.Fatalf("expected m2 want to be cancelled")
	}

	// Remove peer
	pwm.RemovePeer(testPeer)
	if pwm.HasPeerWant(testPeer, m1) {
		t.Fatalf("expected peer-1 state to be removed")
	}
}

func TestHandleRemoteWants_WantHaveAndBlock(t *testing.T) {
	h1 := newTestHost(t)
	defer h1.Close()

	bs := newMemoryBlockstore()
	m1 := mid.FromBytes([]byte("test-content-1"))
	bs.Put(m1, []byte("test-content-1"))

	m2 := mid.FromBytes([]byte("test-content-2"))

	eng, err := New(Config{Host: h1, Blockstore: bs})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer eng.Stop()

	testPeer := peer.ID("peer-remote")
	wants := []*membusspb.WantEntry{
		{Mid: m1.String(), WantType: membusspb.WantType_WANT_HAVE},
		{Mid: m1.String(), WantType: membusspb.WantType_WANT_BLOCK},
		{Mid: m2.String(), WantType: membusspb.WantType_WANT_HAVE, SendDontHave: true},
	}

	resp := eng.HandleRemoteWants(testPeer, wants, nil)

	if len(resp.HaveMids) != 1 || resp.HaveMids[0] != m1.String() {
		t.Errorf("expected HAVE response for m1, got %v", resp.HaveMids)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Mid != m1.String() {
		t.Errorf("expected Block payload for m1, got %v", resp.Blocks)
	}
	if len(resp.DontHaves) != 1 || resp.DontHaves[0] != m2.String() {
		t.Errorf("expected DontHave for missing m2, got %v", resp.DontHaves)
	}
}

func TestOpportunisticPushBlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h1 := newTestHost(t)
	h2 := newTestHost(t)
	defer h1.Close()
	defer h2.Close()

	if err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	bs1 := newMemoryBlockstore()
	eng1, err := New(Config{Host: h1, Blockstore: bs1})
	if err != nil {
		t.Fatalf("failed to create engine 1: %v", err)
	}
	eng1.Start()
	defer eng1.Stop()

	bs2 := newMemoryBlockstore()
	eng2, err := New(Config{Host: h2, Blockstore: bs2})
	if err != nil {
		t.Fatalf("failed to create engine 2: %v", err)
	}
	eng2.Start()
	defer eng2.Stop()

	targetMID := mid.FromBytes([]byte("opportunistic-data"))

	// Peer 2 sends a WANT_BLOCK for targetMID to Peer 1
	wants := []*membusspb.WantEntry{
		{Mid: targetMID.String(), WantType: membusspb.WantType_WANT_BLOCK},
	}
	_ = eng1.HandleRemoteWants(h2.ID(), wants, nil)

	if !eng1.PeerWantlist().HasPeerWant(h2.ID(), targetMID) {
		t.Fatalf("expected eng1 to track h2's want")
	}

	// Trigger opportunistic push on Engine 1
	eng1.OpportunisticPushBlock(targetMID, []byte("opportunistic-data"))

	// Give a short time for stream delivery
	time.Sleep(50 * time.Millisecond)
}
