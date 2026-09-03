// Package manifest_rpc serves erasure coding manifests to peers over libp2p.
//
// Erasure manifests live in the origin node's meta keyspace and normally
// never cross the wire. A node that receives individual shards via Memex
// therefore cannot run k-of-n reconstruction on its own. This protocol lets
// a remote node ask any peer holding the manifest for it, making
// off-origin reconstruction possible.
package manifest_rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-msgio"
	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	membusspb "github.com/nnlgsakib/membuss/proto"
	"google.golang.org/protobuf/proto"
)

// ProtocolID is the libp2p protocol identifier for manifest exchange.
const ProtocolID = "/membuss/manifest/v1"

// maxMessageBytes bounds inbound frames. Manifests are tiny protobufs;
// anything approaching this limit is malformed or hostile.
const maxMessageBytes = 4 << 20

// RPCRequest asks a peer for the erasure manifest of one MID.
type RPCRequest struct {
	Mid string `json:"mid"`
}

// RPCResponse carries the raw protobuf manifest bytes. An empty Manifest
// together with an empty Error means not-found. RootMid, when set, is the
// serving peer's hint for the ingest root whose erasure shard set covers
// the requested MID.
type RPCResponse struct {
	Manifest []byte `json:"manifest,omitempty"`
	RootMid  string `json:"root_mid,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ManifestResult is what a manifest fetch yields: the protobuf
// manifest plus the serving peer's root hint for shard-set discovery.
type ManifestResult struct {
	// Manifest is nil when no peer held the manifest.
	Manifest *membusspb.ErasureManifest
	// RootMid is the serving peer's hint for the ingest root this
	// MID belongs to; empty when unknown or not found.
	RootMid string
	// ServedBy is the peer that returned the manifest; empty when none did.
	ServedBy peer.ID
}

type peerLimiter struct {
	tokens    float64
	lastCheck time.Time
}

// Service serves erasure manifests to peers over libp2p streams.
type Service struct {
	h          host.Host
	bs         store.Blockstore
	peerLimits map[peer.ID]*peerLimiter
	limiterMu  sync.Mutex
}

// NewService registers the manifest protocol handler on the libp2p host.
// The handler is registered only when both the host and the store are set,
// so read-only clients can share the constructor safely.
func NewService(h host.Host, s store.Blockstore) *Service {
	svc := &Service{
		h:          h,
		bs:         s,
		peerLimits: make(map[peer.ID]*peerLimiter),
	}
	if h != nil && s != nil {
		h.SetStreamHandler(ProtocolID, svc.handleStream)
	}
	return svc
}

// allowPeer applies a per-peer token bucket (60 burst, 10/s refill) and
// prunes stale entries once the map grows large.
func (s *Service) allowPeer(p peer.ID) bool {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()

	now := time.Now()
	lim, exists := s.peerLimits[p]
	if !exists {
		s.peerLimits[p] = &peerLimiter{
			tokens:    60.0,
			lastCheck: now,
		}
		if len(s.peerLimits) > 10000 {
			for k, v := range s.peerLimits {
				if now.Sub(v.lastCheck) > 10*time.Minute {
					delete(s.peerLimits, k)
				}
			}
		}
		return true
	}

	elapsed := now.Sub(lim.lastCheck).Seconds()
	lim.lastCheck = now
	lim.tokens += elapsed * 10.0
	if lim.tokens > 60.0 {
		lim.tokens = 60.0
	}

	if lim.tokens >= 1.0 {
		lim.tokens -= 1.0
		return true
	}
	return false
}

// handleStream answers a single manifest request from another node.
// Bad input produces an error response, never a panic.
func (s *Service) handleStream(stream network.Stream) {
	defer stream.Close()

	r := msgio.NewVarintReaderSize(stream, maxMessageBytes)
	w := msgio.NewVarintWriter(stream)

	if !s.allowPeer(stream.Conn().RemotePeer()) {
		out, _ := json.Marshal(RPCResponse{Error: "rate limit exceeded"})
		_ = w.WriteMsg(out)
		return
	}

	msgBytes, err := r.ReadMsg()
	if err != nil {
		return
	}
	r.ReleaseMsg(msgBytes)

	var req RPCRequest
	if err := json.Unmarshal(msgBytes, &req); err != nil {
		out, _ := json.Marshal(RPCResponse{Error: "invalid request payload"})
		_ = w.WriteMsg(out)
		return
	}

	resp := RPCResponse{}
	m, perr := mid.Parse(req.Mid)
	if perr != nil {
		resp.Error = "invalid mid"
	} else if manifest, gerr := erasure.GetManifest(s.bs, m); gerr != nil {
		resp.Error = gerr.Error()
	} else if manifest != nil {
		raw, merr := proto.Marshal(manifest)
		if merr != nil {
			resp.Error = "marshal manifest: " + merr.Error()
		} else {
			resp.Manifest = raw
			if rootRaw, rerr := s.bs.GetMeta(erasure.ManifestRootKey(m)); rerr == nil && len(rootRaw) > 0 {
				resp.RootMid = string(rootRaw)
			}
		}
	}
	// Both fields left empty => not-found; the client maps that to (nil, nil).

	out, _ := json.Marshal(resp)
	_ = w.WriteMsg(out)
}

// Close removes the protocol handler from the host.
func (s *Service) Close() {
	if s.h != nil {
		s.h.RemoveStreamHandler(ProtocolID)
	}
}

// FetchManifest dials pid and returns its erasure manifest for m together
// with the peer's root hint (empty when the peer has none).
// Result.Manifest is nil with a nil error when the peer reports not-found.
func FetchManifest(ctx context.Context, h host.Host, pid peer.ID, m mid.MID) (ManifestResult, error) {
	if h == nil {
		return ManifestResult{}, errors.New("libp2p host is nil")
	}

	stream, err := h.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return ManifestResult{}, fmt.Errorf("open manifest stream to %s: %w", pid, err)
	}
	defer stream.Close()

	// msgio reads have no context; carry the caller's deadline onto the
	// stream so a hung peer cannot block past the context timeout.
	if dl, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(dl)
	}

	w := msgio.NewVarintWriter(stream)
	payload, err := json.Marshal(RPCRequest{Mid: m.String()})
	if err != nil {
		return ManifestResult{}, fmt.Errorf("marshal manifest request: %w", err)
	}
	if err := w.WriteMsg(payload); err != nil {
		return ManifestResult{}, fmt.Errorf("send manifest request: %w", err)
	}

	r := msgio.NewVarintReaderSize(stream, maxMessageBytes)
	respBytes, err := r.ReadMsg()
	if err != nil {
		return ManifestResult{}, fmt.Errorf("read manifest response: %w", err)
	}
	r.ReleaseMsg(respBytes)

	var resp RPCResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return ManifestResult{}, fmt.Errorf("unmarshal manifest response: %w", err)
	}
	if resp.Error != "" {
		return ManifestResult{}, errors.New(resp.Error)
	}
	if len(resp.Manifest) == 0 {
		return ManifestResult{}, nil
	}

	var manifest membusspb.ErasureManifest
	if err := proto.Unmarshal(resp.Manifest, &manifest); err != nil {
		return ManifestResult{}, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return ManifestResult{Manifest: &manifest, RootMid: resp.RootMid, ServedBy: pid}, nil
}

// FetchManifestFromPeers tries each peer in order until one returns the
// manifest for m. Returns the result of the first success; the zero
// ManifestResult when no peer holds it.
func FetchManifestFromPeers(ctx context.Context, h host.Host, peers []peer.ID, m mid.MID, perPeerTimeout time.Duration) ManifestResult {
	if h == nil {
		return ManifestResult{}
	}

	self := h.ID()
	for _, p := range peers {
		if p == self || p == "" {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, perPeerTimeout)
		res, err := FetchManifest(pctx, h, p, m)
		cancel()
		if err == nil && res.Manifest != nil {
			return res
		}
	}
	return ManifestResult{}
}
