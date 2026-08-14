package edge_rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-msgio"
	"github.com/nnlgsakib/membuss/core/memedge"
)

// ProtocolID is the libp2p protocol identifier for MemEdge execution.
const ProtocolID = "/membuss/edge/exec/v1"

// RPCRequest wraps a remote edge execution request.
type RPCRequest struct {
	MID     string           `json:"mid,omitempty"`
	Path    string           `json:"path,omitempty"`
	Code    []byte           `json:"code,omitempty"`
	Runtime memedge.RuntimeType `json:"runtime"`
	Req     *memedge.Request `json:"req"`
	Limits  *memedge.Limits  `json:"limits,omitempty"`
}

// RPCResponse wraps the execution result returned by a peer.
type RPCResponse struct {
	Response *memedge.Response `json:"response"`
	PeerID   string            `json:"peer_id"`
	Tier     memedge.ExecutionTier `json:"tier"`
	Error    string            `json:"error,omitempty"`
}

// Service manages inbound and outbound P2P edge execution streams.
type Service struct {
	h      host.Host
	engine memedge.Engine
	mu     sync.RWMutex
}

// NewService registers the edge execution protocol handler on the libp2p host.
func NewService(h host.Host, engine memedge.Engine) *Service {
	s := &Service{
		h:      h,
		engine: engine,
	}

	if h != nil && engine != nil {
		h.SetStreamHandler(ProtocolID, s.handleStream)
	}

	return s
}

// handleStream handles incoming execution requests from other nodes.
func (s *Service) handleStream(stream network.Stream) {
	defer stream.Close()

	r := msgio.NewVarintReaderSize(stream, 10<<20)
	w := msgio.NewVarintWriter(stream)

	msgBytes, err := r.ReadMsg()
	if err != nil {
		return
	}
	r.ReleaseMsg(msgBytes)

	var req RPCRequest
	if err := json.Unmarshal(msgBytes, &req); err != nil {
		resp := RPCResponse{
			Error: "invalid request payload: " + err.Error(),
		}
		out, _ := json.Marshal(resp)
		_ = w.WriteMsg(out)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	execResp, execErr := s.engine.Execute(ctx, req.Code, req.Runtime, req.Req, req.Limits)
	var peerIDStr string
	if s.h != nil {
		peerIDStr = s.h.ID().String()
	}

	rpcResp := RPCResponse{
		Response: execResp,
		PeerID:   peerIDStr,
		Tier:     memedge.TierPeer,
	}

	if execErr != nil {
		rpcResp.Error = execErr.Error()
	}

	out, _ := json.Marshal(rpcResp)
	_ = w.WriteMsg(out)
}

// ExecuteRemote dials a target peer and executes the function remotely.
func (s *Service) ExecuteRemote(ctx context.Context, targetPeer peer.ID, rpcReq *RPCRequest, tier memedge.ExecutionTier) (*memedge.Response, error) {
	if s.h == nil {
		return nil, errors.New("libp2p host is nil")
	}

	stream, err := s.h.NewStream(ctx, targetPeer, ProtocolID)
	if err != nil {
		return nil, fmt.Errorf("open edge stream to %s: %w", targetPeer, err)
	}
	defer stream.Close()

	w := msgio.NewVarintWriter(stream)
	r := msgio.NewVarintReaderSize(stream, 10<<20)

	payload, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	if err := w.WriteMsg(payload); err != nil {
		return nil, fmt.Errorf("send rpc request: %w", err)
	}

	respBytes, err := r.ReadMsg()
	if err != nil {
		return nil, fmt.Errorf("read rpc response: %w", err)
	}
	defer r.ReleaseMsg(respBytes)

	var rpcResp RPCResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal rpc response: %w", err)
	}

	if rpcResp.Response == nil {
		if rpcResp.Error != "" {
			return nil, errors.New(rpcResp.Error)
		}
		return nil, errors.New("empty response received from peer")
	}

	rpcResp.Response.Tier = tier
	return rpcResp.Response, nil
}

// Delegate executes a 3-tier fallback strategy:
// 1. Try Publisher Peer (if non-empty and connected)
// 2. Try Random connected peer from candidate list
// 3. Fallback to local engine execution (Tier 3)
func (s *Service) Delegate(
	ctx context.Context,
	publisherPeer peer.ID,
	candidatePeers []peer.ID,
	rpcReq *RPCRequest,
) (*memedge.Response, error) {
	// 1. Tier 1: Try publisher if online
	if publisherPeer != "" && s.h != nil && s.h.Network().Connectedness(publisherPeer) == network.Connected {
		pubCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		resp, err := s.ExecuteRemote(pubCtx, publisherPeer, rpcReq, memedge.TierPublisher)
		cancel()
		if err == nil && resp != nil && resp.Status != 0 {
			return resp, nil
		}
	}

	// 2. Tier 2: Try random candidate peer
	if len(candidatePeers) > 0 {
		shuffled := make([]peer.ID, len(candidatePeers))
		copy(shuffled, candidatePeers)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		for _, p := range shuffled {
			if p == publisherPeer || (s.h != nil && p == s.h.ID()) {
				continue
			}
			peerCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			resp, err := s.ExecuteRemote(peerCtx, p, rpcReq, memedge.TierPeer)
			cancel()
			if err == nil && resp != nil && resp.Status != 0 {
				return resp, nil
			}
		}
	}

	// 3. Tier 3: Local execution
	if s.engine != nil {
		resp, err := s.engine.Execute(ctx, rpcReq.Code, rpcReq.Runtime, rpcReq.Req, rpcReq.Limits)
		if resp != nil {
			resp.Tier = memedge.TierGateway
		}
		return resp, err
	}

	return nil, errors.New("all execution tiers failed and local engine unavailable")
}
