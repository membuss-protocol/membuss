// Package anchor implements the Anchor Node full-sync engine.
//
// Anchor nodes ensure content persists even when original
// providers go offline. A node configured with
// AnchorMode=true runs an AnchorEngine that:
//
//   - Discovers content announced on the DHT and pulls it
//     into the local store via Memex.
//   - Runs Mem-Herald with StrategyAll so every block in
//     the local store is announced to the DHT.
//   - Publishes itself as an anchor so other nodes can
//     discover it as a fallback provider.
//
// The engine is intentionally conservative: it never deletes
// content from the local store, it rate-limits its DHT
// queries, and it shuts down cleanly when the host context
// is cancelled.
package anchor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/net/dht"
	"github.com/nnlgsakib/membuss/net/herald"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// AnchorRegistryKey is the local store meta key under which
// the anchor peer list is persisted as JSON.
const AnchorRegistryKey = "/membuss/anchors/v1"

// DefaultDiscoveryInterval is the time between the anchor
// engine's discovery + fetch rounds.
const DefaultDiscoveryInterval = 30 * time.Second

// MaxEnqueueBacklog caps the number of externally-queued
// MIDs the engine will process per round.
const MaxEnqueueBacklog = 1024

// DefaultHealthEvery is how many discovery rounds elapse between
// anchor health checks. Reachability changes slowly relative to the
// discovery cadence, so health is checked less often than discovery
// to avoid needless dial churn.
const DefaultHealthEvery = 5

// anchorFailThreshold is the number of consecutive failed health
// checks before a non-sticky anchor is pruned. A small threshold
// avoids evicting an anchor on a single transient blip.
const anchorFailThreshold = 3

// maxHealthChecksPerRound bounds how many anchors are dialed in a
// single health round so the check never blocks the loop on a large
// registry.
const maxHealthChecksPerRound = 16

// anchorHealthDialTimeout bounds a single reachability dial.
const anchorHealthDialTimeout = 10 * time.Second

// anchorAttemptBackoff is how long the engine waits before
// re-attempting a discovered MID it could not acquire. Without a
// backoff, content that no reachable peer can serve would be
// re-enqueued (and re-logged) on every discovery round forever.
const anchorAttemptBackoff = 10 * time.Minute

// AnchorStore is the subset of store.Store the anchor
// engine actually depends on. Splitting it out keeps tests
// free of BadgerDB and lets the in-memory store satisfy the
// engine without dragging the full Phase 2 surface into
// every test binary.
type AnchorStore interface {
	herald.SealedLister

	Size() (uint64, error)
	Put(m mid.MID, data []byte) error
	Has(m mid.MID) (bool, error)
	PutMeta(key string, value []byte) error
	GetMeta(key string) ([]byte, error)
	Seal(m mid.MID, recursive bool) error
	Close() error
}

// ProviderResolver returns the peers that should be asked to
// serve a given MID. The default implementation wraps the
// local DHT (see defaultProviderResolver); tests can inject
// a direct resolver that returns a known peer list without
// depending on DHT provider-record propagation.
type ProviderResolver interface {
	Resolve(ctx context.Context, m mid.MID) ([]peer.AddrInfo, error)
}

// defaultProviderResolver is the production implementation
// of ProviderResolver. It calls the local DHT and pads the
// result with registered anchor peers.
type defaultProviderResolver struct {
	dht     *dht.MemDHT
	anchors func() []peer.AddrInfo
}

func (r *defaultProviderResolver) Resolve(ctx context.Context, m mid.MID) ([]peer.AddrInfo, error) {
	provs, err := r.dht.FindProviders(ctx, m)
	if err != nil {
		return nil, err
	}
	return mergeAnchors(provs, r.anchors()), nil
}

// Fetcher is the contract the anchor engine uses to pull
// content from a peer. The default implementation is built
// on top of net/memex.
type Fetcher interface {
	Fetch(ctx context.Context, root mid.MID, providers []peer.AddrInfo) error
}

