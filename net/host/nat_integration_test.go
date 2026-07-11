package host

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestHost_AutoRelayReservationCircuitFallbackAndDCUtR(t *testing.T) {
	newNetworkHost := func(cfg Config) *Host {
		cfg.DataDir = t.TempDir()
		cfg.ListenAddrs = []string{
			"/ip4/127.0.0.1/tcp/0",
			"/ip4/127.0.0.1/udp/0/quic-v1",
		}
		h, err := NewHost(cfg)
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		t.Cleanup(func() { _ = h.Close() })
		return h
	}

	// Circuit Relay v2 deliberately starts its hop service only after public
	// reachability is known. Force that verdict in this local-only topology;
	// production relay nodes obtain it from AutoNAT observers.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve relay port: %v", err)
	}
	relayPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	listenAddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", relayPort)
	publicRelayAddr := ma.StringCast(fmt.Sprintf("/dns4/localhost/tcp/%d", relayPort))
	relayHost, err := libp2p.New(
		libp2p.ListenAddrStrings(listenAddr),
		libp2p.AddrsFactory(func([]ma.Multiaddr) []ma.Multiaddr {
			return []ma.Multiaddr{publicRelayAddr}
		}),
		libp2p.EnableRelayService(),
		libp2p.ForceReachabilityPublic(),
	)
	if err != nil {
		t.Fatalf("relay host: %v", err)
	}
	t.Cleanup(func() { _ = relayHost.Close() })
	relayInfo := peer.AddrInfo{ID: relayHost.ID(), Addrs: relayHost.Addrs()}
	newPrivateHost := func() *Host {
		source := NewRelayPeerSource([]peer.AddrInfo{relayInfo})
		return newNetworkHost(Config{
			ForceRelay:      true,
			RelayPeerSource: source.PeerSource(),
		})
	}
	first, second := newPrivateHost(), newPrivateHost()

	firstRelayAddrs := waitForRelayReservation(t, first, relayInfo, 20*time.Second)
	secondRelayAddrs := waitForRelayReservation(t, second, relayInfo, 20*time.Second)
	if len(firstRelayAddrs) == 0 || len(secondRelayAddrs) == 0 {
		t.Fatal("AutoRelay did not advertise both reservations")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := first.Connect(ctx, peer.AddrInfo{ID: second.ID(), Addrs: secondRelayAddrs}); err != nil {
		t.Fatalf("connect through relay: %v", err)
	}
	if !hasLimitedConnection(first, second.ID()) {
		t.Fatal("initial connection did not use Circuit Relay v2")
	}
}

func waitForRelayReservation(t *testing.T, h *Host, relay peer.AddrInfo, timeout time.Duration) []ma.Multiaddr {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.ConnManager().IsProtected(relay.ID, "autorelay") {
			circuit := ma.StringCast(fmt.Sprintf("/p2p/%s/p2p-circuit", relay.ID))
			out := make([]ma.Multiaddr, 0, len(relay.Addrs))
			for _, addr := range relay.Addrs {
				out = append(out, addr.Encapsulate(circuit))
			}
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("host %s did not reserve relay %s within %s", h.ID(), relay.ID, timeout)
	return nil
}

func hasLimitedConnection(h *Host, id peer.ID) bool {
	for _, conn := range h.Network().ConnsToPeer(id) {
		if conn.Stat().Limited {
			return true
		}
	}
	return false
}
