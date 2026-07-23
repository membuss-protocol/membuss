package plugin

import (
	"context"
	"net/http"
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// HookBus manages universal event subscriptions and interceptors across all Membuss core subsystems.
type HookBus struct {
	mu sync.RWMutex

	// --- Daemon Lifecycle ---
	OnDaemonReady    []func(ctx context.Context, core *Core) error
	OnDaemonShutdown []func(ctx context.Context, core *Core)

	// --- Storage Interceptors ---
	BeforeBlockPut []func(ctx context.Context, blk *store.Block) (*store.Block, error)
	AfterBlockPut  []func(ctx context.Context, m mid.MID, size int64)
	BeforeBlockGet []func(ctx context.Context, targetMID mid.MID) (mid.MID, error)
	AfterBlockGet  []func(ctx context.Context, m mid.MID, data []byte) ([]byte, error)
	AfterBlockDel  []func(ctx context.Context, targetMID mid.MID)

	// --- Merkle DAG & Chunking ---
	OnDAGCreated  []func(ctx context.Context, rootMID mid.MID, rootNode *dag.Node)
	OnDAGUnsealed []func(ctx context.Context, rootMID mid.MID, destPath string)

	// --- P2P Network & Transport ---
	OnPeerConnected    []func(peerID peer.ID)
	OnPeerDisconnected []func(peerID peer.ID)
	StreamHandlers     map[string]network.StreamHandler

	// --- Memex Block Exchange ---
	BeforeBlockRequested []func(ctx context.Context, targetPeer peer.ID, blockMID mid.MID) error
	OnBlockServed        []func(ctx context.Context, clientPeer peer.ID, blockMID mid.MID, bytesSent int64)
	OnBlockReceived      []func(ctx context.Context, providerPeer peer.ID, blockMID mid.MID, bytesReceived int64)

	// --- DHT & Routing ---
	OnProviderAnnounced []func(m mid.MID, providerPeer peer.ID)
	OnProviderQueried   []func(m mid.MID, providers []peer.ID)

	// --- Anchor Operations ---
	OnAnchorHold     []func(ctx context.Context, rootMID mid.MID, bytes int64) error
	OnAnchorSeal     []func(ctx context.Context, rootMID mid.MID, shards int) error
	OnAnchorAutoHeal []func(ctx context.Context, targetMID mid.MID, status string)

	// --- MemNS Mutable Pointers ---
	OnMemNSPublish []func(name string, targetMID mid.MID)
	OnMemNSResolve []func(name string, resolvedMID mid.MID)

	// --- Gateway & Node API HTTP Middleware ---
	GatewayMiddleware []func(http.Handler) http.Handler
	NodeAPIMiddleware []func(http.Handler) http.Handler
}

// NewHookBus constructs an initialized HookBus.
func NewHookBus() *HookBus {
	return &HookBus{
		StreamHandlers: make(map[string]network.StreamHandler),
	}
}

// --- Trigger Methods ---

// TriggerBeforeBlockPut runs all BeforeBlockPut interceptors in order.
func (b *HookBus) TriggerBeforeBlockPut(ctx context.Context, blk *store.Block) (*store.Block, error) {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, blk *store.Block) (*store.Block, error), len(b.BeforeBlockPut))
	copy(hooks, b.BeforeBlockPut)
	b.mu.RUnlock()

	current := blk
	var err error
	for _, fn := range hooks {
		current, err = fn(ctx, current)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

// TriggerAfterBlockPut triggers all AfterBlockPut callbacks.
func (b *HookBus) TriggerAfterBlockPut(ctx context.Context, m mid.MID, size int64) {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, m mid.MID, size int64), len(b.AfterBlockPut))
	copy(hooks, b.AfterBlockPut)
	b.mu.RUnlock()

	for _, fn := range hooks {
		fn(ctx, m, size)
	}
}

// TriggerBeforeBlockGet triggers BeforeBlockGet hooks.
func (b *HookBus) TriggerBeforeBlockGet(ctx context.Context, targetMID mid.MID) (mid.MID, error) {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, targetMID mid.MID) (mid.MID, error), len(b.BeforeBlockGet))
	copy(hooks, b.BeforeBlockGet)
	b.mu.RUnlock()

	current := targetMID
	var err error
	for _, fn := range hooks {
		current, err = fn(ctx, current)
		if err != nil {
			return current, err
		}
	}
	return current, nil
}

// TriggerAfterBlockGet triggers AfterBlockGet hooks, allowing data transformation.
func (b *HookBus) TriggerAfterBlockGet(ctx context.Context, m mid.MID, data []byte) ([]byte, error) {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, m mid.MID, data []byte) ([]byte, error), len(b.AfterBlockGet))
	copy(hooks, b.AfterBlockGet)
	b.mu.RUnlock()

	current := data
	var err error
	for _, fn := range hooks {
		current, err = fn(ctx, m, current)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

// TriggerAfterBlockDel triggers AfterBlockDel hooks.
func (b *HookBus) TriggerAfterBlockDel(ctx context.Context, targetMID mid.MID) {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, targetMID mid.MID), len(b.AfterBlockDel))
	copy(hooks, b.AfterBlockDel)
	b.mu.RUnlock()

	for _, fn := range hooks {
		fn(ctx, targetMID)
	}
}

// TriggerOnAnchorHold triggers OnAnchorHold hooks.
func (b *HookBus) TriggerOnAnchorHold(ctx context.Context, rootMID mid.MID, bytes int64) error {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, rootMID mid.MID, bytes int64) error, len(b.OnAnchorHold))
	copy(hooks, b.OnAnchorHold)
	b.mu.RUnlock()

	for _, fn := range hooks {
		if err := fn(ctx, rootMID, bytes); err != nil {
			return err
		}
	}
	return nil
}

// TriggerOnAnchorSeal triggers OnAnchorSeal hooks.
func (b *HookBus) TriggerOnAnchorSeal(ctx context.Context, rootMID mid.MID, shards int) error {
	b.mu.RLock()
	hooks := make([]func(ctx context.Context, rootMID mid.MID, shards int) error, len(b.OnAnchorSeal))
	copy(hooks, b.OnAnchorSeal)
	b.mu.RUnlock()

	for _, fn := range hooks {
		if err := fn(ctx, rootMID, shards); err != nil {
			return err
		}
	}
	return nil
}