// Logger is the optional structured logger interface the
// engine uses. nil falls back to a no-op.
type Logger interface {
	Infof(format string, args ...any)
	Errorf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Errorf(string, ...any) {}

// Config configures an AnchorEngine.
type Config struct {
	// Host is the local libp2p host. Required.
	Host host.Host
	// DHT is the local DHT facade. Required.
	DHT *dht.MemDHT
	// Store is the local content store. Required.
	Store AnchorStore
	// Herald is the local Mem-Herald. Required.
	Herald *herald.MemHerald
	// Fetcher pulls content. Required.
	Fetcher Fetcher
	// ProviderResolver resolves providers for a given MID.
	// If nil, the engine builds a default that calls
	// DHT.FindProviders and pads with registered anchors.
	ProviderResolver ProviderResolver
	// DiscoveryInterval is the time between discovery +
	// fetch rounds. Default is DefaultDiscoveryInterval.
	DiscoveryInterval time.Duration
	// HealthEvery is how many discovery rounds elapse between
	// anchor health checks. Zero uses DefaultHealthEvery. A
	// negative value disables health checking entirely.
	HealthEvery int
	// BootstrapAnchors is the initial set of anchor peers
	// the engine should trust on startup. Bootstrap anchors are
	// "sticky": they are never pruned by health checks, since they
	// reflect explicit operator intent and are re-added on restart.
	BootstrapAnchors []peer.AddrInfo
	// Logger is optional; nil means silent.
	Logger Logger
}

// enqueuedMID is a MID queued for acquisition together with the
// peer the engine learned it from. Carrying the source peer lets
// the engine fetch directly from the announcer instead of relying
// solely on DHT provider records, which may not exist yet on a
// young network.
type enqueuedMID struct {
	mid    mid.MID
	source peer.ID // zero value = no known source (e.g. re-seed)
}

// AnchorEngine is the Anchor Node full-sync engine.
type AnchorEngine struct {
	cfg    Config
	logger Logger

	mu      sync.Mutex
	anchors map[peer.ID]peer.AddrInfo
	backlog []enqueuedMID
	started time.Time
	synced  int64
	// attempts records the last time the engine tried to acquire a
	// MID it learned about, keyed by MID string. It is used both to
	// suppress redundant re-enqueues/logging and to back off retries
	// for content that no reachable peer can currently serve. A MID
	// that has been sealed is removed, so it is never revisited.
	attempts map[string]time.Time

	// sticky holds anchors that must never be pruned by health
	// checks (the operator-provided bootstrap set).
	sticky map[peer.ID]struct{}
	// healthFails counts consecutive failed health checks per anchor.
	healthFails map[peer.ID]int
	// roundNum counts discovery rounds; health checks run every
	// HealthEvery rounds.
	roundNum int

	resolver ProviderResolver

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// New constructs an AnchorEngine. Call Start to begin the
// background loop.
func New(cfg Config) (*AnchorEngine, error) {
	if cfg.Host == nil {
		return nil, errors.New("anchor: nil host")
	}
	if cfg.DHT == nil {
		return nil, errors.New("anchor: nil dht")
	}
	if cfg.Store == nil {
		return nil, errors.New("anchor: nil store")
	}
	if cfg.Herald == nil {
		return nil, errors.New("anchor: nil herald")
	}
	if cfg.Fetcher == nil {
		return nil, errors.New("anchor: nil fetcher")
	}
	if cfg.DiscoveryInterval <= 0 {
		cfg.DiscoveryInterval = DefaultDiscoveryInterval
	}
	if cfg.HealthEvery == 0 {
		cfg.HealthEvery = DefaultHealthEvery
	}
	if cfg.Logger == nil {
		cfg.Logger = nopLogger{}
	}
	return &AnchorEngine{
		cfg:         cfg,
		logger:      cfg.Logger,
		anchors:     make(map[peer.ID]peer.AddrInfo),
		attempts:    make(map[string]time.Time),
		sticky:      make(map[peer.ID]struct{}),
		healthFails: make(map[peer.ID]int),
		resolver:    cfg.ProviderResolver,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}, nil
}

// Start loads the persisted anchor registry, registers the
// bootstrap anchors, and launches the discovery loop.
func (e *AnchorEngine) Start(ctx context.Context) error {
	e.started = time.Now()
	if e.resolver == nil {
		dht := e.cfg.DHT
		e.resolver = &defaultProviderResolver{dht: dht, anchors: func() []peer.AddrInfo {
			return e.AnchorPeers()
		}}
	}
	e.loadRegistry()
	if val, err := e.cfg.Store.GetMeta("anchor_synced"); err == nil && len(val) == 8 {
		atomic.StoreInt64(&e.synced, int64(binary.BigEndian.Uint64(val)))
	}
	for _, ai := range e.cfg.BootstrapAnchors {
		e.AddAnchor(ai)
		e.mu.Lock()
		e.sticky[ai.ID] = struct{}{}
		e.mu.Unlock()
	}
	go e.loop(ctx)
	return nil
}

// Stop signals the loop to exit and waits for it.
func (e *AnchorEngine) Stop() {
	e.stopOnce.Do(func() { close(e.stopCh) })
	<-e.doneCh
}

// Enqueue asks the anchor engine to ensure root is locally
// stored. Safe to call from any goroutine. It records no source
// peer, so acquisition falls back to DHT provider lookup.
func (e *AnchorEngine) Enqueue(root mid.MID) {
	e.enqueueFrom(root, "")
}

// enqueueFrom queues root for acquisition, remembering the peer it
// was learned from, and reports whether it was actually queued.
// MIDs attempted within anchorAttemptBackoff are skipped (returning
// false) so unreachable content does not churn the backlog or the
// logs every round.
func (e *AnchorEngine) enqueueFrom(root mid.MID, source peer.ID) bool {
	if root.IsZero() {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if last, ok := e.attempts[root.String()]; ok && time.Since(last) < anchorAttemptBackoff {
		return false
	}
	if len(e.backlog) >= MaxEnqueueBacklog {
		e.backlog = e.backlog[1:]
	}
	e.backlog = append(e.backlog, enqueuedMID{mid: root, source: source})
	return true
}

// AddAnchor adds ai to the local anchor registry.
func (e *AnchorEngine) AddAnchor(ai peer.AddrInfo) {
	if ai.ID == "" {
		return
	}
	e.mu.Lock()
	e.anchors[ai.ID] = ai
	e.mu.Unlock()
	e.cfg.Host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.PermanentAddrTTL)
}

// RemoveAnchor removes a peer from the local anchor registry and
// clears its addresses from the peerstore. Anchor addresses are
// stored with a permanent TTL, so they must be explicitly cleared or
// they would linger forever after the anchor is removed.
func (e *AnchorEngine) RemoveAnchor(id peer.ID) {
	e.mu.Lock()
	delete(e.anchors, id)
	delete(e.healthFails, id)
	e.mu.Unlock()
	if e.cfg.Host != nil {
		e.cfg.Host.Peerstore().ClearAddrs(id)
	}
}

// checkAnchorHealth dials a bounded sample of unhealthy anchors and
// prunes those that fail anchorFailThreshold consecutive rounds.
// Sticky (bootstrap) anchors are checked but never pruned. Network
// dials happen without any lock held; registry mutations are done
// under e.mu.
func (e *AnchorEngine) checkAnchorHealth(ctx context.Context) {
	if e.cfg.Host == nil {
		return
	}

	// Snapshot the current anchor set under the lock.
	e.mu.Lock()
	candidates := make([]peer.AddrInfo, 0, len(e.anchors))
	for _, ai := range e.anchors {
		candidates = append(candidates, ai)
	}
	e.mu.Unlock()

	checked := 0
	for _, ai := range candidates {
		if checked >= maxHealthChecksPerRound {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		default:
		}

		// Never dial ourselves.
		if ai.ID == e.cfg.Host.ID() {
			continue
		}

		reachable := e.probeAnchor(ctx, ai)
		checked++

		e.mu.Lock()
		_, isSticky := e.sticky[ai.ID]
		if reachable {
			delete(e.healthFails, ai.ID)
			e.mu.Unlock()
			continue
		}
		if isSticky {
			// Sticky anchors are never pruned; just record the miss.
			e.mu.Unlock()
			continue
		}
		e.healthFails[ai.ID]++
		fails := e.healthFails[ai.ID]
		e.mu.Unlock()

		if fails >= anchorFailThreshold {
			e.logger.Infof("anchor: pruning unreachable anchor %s after %d failed checks", ai.ID, fails)
			e.RemoveAnchor(ai.ID)
		}
	}
}

// probeAnchor reports whether the anchor is currently reachable: it is
// healthy if already connected, otherwise a short-lived dial is
// attempted.
func (e *AnchorEngine) probeAnchor(ctx context.Context, ai peer.AddrInfo) bool {
	if e.cfg.Host.Network().Connectedness(ai.ID) == network.Connected {
		return true
	}
	dialCtx, cancel := context.WithTimeout(ctx, anchorHealthDialTimeout)
	defer cancel()
	return e.cfg.Host.Connect(dialCtx, ai) == nil
}

// AnchorPeers returns a snapshot of the current anchor
// registry.
func (e *AnchorEngine) AnchorPeers() []peer.AddrInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]peer.AddrInfo, 0, len(e.anchors))
	for _, ai := range e.anchors {
		out = append(out, ai)
	}
	return out
}

