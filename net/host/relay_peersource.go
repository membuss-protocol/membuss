package host

import (
	"context"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
)

// RelayFinder discovers up to limit relay candidates. Implementations should
// return promptly when ctx is cancelled.
type RelayFinder func(ctx context.Context, limit int) ([]peer.AddrInfo, error)

// RelayPeerSource bridges host construction and routing-based relay discovery.
// AutoRelay must be configured while the host is built, but the DHT needs that
// host before it can be constructed. RelayPeerSource resolves the cycle: it can
// serve dedicated static relays immediately and waits for SetFinder before
// querying the DHT for the remainder of a request.
type RelayPeerSource struct {
	mu     sync.RWMutex
	static []peer.AddrInfo
	finder RelayFinder
	ready  chan struct{}
	once   sync.Once
}

// NewRelayPeerSource returns a dynamic AutoRelay candidate source. Invalid and
// duplicate static entries are removed up front.
func NewRelayPeerSource(static []peer.AddrInfo) *RelayPeerSource {
	return &RelayPeerSource{
		static: dedupePeerInfo(static, 0),
		ready:  make(chan struct{}),
	}
}

// SetFinder installs or replaces the routing-backed finder. The first call
// releases any AutoRelay request waiting for DHT construction to complete.
func (s *RelayPeerSource) SetFinder(finder RelayFinder) {
	if s == nil || finder == nil {
		return
	}
	s.mu.Lock()
	s.finder = finder
	s.mu.Unlock()
	s.once.Do(func() { close(s.ready) })
}

// PeerSource returns the callback expected by libp2p AutoRelay.
func (s *RelayPeerSource) PeerSource() autorelay.PeerSource {
	return func(ctx context.Context, numPeers int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo)
		go s.find(ctx, numPeers, out)
		return out
	}
}

func (s *RelayPeerSource) find(ctx context.Context, limit int, out chan<- peer.AddrInfo) {
	defer close(out)
	if s == nil || limit <= 0 {
		return
	}

	s.mu.RLock()
	static := append([]peer.AddrInfo(nil), s.static...)
	ready := s.ready
	s.mu.RUnlock()

	seen := make(map[peer.ID]struct{}, limit)
	send := func(info peer.AddrInfo) bool {
		if info.ID == "" {
			return true
		}
		if _, ok := seen[info.ID]; ok {
			return true
		}
		select {
		case out <- info:
			seen[info.ID] = struct{}{}
			return len(seen) < limit
		case <-ctx.Done():
			return false
		}
	}

	for _, info := range static {
		if !send(info) {
			return
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-ready:
	}

	s.mu.RLock()
	finder := s.finder
	s.mu.RUnlock()
	if finder == nil {
		return
	}
	dynamic, err := finder(ctx, limit-len(seen))
	if err != nil {
		return
	}
	for _, info := range dynamic {
		if !send(info) {
			return
		}
	}
}

func dedupePeerInfo(peers []peer.AddrInfo, limit int) []peer.AddrInfo {
	indexes := make(map[peer.ID]int, len(peers))
	seenAddrs := make(map[peer.ID]map[string]struct{}, len(peers))
	out := make([]peer.AddrInfo, 0, len(peers))
	for _, info := range peers {
		if info.ID == "" {
			continue
		}
		idx, ok := indexes[info.ID]
		if !ok {
			idx = len(out)
			indexes[info.ID] = idx
			out = append(out, peer.AddrInfo{ID: info.ID})
			seenAddrs[info.ID] = make(map[string]struct{}, len(info.Addrs))
		}
		for _, addr := range info.Addrs {
			if addr == nil {
				continue
			}
			key := string(addr.Bytes())
			if _, ok := seenAddrs[info.ID][key]; ok {
				continue
			}
			seenAddrs[info.ID][key] = struct{}{}
			out[idx].Addrs = append(out[idx].Addrs, addr)
		}
	}
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}
