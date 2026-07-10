// Phase 11: relay discovery via the Membuss DHT.
//
// A node that runs Config.RelayService=true advertises itself as a provider
// for RelaysKey. Provider records are independent per peer, so multiple relay
// operators can advertise concurrently without overwriting one shared value.
package dht

import (
	"context"
	"errors"
	"sort"

	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	routingdiscovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	circuitproto "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/proto"
)

// RelaysKey is the routing-discovery namespace under which relay providers
// advertise. Its value is stable because changing it partitions discovery.
const RelaysKey = "/membuss/relays/v1"

// ErrRelayServiceUnavailable means the local hop service has not started.
// go-libp2p starts it only after the node is known to be publicly reachable.
var ErrRelayServiceUnavailable = errors.New("dht: circuit relay service is not active")

// anchorsKey is the existing anchor key from Phase 6. Re-
// declared here for symmetry with the relay helpers so callers
// do not need to import the anchor package.
const anchorsKey = "/membuss/anchors/v1"

// PublishAsRelay creates or refreshes this node's provider record for the relay
// namespace. Routing discovery caps the returned advertisement TTL at three
// hours, so callers should refresh at least that often.
func (m *MemDHT) PublishAsRelay(ctx context.Context) error {
	if m == nil || m.dht == nil {
		return errors.New("dht: nil")
	}
	h := m.dht.Host()
	if h == nil {
		return errors.New("dht: nil host")
	}
	addrs := h.Addrs()
	if len(addrs) == 0 {
		// No addresses = nothing to publish. This is
		// normal for the in-process test host and not
		// an error.
		return nil
	}
	active := false
	for _, id := range h.Mux().Protocols() {
		if id == circuitproto.ProtoIDv2Hop {
			active = true
			break
		}
	}
	if !active {
		return ErrRelayServiceUnavailable
	}
	_, err := routingdiscovery.NewRoutingDiscovery(m.dht).Advertise(ctx, RelaysKey)
	return err
}

// FindRelays queries the DHT for the relay list and returns a
// deduplicated, sorted set of AddrInfo values. The number of
// candidates is bounded by max; pass 0 to take the default
// (32). Returns an empty slice (never nil) when no relays
// have been published yet.
func (m *MemDHT) FindRelays(ctx context.Context, max int) ([]peer.AddrInfo, error) {
	if m == nil || m.dht == nil {
		return nil, errors.New("dht: nil")
	}
	if max <= 0 {
		max = 32
	}
	findCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	peers, err := routingdiscovery.NewRoutingDiscovery(m.dht).FindPeers(
		findCtx,
		RelaysKey,
		discovery.Limit(max+1), // allow room to discard our own provider record
	)
	if err != nil {
		return nil, err
	}
	// Dedupe by peer.ID and sort for deterministic output.
	seen := make(map[peer.ID]struct{}, max)
	out := make([]peer.AddrInfo, 0, max)
	for p := range peers {
		if p.ID == "" || p.ID == m.dht.PeerID() || len(p.Addrs) == 0 {
			continue
		}
		if _, ok := seen[p.ID]; ok {
			continue
		}
		seen[p.ID] = struct{}{}
		out = append(out, p)
		if len(out) >= max {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// AddrInfoFromHost is a small convenience used by callers that
// have a host but no AddrInfo (e.g. the daemon when wiring
// AutoRelay's static relay list). The returned AddrInfo
// reflects the host's current self-perceived addresses.
func AddrInfoFromHost(h host.Host) peer.AddrInfo {
	if h == nil {
		return peer.AddrInfo{}
	}
	return peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()}
}