// PublishSelf publishes this node's identity under
// AnchorRegistryKey. The wire format is JSON-encoded
// {id, addrs}.
func (e *AnchorEngine) PublishSelf(ctx context.Context) error {
	ai := peer.AddrInfo{
		ID:    e.cfg.Host.ID(),
		Addrs: e.cfg.Host.Addrs(),
	}
	payload, err := encodeAddrInfo(ai)
	if err != nil {
		return err
	}
	return e.cfg.DHT.PutValue(ctx, AnchorRegistryKey, payload)
}

// AnchorStatus is the JSON-shaped status the engine reports
// via its Status() method and the /anchor/status HTTP
// endpoint.
type AnchorStatus struct {
	PeerID     string        `json:"peer_id"`
	Uptime     time.Duration `json:"uptime"`
	BlocksHeld int64         `json:"blocks_held"`
	Anchors    int           `json:"anchors"`
	Backlog    int           `json:"backlog"`
	Synced     int64         `json:"synced"`
}

// Status returns a snapshot of the engine's stats.
func (e *AnchorEngine) Status() AnchorStatus {
	e.mu.Lock()
	backlog := len(e.backlog)
	anchors := len(e.anchors)
	e.mu.Unlock()
	var held int64
	if ab, ok := e.cfg.Store.(interface {
		AllBlocks() ([]mid.MID, error)
	}); ok {
		if blocks, err := ab.AllBlocks(); err == nil {
			held = int64(len(blocks))
		}
	} else if lenGetter, ok := e.cfg.Store.(interface {
		Len() int
	}); ok {
		held = int64(lenGetter.Len())
	}
	return AnchorStatus{
		PeerID:     e.cfg.Host.ID().String(),
		Uptime:     time.Since(e.started),
		BlocksHeld: held,
		Anchors:    anchors,
		Backlog:    backlog,
		Synced:     atomic.LoadInt64(&e.synced),
	}
}

