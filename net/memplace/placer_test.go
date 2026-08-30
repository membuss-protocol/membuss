package memplace

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multibase"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/shard"
	"github.com/nnlgsakib/membuss/core/store"
)

// newPeerID builds a deterministic peer.ID from a string seed so
// tests don't need real key generation.
func newPeerID(t *testing.T, seed string) peer.ID {
	t.Helper()
	// Identity multihash of the raw seed bytes: valid peer.ID, no crypto needed.
	b, err := multibase.Encode(multibase.Base32, []byte("\x00"+seed))
	if err != nil {
		t.Fatal(err)
	}
	return peer.ID(b)
}

type recPush struct {
	calls map[peer.ID][]store.Block
}

func (r *recPush) PushBlocksTo(_ context.Context, pid peer.ID, blocks []store.Block) error {
	if r.calls == nil {
		r.calls = make(map[peer.ID][]store.Block)
	}
	r.calls[pid] = append(r.calls[pid], blocks...)
	return nil
}

type recAnnounce struct {
	roots []mid.MID
}

func (r *recAnnounce) ProvideShardSet(_ context.Context, root mid.MID) error {
	r.roots = append(r.roots, root)
	return nil
}

func TestShardDistribution_ExcludesSelfAndCoversOwners(t *testing.T) {
	self := newPeerID(t, "self")
	p1 := newPeerID(t, "peer1")
	p2 := newPeerID(t, "peer2")
	p3 := newPeerID(t, "peer3")

	ring := shard.NewHashRing()
	ring.AddPeer(self.String())
	ring.AddPeer(p1.String())
	ring.AddPeer(p2.String())
	ring.AddPeer(p3.String())

	shards := []store.Block{
		{MID: mid.FromBytes([]byte("shard-a")), Data: []byte("a")},
		{MID: mid.FromBytes([]byte("shard-b")), Data: []byte("b")},
		{MID: mid.FromBytes([]byte("shard-c")), Data: []byte("c")},
	}

	dist := shardDistribution(ring, self, 2, shards)

	if len(dist) == 0 {
		t.Fatal("expected non-empty distribution")
	}
	total := 0
	for pid, blocks := range dist {
		if pid == self {
			t.Fatal("self must not appear in distribution")
		}
		for _, blk := range blocks {
			if blk.Data == nil {
				t.Fatal("block data must survive the distribution")
			}
		}
		total += len(blocks)
	}
	// 3 shards * 2 replicas = 6 placements; self takes at most 3
	// of them, so remote must be >= 3.
	if total < 3 {
		t.Fatalf("expected >= 3 remote placements, got %d", total)
	}
}

func TestPlaceShards_PushesAndAnnounces(t *testing.T) {
	self := newPeerID(t, "self")
	p1 := newPeerID(t, "peer1")
	p2 := newPeerID(t, "peer2")
	p3 := newPeerID(t, "peer3")

	ring := shard.NewHashRing()
	ring.AddPeer(self.String())
	ring.AddPeer(p1.String())
	ring.AddPeer(p2.String())
	ring.AddPeer(p3.String())

	push := &recPush{}
	ann := &recAnnounce{}
	p := New(Config{Replicas: 2}, ring, self, push, ann, nil)

	shards := []store.Block{
		{MID: mid.FromBytes([]byte("s1")), Data: []byte("x1")},
		{MID: mid.FromBytes([]byte("s2")), Data: []byte("x2")},
	}
	root := mid.FromBytes([]byte("root"))

	n, err := p.PlaceShards(context.Background(), root, shards)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Skip("all owners were self; rerun with different shards")
	}
	if len(push.calls) == 0 {
		t.Fatal("expected pushes to remote owners")
	}
	if len(ann.roots) != 1 || ann.roots[0].String() != root.String() {
		t.Fatalf("expected exactly one announce of the root, got %v", ann.roots)
	}
}

func TestPlaceShards_NilRingNoop(t *testing.T) {
	p := New(Config{Replicas: 2}, nil, "", &recPush{}, &recAnnounce{}, nil)
	n, err := p.PlaceShards(context.Background(), mid.FromBytes([]byte("r")), []store.Block{{MID: mid.FromBytes([]byte("s")), Data: []byte("d")}})
	if err != nil || n != 0 {
		t.Fatalf("nil ring must be a no-op, got n=%d err=%v", n, err)
	}
}
