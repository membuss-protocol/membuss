package memex_v2

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

func TestHandleRemoteWants_DontHave(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = ctx
	h := newTestHost(t)
	defer h.Close()

	eng, _ := newTestEngine(t, h)
	defer eng.Stop()

	nonExistentMID := mid.FromBytes([]byte("does-not-exist-in-store-12345"))

	// Request with SendDontHave: true
	resp := eng.HandleRemoteWants(peer.ID("peer-test"), []*membusspb.WantEntry{
		{
			Mid:          nonExistentMID.String(),
			SendDontHave: true,
		},
	}, nil)

	if len(resp.Blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(resp.Blocks))
	}
	if len(resp.HaveMids) != 0 {
		t.Fatalf("expected 0 HaveMids for missing block, got %d", len(resp.HaveMids))
	}
	if len(resp.DontHaves) != 1 {
		t.Fatalf("expected 1 DontHaves, got %d", len(resp.DontHaves))
	}
	if resp.DontHaves[0] != nonExistentMID.String() {
		t.Fatalf("expected DontHave %s, got %s", nonExistentMID.String(), resp.DontHaves[0])
	}
}