func (e *AnchorEngine) loop(ctx context.Context) {
	defer close(e.doneCh)
	t := time.NewTicker(e.cfg.DiscoveryInterval)
	defer t.Stop()
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-time.After(2 * time.Second):
			if err := e.PublishSelf(ctx); err != nil {
				e.logger.Errorf("anchor: publish self: %v", err)
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-t.C:
			e.tick(ctx)
		}
	}
}

func (e *AnchorEngine) tick(ctx context.Context) {
	e.purgeStaleAttempts(time.Now())

	// Run an anchor health check every HealthEvery rounds so
	// unreachable anchors are pruned instead of accumulating forever.
	e.mu.Lock()
	e.roundNum++
	round := e.roundNum
	e.mu.Unlock()
	if e.cfg.HealthEvery > 0 && round%e.cfg.HealthEvery == 0 {
		e.checkAnchorHealth(ctx)
	}

	e.persistRegistry()

	// Discover content from connected peers via the
	// direct content-exchange stream.
	e.discoverFromPeers(ctx)

	e.mu.Lock()
	pending := e.backlog
	e.backlog = nil
	e.mu.Unlock()

	for _, item := range pending {
		e.fetchIfMissing(ctx, item.mid, item.source)
	}

	sealed, err := e.cfg.Store.AllSealed()
	if err != nil || len(sealed) == 0 {
		return
	}
	maxSample := 4
	if len(sealed) < maxSample {
		maxSample = len(sealed)
	}
	for i := 0; i < maxSample; i++ {
		m := sealed[i]
		// Only re-acquire sealed roots whose bytes are actually
		// missing locally; already-held roots need nothing. No source
		// peer is known here, so acquisition falls back to DHT/anchors.
		if !e.shouldFetch(m) {
			continue
		}
		e.fetchIfMissing(ctx, m, "")
	}
}

