package anchor

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

func TestAnchor_MergeAnchors(t *testing.T) {
	p1, _ := peer.Decode("12D3KooWPjceQrSwdWXPyLLeABRXmuqt69Rg3sBYbU1Nft9HyQ6X")
	p2, _ := peer.Decode("12D3KooWMUPp3d6dbMVFc8JfwVkno3kHzjNtHZXc6pvd6hi8C2a8")
	direct := []peer.AddrInfo{{ID: p1}}
	anchors := []peer.AddrInfo{{ID: p2}, {ID: p1}}
	merged := mergeAnchors(direct, anchors)
	if len(merged) != 2 {
		t.Fatalf("merged length: got %d, want 2", len(merged))
	}
	if merged[0].ID != p1 {
		t.Fatal("direct provider should remain first")
	}
	if merged[1].ID != p2 {
		t.Fatal("anchor should be appended")
	}
}

func TestAnchor_EncodeDecodeAddrInfo(t *testing.T) {
	pid, _ := peer.Decode("12D3KooWPjceQrSwdWXPyLLeABRXmuqt69Rg3sBYbU1Nft9HyQ6X")
	addr, _ := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")
	ai := peer.AddrInfo{ID: pid, Addrs: []multiaddr.Multiaddr{addr}}
	raw, err := encodeAddrInfo(ai)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeAnchorValue(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != ai.ID {
		t.Fatalf("ID: got %s, want %s", got.ID, ai.ID)
	}
	if len(got.Addrs) != 1 || got.Addrs[0].String() != addr.String() {
		t.Fatalf("Addrs: got %v, want %v", got.Addrs, ai.Addrs)
	}
}

func TestAnchor_RegistryIsConsistent(t *testing.T) {
	h := newTestHost(t)
	t.Cleanup(func() { _ = h.Close() })

	bs := store.NewMemstore()
	eng := &AnchorEngine{
		cfg: Config{
			Host:  h,
			Store: bs,
		},
		anchors:  make(map[peer.ID]peer.AddrInfo),
		attempts: make(map[string]time.Time),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	st := eng.Status()
	if st.PeerID != h.ID().String() {
		t.Fatalf("PeerID: got %s, want %s", st.PeerID, h.ID())
	}
}

func TestAnchor_PurgeStaleAttempts(t *testing.T) {
	eng := &AnchorEngine{
		attempts: make(map[string]time.Time),
	}
	now := time.Now()
	freshMID := mid.FromBytes([]byte("fresh-attempt"))
	staleMID := mid.FromBytes([]byte("stale-attempt"))

	eng.attempts[freshMID.String()] = now.Add(-5 * time.Minute)
	eng.attempts[staleMID.String()] = now.Add(-25 * time.Minute)

	eng.purgeStaleAttempts(now)

	eng.mu.Lock()
	defer eng.mu.Unlock()
	if _, exists := eng.attempts[freshMID.String()]; !exists {
		t.Errorf("expected fresh attempt to remain in attempts map")
	}
	if _, exists := eng.attempts[staleMID.String()]; exists {
		t.Errorf("expected stale attempt (>20m) to be purged from attempts map")
	}
}

func TestAnchor_DirtyRegistryPersistence(t *testing.T) {
	bs := store.NewMemstore()
	eng := &AnchorEngine{
		cfg: Config{
			Store: bs,
		},
		anchors: make(map[peer.ID]peer.AddrInfo),
	}

	eng.persistRegistry()
	val, err := bs.GetMeta(AnchorRegistryKey)
	if err == nil && len(val) > 0 {
		t.Fatalf("expected no DB write when registry is clean")
	}

	pid, _ := peer.Decode("12D3KooWPjceQrSwdWXPyLLeABRXmuqt69Rg3sBYbU1Nft9HyQ6X")
	eng.AddAnchor(peer.AddrInfo{ID: pid})

	if !eng.dirty {
		t.Fatalf("expected dirty == true after AddAnchor")
	}

	eng.persistRegistry()

	if eng.dirty {
		t.Fatalf("expected dirty == false after persistRegistry")
	}

	val, err = bs.GetMeta(AnchorRegistryKey)
	if err != nil || len(val) == 0 {
		t.Fatalf("expected DB write after AddAnchor")
	}
}

func TestAnchor_RoundRobinReacquisition(t *testing.T) {
	eng := &AnchorEngine{
		cfg: Config{
			ReacquireBatchSize: 3,
		},
		sampleOffset: 0,
	}

	sealed := make([]mid.MID, 10)
	for i := 0; i < 10; i++ {
		sealed[i] = mid.FromBytes([]byte{byte(i + 1)})
	}

	eng.mu.Lock()
	offset1 := eng.sampleOffset % len(sealed)
	eng.sampleOffset = (offset1 + 3) % len(sealed)
	eng.mu.Unlock()

	if offset1 != 0 || eng.sampleOffset != 3 {
		t.Fatalf("tick 1 offset: got %d next %d, want 0 next 3", offset1, eng.sampleOffset)
	}

	eng.mu.Lock()
	offset2 := eng.sampleOffset % len(sealed)
	eng.sampleOffset = (offset2 + 3) % len(sealed)
	eng.mu.Unlock()

	if offset2 != 3 || eng.sampleOffset != 6 {
		t.Fatalf("tick 2 offset: got %d next %d, want 3 next 6", offset2, eng.sampleOffset)
	}
}

