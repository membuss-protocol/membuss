package dht

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestMemDHT_Bootstrap_UnreachablePeers(t *testing.T) {
	h := newTestHost(t)
	t.Cleanup(func() { h.Close() })

	mdht, err := New(context.Background(), Config{Host: h})
	if err != nil {
		t.Fatalf("new dht: %v", err)
	}
	t.Cleanup(func() { _ = mdht.Close() })

	badPeer := peer.AddrInfo{ID: peer.ID("QmInvalidAddress123")}

	err = mdht.Bootstrap(context.Background(), []peer.AddrInfo{badPeer})
	if err == nil {
		t.Fatal("expected Bootstrap to return error for unreachable peer list, got nil")
	}
	if !strings.Contains(err.Error(), "all bootstrap peers unreachable") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMemDHT_Bootstrap_NoPeers(t *testing.T) {
	h := newTestHost(t)
	t.Cleanup(func() { h.Close() })

	mdht, err := New(context.Background(), Config{Host: h})
	if err != nil {
		t.Fatalf("new dht: %v", err)
	}
	t.Cleanup(func() { _ = mdht.Close() })

	err = mdht.Bootstrap(context.Background(), nil)
	if err != nil {
		t.Errorf("expected Bootstrap with no peers to succeed, got: %v", err)
	}
}

func TestBootstrapWithBackoff_DeduplicatesSuccessfulPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h1, h2 := newTestHost(t), newTestHost(t)
	t.Cleanup(func() { _ = h1.Close(); _ = h2.Close() })
	mdht, err := New(ctx, Config{Host: h1})
	if err != nil {
		t.Fatalf("new dht: %v", err)
	}
	t.Cleanup(func() { _ = mdht.Close() })

	info := peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}
	connected, err := mdht.BootstrapWithBackoff(ctx, []peer.AddrInfo{info, info}, BootstrapConfig{MaxAttempts: 1})
	if err != nil {
		t.Fatalf("BootstrapWithBackoff: %v", err)
	}
	if connected != 1 {
		t.Fatalf("connected = %d, want 1 deduplicated peer", connected)
	}
}

func TestDedupeAddrInfo_MergesTransportAddresses(t *testing.T) {
	id := peer.ID("peer")
	tcpAddr := ma.StringCast("/ip4/192.0.2.10/tcp/4001")
	quicAddr := ma.StringCast("/ip4/192.0.2.10/udp/4001/quic-v1")
	got := dedupeAddrInfo([]peer.AddrInfo{
		{ID: id, Addrs: []ma.Multiaddr{tcpAddr}},
		{ID: id, Addrs: []ma.Multiaddr{quicAddr, tcpAddr}},
	})
	if len(got) != 1 || len(got[0].Addrs) != 2 {
		t.Fatalf("dedupeAddrInfo = %v, want one peer with two addresses", got)
	}
}