// discoverFromPeers opens content-exchange streams to all
// connected peers and enqueues any sealed MIDs we don't
// already have.
func (e *AnchorEngine) discoverFromPeers(ctx context.Context) {
	known := make(map[string]struct{})
	sealed, err := e.cfg.Store.AllSealed()
	if err == nil {
		for _, m := range sealed {
			known[m.String()] = struct{}{}
		}
	}

	announcements, err := DiscoverContent(ctx, e.cfg.Host, known)
	if err != nil {
		e.logger.Errorf("anchor: discover from peers: %v", err)
		return
	}

	// Count only MIDs actually queued this round. Content still in
	// its retry backoff is suppressed, so the log reflects genuine
	// new discovery instead of re-reporting the same unreachable
	// MIDs every round.
	queued := 0
	for _, a := range announcements {
		if e.enqueueFrom(a.MID, a.Source) {
			queued++
		}
	}

	if queued > 0 {
		e.logger.Infof("anchor: discovered %d new MIDs from peers", queued)
	}
}

// sealFetched seals a MID that was just fetched so GC
// never deletes it.
func (e *AnchorEngine) sealFetched(ctx context.Context, m mid.MID) {
	if err := e.cfg.Store.Seal(m, false); err != nil {
		e.logger.Errorf("anchor: seal fetched %s: %v", m.String()[:12], err)
	}
}

// fetchIfMissing ensures the engine holds and has sealed m. It is
// the acquisition counterpart to discovery, and the two must agree
// on what "done" means: discovery reports a MID as new until it is
// sealed, so acquisition must always end by sealing.
//
// Three cases:
//   - Already held but unsealed: seal it (the block arrived some
//     other way, e.g. a prior fetch that was not sealed). Without
//     this the MID is rediscovered every round forever.
//   - Not held: fetch it, preferring the peer that announced it as
//     a provider and merging in DHT providers and known anchors,
//     then seal.
//   - Unreachable (no providers or fetch failed): record the
//     attempt so it backs off instead of retrying every round.
func (e *AnchorEngine) fetchIfMissing(ctx context.Context, m mid.MID, source peer.ID) {
	if m.IsZero() {
		return
	}
	e.recordAttempt(m)

	has, err := e.cfg.Store.Has(m)
	if err == nil && has {
		// Already have the bytes; sealing is idempotent and is what
		// makes discovery stop reporting this MID as new.
		e.finishAcquired(ctx, m)
		return
	}

	provs, err := e.resolver.Resolve(ctx, m)
	if err != nil {
		return
	}
	provs = e.mergeSource(provs, source)
	if len(provs) == 0 {
		return
	}
	if err := e.cfg.Fetcher.Fetch(ctx, m, provs); err != nil {
		e.logger.Errorf("anchor: fetch %s: %v", m.String()[:12], err)
		return
	}
	e.finishAcquired(ctx, m)
}

