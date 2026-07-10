package host

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestHost_OutboundDialPrefersQUIC(t *testing.T) {
	newHost := func() *Host {
		h, err := NewHost(Config{
			DataDir: t.TempDir(),
			ListenAddrs: []string{
				"/ip4/127.0.0.1/tcp/0",
				"/ip4/127.0.0.1/udp/0/quic-v1",
			},
		})
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		t.Cleanup(func() { _ = h.Close() })
		return h
	}

	dialer, target := newHost(), newHost()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dialer.Connect(ctx, peer.AddrInfo{ID: target.ID(), Addrs: target.Addrs()}); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	conns := dialer.Network().ConnsToPeer(target.ID())
	if len(conns) == 0 {
		t.Fatal("no connection to target")
	}
	for _, conn := range conns {
		if strings.Contains(conn.RemoteMultiaddr().String(), "/quic-v1") {
			return
		}
	}
	t.Fatalf("smart dialer selected no QUIC connection: %v", conns)
}

func TestHost_AnnounceAddrsSupplementDetectedAddrs(t *testing.T) {
	announce := "/ip4/203.0.113.10/tcp/4001"
	h, err := NewHost(Config{
		DataDir:       t.TempDir(),
		ListenAddrs:   []string{"/ip4/127.0.0.1/tcp/0"},
		AnnounceAddrs: []string{announce, announce},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	var detected, announced int
	for _, addr := range h.Addrs() {
		switch addr.String() {
		case announce:
			announced++
		default:
			if strings.Contains(addr.String(), "/ip4/127.0.0.1/tcp/") {
				detected++
			}
		}
	}
	if detected == 0 {
		t.Fatalf("detected listener address was replaced: %v", h.Addrs())
	}
	if announced != 1 {
		t.Fatalf("announced address count = %d, want 1: %v", announced, h.Addrs())
	}
}
