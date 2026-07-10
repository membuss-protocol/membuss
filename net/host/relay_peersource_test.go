package host

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestRelayPeerSource_MergesAndDeduplicates(t *testing.T) {
	tcpAddr := ma.StringCast("/ip4/192.0.2.10/tcp/4001")
	quicAddr := ma.StringCast("/ip4/192.0.2.10/udp/4001/quic-v1")
	static := peer.AddrInfo{ID: peer.ID("static"), Addrs: []ma.Multiaddr{tcpAddr}}
	staticQUIC := peer.AddrInfo{ID: static.ID, Addrs: []ma.Multiaddr{quicAddr}}
	dynamic := peer.AddrInfo{ID: peer.ID("dynamic")}
	source := NewRelayPeerSource([]peer.AddrInfo{static, staticQUIC, static, {}})
	source.SetFinder(func(context.Context, int) ([]peer.AddrInfo, error) {
		return []peer.AddrInfo{static, dynamic, dynamic}, nil
	})

	got := collectRelayPeers(t, source.PeerSource()(context.Background(), 3))
	if len(got) != 2 || got[0].ID != static.ID || got[1].ID != dynamic.ID {
		t.Fatalf("relay candidates = %v, want static then dynamic", got)
	}
	if len(got[0].Addrs) != 2 {
		t.Fatalf("static relay addresses = %v, want merged TCP and QUIC", got[0].Addrs)
	}
}

func TestRelayPeerSource_WaitsForFinder(t *testing.T) {
	source := NewRelayPeerSource(nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := source.PeerSource()(ctx, 1)

	select {
	case <-out:
		t.Fatal("peer source closed before the DHT finder was installed")
	case <-time.After(20 * time.Millisecond):
	}

	want := peer.AddrInfo{ID: peer.ID("relay")}
	source.SetFinder(func(context.Context, int) ([]peer.AddrInfo, error) {
		return []peer.AddrInfo{want}, nil
	})
	got := collectRelayPeers(t, out)
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("relay candidates = %v, want %v", got, want)
	}
}

func TestRelayPeerSource_CancellationClosesOutput(t *testing.T) {
	source := NewRelayPeerSource(nil)
	ctx, cancel := context.WithCancel(context.Background())
	out := source.PeerSource()(ctx, 1)
	cancel()

	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("unexpected relay after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("peer source did not close after cancellation")
	}
}

func collectRelayPeers(t *testing.T, ch <-chan peer.AddrInfo) []peer.AddrInfo {
	t.Helper()
	var peers []peer.AddrInfo
	for info := range ch {
		peers = append(peers, info)
	}
	return peers
}
