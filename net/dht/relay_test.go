package dht

import (
	"context"
	"errors"
	"testing"
	"time"

	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	circuitproto "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/proto"
)

// TestRelaysKey_Stable guards the DHT key from accidental
// renames. Renaming the key is a wire-format change: any
// in-flight relay announcement under the old key becomes
// invisible to FindRelays.
func TestRelaysKey_Stable(t *testing.T) {
	want := "/membuss/relays/v1"
	if RelaysKey != want {
		t.Fatalf("RelaysKey = %q, want %q", RelaysKey, want)
	}
}

func TestRelayDiscovery_MultipleProviders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	h1, h2, clientHost := newTestHost(t), newTestHost(t), newTestHost(t)
	t.Cleanup(func() { _ = h1.Close(); _ = h2.Close(); _ = clientHost.Close() })
	hopHandler := func(stream network.Stream) { _ = stream.Reset() }
	h1.SetStreamHandler(circuitproto.ProtoIDv2Hop, hopHandler)
	h2.SetStreamHandler(circuitproto.ProtoIDv2Hop, hopHandler)
	d1, err := New(ctx, Config{Host: h1, Mode: kaddht.ModeServer})
	if err != nil {
		t.Fatalf("dht1: %v", err)
	}
	d2, err := New(ctx, Config{Host: h2, Mode: kaddht.ModeServer})
	if err != nil {
		t.Fatalf("dht2: %v", err)
	}
	client, err := New(ctx, Config{Host: clientHost, Mode: kaddht.ModeServer})
	if err != nil {
		t.Fatalf("client dht: %v", err)
	}
	t.Cleanup(func() { _ = d1.Close(); _ = d2.Close(); _ = client.Close() })

	clientInfo := peer.AddrInfo{ID: clientHost.ID(), Addrs: clientHost.Addrs()}
	for name, d := range map[string]*MemDHT{"relay1": d1, "relay2": d2} {
		if err := d.Bootstrap(ctx, []peer.AddrInfo{clientInfo}); err != nil {
			t.Fatalf("%s bootstrap: %v", name, err)
		}
	}
	if err := client.Bootstrap(ctx, []peer.AddrInfo{
		{ID: h1.ID(), Addrs: h1.Addrs()},
		{ID: h2.ID(), Addrs: h2.Addrs()},
	}); err != nil {
		t.Fatalf("client bootstrap: %v", err)
	}
	if err := waitForRoutingTable(ctx, client, 2, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := d1.PublishAsRelay(ctx); err != nil {
		t.Fatalf("publish relay1: %v", err)
	}
	if err := d2.PublishAsRelay(ctx); err != nil {
		t.Fatalf("publish relay2: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := client.FindRelays(ctx, 8)
		if err == nil && len(got) == 2 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	got, err := client.FindRelays(ctx, 8)
	t.Fatalf("relay discovery returned %d providers, err=%v; want 2", len(got), err)
}

// TestFindRelays_Empty exercises the no-published-record
// path: FindRelays must return (nil-or-empty, nil) on a DHT
// that has no record yet, so the caller can fall back to a
// static config list.
func TestFindRelays_Empty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := newTestHost(t)
	t.Cleanup(func() { _ = h.Close() })

	md, err := New(ctx, Config{Host: h})
	if err != nil {
		t.Fatalf("dht: %v", err)
	}
	t.Cleanup(func() { _ = md.Close() })

	// Fresh DHT: no Put has happened, so the lookup misses.
	got, err := md.FindRelays(ctx, 8)
	if err != nil {
		t.Fatalf("FindRelays on empty dht: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("FindRelays returned %d entries, want 0", len(got))
	}
}

// TestPublishAsRelay_NoAddrs is the in-process host case: a
// host that has no addrs cannot publish itself, so
// PublishAsRelay must be a silent no-op (returning nil).
func TestPublishAsRelay_NoAddrs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build a host with NoListenAddrs so Addrs() returns
	// an empty slice. We reuse newTestHost's identity
	// plumbing but force no listen addrs.
	host, _, err := genInProcessHost()
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	defer host.Close()

	md, err := New(ctx, Config{Host: host})
	if err != nil {
		t.Fatalf("dht: %v", err)
	}
	defer md.Close()

	if err := md.PublishAsRelay(ctx); err != nil {
		t.Fatalf("PublishAsRelay with no addrs: %v", err)
	}
}

func TestPublishAsRelay_RequiresActiveService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := newTestHost(t)
	defer h.Close()
	md, err := New(ctx, Config{Host: h})
	if err != nil {
		t.Fatalf("dht: %v", err)
	}
	defer md.Close()

	if err := md.PublishAsRelay(ctx); !errors.Is(err, ErrRelayServiceUnavailable) {
		t.Fatalf("PublishAsRelay error = %v, want ErrRelayServiceUnavailable", err)
	}
}
