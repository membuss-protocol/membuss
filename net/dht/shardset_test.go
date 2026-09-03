package dht

import (
	"context"
	"testing"
	"time"

	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/core/mid"
)

func TestShardSetKeyDeterminism(t *testing.T) {
	r1 := mid.FromBytes([]byte("shard-root-one"))
	r2 := mid.FromBytes([]byte("shard-root-two"))

	k1a := shardSetKey(r1)
	k1b := shardSetKey(r1)
	if k1a.String() != k1b.String() {
		t.Fatal("not deterministic")
	}
	if k1a.Equal(r1) {
		t.Fatal("key must differ from root")
	}
	if k1a.Equal(shardSetKey(r2)) {
		t.Fatal("distinct roots must derive distinct keys")
	}
}

func TestMemDHT_ShardSetRoundtrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	h1 := newTestHost(t)
	h2 := newTestHost(t)
	t.Cleanup(func() { _ = h1.Close(); _ = h2.Close() })

	d1, err := New(ctx, Config{Host: h1, Mode: kaddht.ModeServer})
	if err != nil {
		t.Fatalf("dht1: %v", err)
	}
	d2, err := New(ctx, Config{Host: h2, Mode: kaddht.ModeServer})
	if err != nil {
		t.Fatalf("dht2: %v", err)
	}
	t.Cleanup(func() { _ = d1.Close(); _ = d2.Close() })

	ai := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	if err := d1.Bootstrap(ctx, []peer.AddrInfo{ai}); err != nil {
		t.Fatalf("d1 bootstrap: %v", err)
	}
	if err := d2.Bootstrap(ctx, []peer.AddrInfo{{ID: h1.ID(), Addrs: h1.Addrs()}}); err != nil {
		t.Fatalf("d2 bootstrap: %v", err)
	}
	if err := waitForRoutingTable(ctx, d1, 1, 30*time.Second); err != nil {
		t.Fatalf("d1 routing table: %v", err)
	}
	if err := waitForRoutingTable(ctx, d2, 1, 30*time.Second); err != nil {
		t.Fatalf("d2 routing table: %v", err)
	}

	root := mid.FromBytes([]byte("shardset-roundtrip-root"))
	if err := d1.ProvideShardSet(ctx, root); err != nil {
		t.Fatalf("ProvideShardSet: %v", err)
	}

	deadline := time.Now().Add(45 * time.Second)
	for {
		holders, ferr := d2.FindShardSets(ctx, root)
		if ferr == nil && len(holders) > 0 {
			for _, p := range holders {
				if p.ID == h1.ID() {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("d2 never discovered h1 as shard holder of %s", root)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