// finishAcquired seals a now-held MID, bumps the synced counter, and
// clears its attempt record so it is never revisited.
func (e *AnchorEngine) finishAcquired(ctx context.Context, m mid.MID) {
	e.sealFetched(ctx, m)
	val := atomic.AddInt64(&e.synced, 1)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(val))
	_ = e.cfg.Store.PutMeta("anchor_synced", buf)
	e.markDone(m)
}

// mergeSource adds the announcing peer to the provider set if its
// address is known, so the engine can fetch directly from the node
// it learned the MID from even before DHT provider records exist.
func (e *AnchorEngine) mergeSource(provs []peer.AddrInfo, source peer.ID) []peer.AddrInfo {
	if source == "" || e.cfg.Host == nil {
		return provs
	}
	for _, p := range provs {
		if p.ID == source {
			return provs
		}
	}
	addrs := e.cfg.Host.Peerstore().Addrs(source)
	return append(provs, peer.AddrInfo{ID: source, Addrs: addrs})
}

func (e *AnchorEngine) shouldFetch(m mid.MID) bool {
	if m.IsZero() {
		return false
	}
	has, err := e.cfg.Store.Has(m)
	if err != nil {
		return true
	}
	return !has
}

// recordAttempt stamps the current time against m so redundant
// re-enqueues within anchorAttemptBackoff are suppressed.
func (e *AnchorEngine) recordAttempt(m mid.MID) {
	e.mu.Lock()
	e.attempts[m.String()] = time.Now()
	e.mu.Unlock()
}

// markDone drops m from the attempt map once it is sealed, so it is
// never re-enqueued or re-logged.
func (e *AnchorEngine) markDone(m mid.MID) {
	e.mu.Lock()
	delete(e.attempts, m.String())
	e.mu.Unlock()
}

// purgeStaleAttempts removes attempt records older than 2 * anchorAttemptBackoff
// to prevent unbounded map growth on long-running nodes.
func (e *AnchorEngine) purgeStaleAttempts(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cutoff := 2 * anchorAttemptBackoff
	for k, ts := range e.attempts {
		if now.Sub(ts) > cutoff {
			delete(e.attempts, k)
		}
	}
}

func (e *AnchorEngine) persistRegistry() {
	e.mu.Lock()
	anchors := make([]*membusspb.PeerAddrInfoProto, 0, len(e.anchors))
	for _, ai := range e.anchors {
		addrs := make([]string, 0, len(ai.Addrs))
		for _, a := range ai.Addrs {
			addrs = append(addrs, a.String())
		}
		anchors = append(anchors, &membusspb.PeerAddrInfoProto{
			Id:    ai.ID.String(),
			Addrs: addrs,
		})
	}
	e.mu.Unlock()

	reg := &membusspb.AnchorRegistryProto{Anchors: anchors}
	payload, err := proto.Marshal(reg)
	if err != nil {
		return
	}
	_ = e.cfg.Store.PutMeta(AnchorRegistryKey, payload)
}

