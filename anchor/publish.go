package anchor

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/core/mid"
)

const (
	// ContentExchangeProto is the libp2p protocol ID for the
	// direct content-exchange stream. The anchor opens a
	// stream to each connected peer; the peer responds with a
	// JSON array of its sealed MID strings.
	ContentExchangeProto = "/membuss/content-exchange/1.0.0"

	// maxSeedListBytes caps how much data we read from a
	// content-exchange stream (64 MiB supports ~1M sealed MIDs).
	maxSeedListBytes = 64 << 20
)

// SealedLister is the subset of store.Store the publisher
// needs to enumerate sealed MIDs.
type SealedLister interface {
	AllSealed() ([]mid.MID, error)
}

// DHTProvider is the subset of DHT methods needed by the publisher
// to announce sealed MIDs to the network.
type DHTProvider interface {
	Provide(ctx context.Context, m mid.MID) error
}

// ContentPublisher runs on every node and serves sealed MID
// lists on the content-exchange stream handler. It also
// periodically publishes the sealed list to the DHT as a
// fallback for peers not directly connected.
type ContentPublisher struct {
	host   host.Host
	store  SealedLister
	dht    DHTProvider
	mu     sync.Mutex
	closed bool
	doneCh chan struct{}
}

// NewContentPublisher creates and registers the stream
// handler. Call Start to begin background DHT publishing.
func NewContentPublisher(h host.Host, store SealedLister, dht ...DHTProvider) *ContentPublisher {
	cp := &ContentPublisher{
		host:   h,
		store:  store,
		doneCh: make(chan struct{}),
	}
	if len(dht) > 0 {
		cp.dht = dht[0]
	}
	if h != nil {
		h.SetStreamHandler(ContentExchangeProto, cp.handleStream)
	}
	return cp
}

// Start launches a background goroutine that publishes the
// sealed MID list to the DHT periodically.
func (cp *ContentPublisher) Start(ctx context.Context) {
	go cp.loop(ctx)
}

// Stop signals the background goroutine to exit.
func (cp *ContentPublisher) Stop() {
	cp.mu.Lock()
	if !cp.closed {
		cp.closed = true
		close(cp.doneCh)
	}
	cp.mu.Unlock()
}

func (cp *ContentPublisher) loop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cp.doneCh:
			return
		case <-t.C:
			cp.publishToDHT(ctx)
		}
	}
}

func (cp *ContentPublisher) publishToDHT(ctx context.Context) {
	if cp.dht == nil || cp.store == nil {
		return
	}
	mids, err := cp.store.AllSealed()
	if err != nil {
		return
	}
	for _, m := range mids {
		select {
		case <-ctx.Done():
			return
		case <-cp.doneCh:
			return
		default:
			_ = cp.dht.Provide(ctx, m)
		}
	}
}

// handleStream serves a content-exchange request. The
// requester opens a stream, reads a single JSON frame of
// sealed MID strings. No handshake, no request messages —
// just the response.
func (cp *ContentPublisher) handleStream(s network.Stream) {
	mids, err := cp.store.AllSealed()
	if err != nil {
		_ = s.Reset()
		return
	}
	strs := make([]string, 0, len(mids))
	for _, m := range mids {
		strs = append(strs, m.String())
	}
	enc := json.NewEncoder(s)
	if err := enc.Encode(strs); err != nil {
		_ = s.Reset()
		return
	}
	_ = s.Close()
}

// DiscoverContent opens a content-exchange stream to each
// connected peer and reads their sealed MID list. It returns
// announcements for MIDs the caller does not already know.
func DiscoverContent(ctx context.Context, h host.Host, known map[string]struct{}) ([]ContentAnnouncement, error) {
	peers := h.Network().Peers()
	if len(peers) == 0 {
		return nil, nil
	}

	type result struct {
		peer peer.ID
		mids []mid.MID
		err  error
	}
	results := make(chan result, len(peers))
	for _, p := range peers {
		go func(pid peer.ID) {
			mids, err := fetchPeerSealed(ctx, h, pid)
			results <- result{peer: pid, mids: mids, err: err}
		}(p)
	}

	var out []ContentAnnouncement
	for i := 0; i < len(peers); i++ {
		r := <-results
		if r.err != nil {
			continue
		}
		for _, m := range r.mids {
			if _, exists := known[m.String()]; !exists {
				out = append(out, ContentAnnouncement{MID: m, Source: r.peer})
			}
		}
	}
	return out, nil
}

// ContentAnnouncement is a MID discovered from a peer, along with
// the peer that announced it so the engine can fetch directly from
// the source before DHT provider records exist.
type ContentAnnouncement struct {
	MID    mid.MID
	Source peer.ID
}

// fetchPeerSealed opens a content-exchange stream to pid,
// reads the JSON array of sealed MID strings, and returns
// them.
func fetchPeerSealed(ctx context.Context, h host.Host, pid peer.ID) ([]mid.MID, error) {
	s, err := h.NewStream(ctx, pid, ContentExchangeProto)
	if err != nil {
		return nil, err
	}

	limitedReader := io.LimitReader(s, maxSeedListBytes)
	var strs []string
	dec := json.NewDecoder(limitedReader)
	if err := dec.Decode(&strs); err != nil {
		_ = s.Reset()
		return nil, err
	}

	out := make([]mid.MID, 0, len(strs))
	for _, sStr := range strs {
		m, err := mid.Parse(sStr)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	_ = s.Close()
	return out, nil
}
