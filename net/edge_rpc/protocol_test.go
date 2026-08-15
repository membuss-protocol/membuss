package edge_rpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/memedge"
)

func TestEdgeRPC_P2PDelegation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Setup Host A (Client / Gateway)
	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("failed to create host A: %v", err)
	}
	defer hA.Close()

	engineA, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create engine A: %v", err)
	}
	defer engineA.Close()
	svcA := NewService(hA, engineA)

	// 2. Setup Host B (Peer / Worker)
	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("failed to create host B: %v", err)
	}
	defer hB.Close()

	engineB, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create engine B: %v", err)
	}
	defer engineB.Close()
	_ = NewService(hB, engineB)

	// Connect Host A to Host B
	infoB := peer.AddrInfo{
		ID:    hB.ID(),
		Addrs: hB.Addrs(),
	}
	if err := hA.Connect(ctx, infoB); err != nil {
		t.Fatalf("failed to connect A to B: %v", err)
	}

	jsCode := `
function handler(req) {
	return {
		status: 200,
		headers: { "X-Worker": "PeerB" },
		body: JSON.stringify({ echo: req.query.msg, worker: "HostB" })
	};
}
`

	rpcReq := &RPCRequest{
		Path:    "/api/echo.js",
		Code:    []byte(jsCode),
		Runtime: memedge.RuntimeJS,
		Req: &memedge.Request{
			Method: "GET",
			Path:   "/api/echo.js",
			Query:  map[string]string{"msg": "HelloFromA"},
		},
	}

	// Test direct remote execution
	resp, err := svcA.ExecuteRemote(ctx, hB.ID(), rpcReq, memedge.TierPeer)
	if err != nil {
		t.Fatalf("remote execution failed: %v", err)
	}

	if resp.Status != 200 {
		t.Errorf("expected status 200, got %d", resp.Status)
	}
	if !strings.Contains(resp.Body, "HelloFromA") {
		t.Errorf("expected body to contain 'HelloFromA', got %s", resp.Body)
	}
	if resp.Headers["X-Worker"] != "PeerB" {
		t.Errorf("expected X-Worker header 'PeerB', got %v", resp.Headers)
	}
	if resp.Tier != memedge.TierPeer {
		t.Errorf("expected tier to be Peer, got %v", resp.Tier)
	}

	// Test 3-tier fallback delegate
	delegateResp, err := svcA.Delegate(ctx, hB.ID(), []peer.ID{hB.ID()}, rpcReq)
	if err != nil {
		t.Fatalf("delegate execution failed: %v", err)
	}
	if delegateResp.Status != 200 {
		t.Errorf("expected status 200 from delegate, got %d", delegateResp.Status)
	}
}

func TestEdgeRPC_RateLimiting(t *testing.T) {
	svc := &Service{
		peerLimits: make(map[peer.ID]*peerLimiter),
	}

	testPeer := peer.ID("12D3KooWTestPeerRateLimit12345")

	// Consume 20 tokens (burst limit)
	for i := 0; i < 20; i++ {
		if !svc.allowPeer(testPeer) {
			t.Fatalf("request %d within burst limit should be allowed", i)
		}
	}

	// 21st request should be rejected by rate limiter
	if svc.allowPeer(testPeer) {
		t.Fatalf("request beyond burst limit should be rate limited")
	}
}