func (e *AnchorEngine) loadRegistry() {
	raw, err := e.cfg.Store.GetMeta(AnchorRegistryKey)
	if err != nil || len(raw) == 0 {
		return
	}

	var reg membusspb.AnchorRegistryProto
	if err := proto.Unmarshal(raw, &reg); err == nil && len(reg.Anchors) > 0 {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, pb := range reg.Anchors {
			pid, err := peer.Decode(pb.Id)
			if err != nil || pid == "" {
				continue
			}
			addrs := make([]multiaddr.Multiaddr, 0, len(pb.Addrs))
			for _, s := range pb.Addrs {
				if a, err := multiaddr.NewMultiaddr(s); err == nil {
					addrs = append(addrs, a)
				}
			}
			ai := peer.AddrInfo{ID: pid, Addrs: addrs}
			e.anchors[pid] = ai
			e.cfg.Host.Peerstore().AddAddrs(pid, addrs, peerstore.PermanentAddrTTL)
		}
		return
	}

	// Legacy JSON fallback
	var anchors []peer.AddrInfo
	if err := json.Unmarshal(raw, &anchors); err == nil {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, ai := range anchors {
			if ai.ID == "" {
				continue
			}
			e.anchors[ai.ID] = ai
			e.cfg.Host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.PermanentAddrTTL)
		}
	}
}

// FindProvidersWithAnchors wraps dht.FindProviders and pads
// the result with known anchor peers when fewer than `want`
// providers are returned. Direct providers keep their
// ordering; anchors are appended in registry order.
func FindProvidersWithAnchors(ctx context.Context, d *dht.MemDHT, m mid.MID, anchors []peer.AddrInfo, want int) ([]peer.AddrInfo, error) {
	provs, err := d.FindProviders(ctx, m)
	if err != nil {
		return nil, err
	}
	provs = mergeAnchors(provs, anchors)
	if want > 0 && len(provs) > want {
		provs = provs[:want]
	}
	return provs, nil
}

func mergeAnchors(direct, anchors []peer.AddrInfo) []peer.AddrInfo {
	if len(anchors) == 0 {
		return direct
	}
	seen := make(map[peer.ID]struct{}, len(direct))
	for _, p := range direct {
		seen[p.ID] = struct{}{}
	}
	for _, a := range anchors {
		if _, ok := seen[a.ID]; ok {
			continue
		}
		seen[a.ID] = struct{}{}
		direct = append(direct, a)
	}
	return direct
}

func encodeAddrInfo(ai peer.AddrInfo) ([]byte, error) {
	addrs := make([]string, 0, len(ai.Addrs))
	for _, a := range ai.Addrs {
		addrs = append(addrs, a.String())
	}
	pb := &membusspb.PeerAddrInfoProto{
		Id:    ai.ID.String(),
		Addrs: addrs,
	}
	return proto.Marshal(pb)
}

// DecodeAnchorValue parses a value previously written by
// encodeAddrInfo. It is exported so other packages can
// reuse the same wire format.
func DecodeAnchorValue(raw []byte) (peer.AddrInfo, error) {
	var pb membusspb.PeerAddrInfoProto
	if err := proto.Unmarshal(raw, &pb); err == nil && pb.Id != "" {
		id, err := peer.Decode(pb.Id)
		if err != nil {
			return peer.AddrInfo{}, fmt.Errorf("anchor: bad peer id: %w", err)
		}
		addrs := make([]multiaddr.Multiaddr, 0, len(pb.Addrs))
		for _, s := range pb.Addrs {
			a, err := multiaddr.NewMultiaddr(s)
			if err != nil {
				continue
			}
			addrs = append(addrs, a)
		}
		return peer.AddrInfo{ID: id, Addrs: addrs}, nil
	}

	// Legacy JSON fallback
	type wire struct {
		ID    string   `json:"id"`
		Addrs []string `json:"addrs"`
	}
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return peer.AddrInfo{}, err
	}
	id, err := peer.Decode(w.ID)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("anchor: bad peer id: %w", err)
	}
	addrs := make([]multiaddr.Multiaddr, 0, len(w.Addrs))
	for _, s := range w.Addrs {
		a, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			continue
		}
		addrs = append(addrs, a)
	}
	return peer.AddrInfo{ID: id, Addrs: addrs}, nil
}
