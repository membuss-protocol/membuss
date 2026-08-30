package herald

import (
	"context"
	"sync"
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
)

type fakeShardSetAnnouncer struct {
	mu    sync.Mutex
	roots []mid.MID
}

func (a *fakeShardSetAnnouncer) ProvideShardSet(ctx context.Context, root mid.MID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.roots = append(a.roots, root)
	return nil
}

func TestShardSetAnnouncer_ShardsStrategy(t *testing.T) {
	store := &fakeStore{}
	r1 := mid.FromBytes([]byte("shards-strategy-root-1"))
	r2 := mid.FromBytes([]byte("shards-strategy-root-2"))
	store.Seal(r1)
	store.Seal(r2)

	dht := &fakeProvider{}
	ann := &fakeShardSetAnnouncer{}

	hd, err := New(Config{
		Store:             store,
		DHT:               dht,
		Strategy:          StrategyShards,
		PeerID:            "",
		ShardSetAnnouncer: ann,
	})
	if err != nil {
		t.Fatalf("new herald: %v", err)
	}
	hd.RunOnce(context.Background())

	got := ann.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected shard-set provides for 2 roots, got %d: %v", len(got), got)
	}
	for _, want := range []mid.MID{r1, r2} {
		found := false
		for _, g := range got {
			if g.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("root %s not announced as shard set", want)
		}
	}
	if dht.Count() != 2 {
		t.Fatalf("expected 2 provider announces alongside shard sets, got %d", dht.Count())
	}
}

// TestShardSetAnnouncer_NilIsNoOp verifies the default path is unchanged:
// with no announcer wired, a shards-strategy round still provides normally.
func TestShardSetAnnouncer_NilIsNoOp(t *testing.T) {
	store := &fakeStore{}
	store.Seal(mid.FromBytes([]byte("shards-nil-root")))

	dht := &fakeProvider{}
	hd, err := New(Config{
		Store:    store,
		DHT:      dht,
		Strategy: StrategyShards,
	})
	if err != nil {
		t.Fatalf("new herald: %v", err)
	}
	n := hd.RunOnce(context.Background())
	if n != 1 {
		t.Fatalf("expected 1 announce, got %d", n)
	}
}

func (a *fakeShardSetAnnouncer) snapshot() []mid.MID {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]mid.MID, len(a.roots))
	copy(out, a.roots)
	return out
}
