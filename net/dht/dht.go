// Package dht wraps go-libp2p-kad-dht into a Membuss-shaped API.
//
// Membuss uses the DHT to announce provider records ("I have
// this MID") and to discover providers of a given MID. Small
// arbitrary values can also be stored and retrieved. The
// underlying Kademlia protocol is identified by the prefix
// /membuss/dht/1.0.0 (the libp2p kad-dht library appends
// /kad/1.0.0 automatically).
package dht

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	dhtrecords "github.com/libp2p/go-libp2p-kad-dht/records"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/net/conngater"
	"github.com/multiformats/go-multihash"

	"github.com/nnlgsakib/membuss/core/mid"
)

// ProtocolPrefix is the application-specific protocol prefix
// for the Membuss DHT. The kad-dht library appends /kad/1.0.0
// to it for the actual protocol ID.
const ProtocolPrefix = "/membuss/dht/1.0.0"

// DefaultBootstrapTimeout is the maximum time a Bootstrap call
// will wait for connections to bootstrap peers.
const DefaultBootstrapTimeout = 30 * time.Second

// MemDHT is the Membuss DHT facade. It is safe for concurrent
// use after construction.
type MemDHT struct {
	dht        *kaddht.IpfsDHT
	dstore     ds.Batching
	bwc        *metrics.BandwidthCounter
	freshStore *freshnessProviderStore
}

// Config configures a MemDHT.
type Config struct {
	Host           host.Host
	BootstrapPeers []peer.AddrInfo
	// Mode overrides the kad-dht operating mode. The
	// default is kaddht.ModeAuto, which lets kad-dht pick
	// Client vs. Server based on reachability. Tests can
	// pass kaddht.ModeServer to force a server role.
	Mode kaddht.ModeOpt
	// ModeName is the YAML-friendly version of Mode.
	// Allowed values: "auto" (default), "client",
	// "server", "auto-server". When set it overrides
	// the typed Mode field, so config.yaml can drive
	// the choice without forcing every caller to build
	// a kaddht.ModeOpt.
	ModeName string
	// Datastore is the on-disk store used by kad-dht to
	// persist provider records across Provide/Restart
	// cycles. When nil, kad-dht falls back to a private
	// in-memory store, which means FindProviders can
	// only see providers the local node has already
	// observed during this run. The Membuss daemon
	// always passes a MapDatastore-backed ds.Batching
	// here so the DHT propagates provider records
	// across a multi-node cluster the way IPFS does.
	Datastore ds.Batching
	// OptimisticProvide, when true, enables
	// kaddht.EnableOptimisticProvide. The optimisation
	// short-circuits the last few hops of the provide
	// walk: as soon as the local node has announced the
	// CID to its K closest peers, the Provide call
	// returns success. Cross-cluster propagation is
	// dramatically faster and is what IPFS ships with
	// by default. Default true.
	OptimisticProvide bool

	// ProviderRecordTTL is the duration provider records remain
	// valid in the DHT.
	ProviderRecordTTL time.Duration
	// ProviderAddrTTL is the TTL for provider address records.
	ProviderAddrTTL time.Duration
	// ProviderCleanupInterval is the sweep interval for pruning
	// expired provider records from local storage.
	ProviderCleanupInterval time.Duration
	// ConnectionGater is used to filter out blacklisted peers from the
	// routing table and queries.
	ConnectionGater *conngater.BasicConnectionGater
	// BandwidthCounter tracks data transferred by remote peers.
	BandwidthCounter *metrics.BandwidthCounter
}

// modeOrDefault resolves cfg.Mode vs cfg.ModeName to a
// concrete kaddht.ModeOpt. ModeName wins so config.yaml
// can drive the choice. Allowed values are "auto",
// "client", "server" and "auto-server". An empty
// ModeName plus a zero Mode falls back to ModeAuto.
func (c Config) modeOrDefault() kaddht.ModeOpt {
	switch strings.ToLower(strings.TrimSpace(c.ModeName)) {
	case "client":
		return kaddht.ModeClient
	case "server":
		return kaddht.ModeServer
	case "auto-server", "autoserver":
		return kaddht.ModeAutoServer
	case "auto", "":
		// fall through to the typed Mode below
	default:
		// unknown string: ignore and fall back
	}
	if c.Mode == 0 {
		return kaddht.ModeAuto
	}
	return c.Mode
}

// New constructs a MemDHT. The DHT is not yet connected to any
// peer; call Bootstrap to connect to the configured bootstrap
// set.
//
// Phase 17: New honours Config.ModeName (the YAML-friendly
// form), Config.Datastore (a ds.Batching the kad-dht
// ProviderManager persists into) and Config.OptimisticProvide
// (turns on the last-hop skip so cross-node provider records
// propagate like IPFS).
func New(ctx context.Context, cfg Config) (*MemDHT, error) {
	if cfg.Host == nil {
		return nil, errors.New("dht: nil host")
	}
	opts := []kaddht.Option{
		kaddht.ProtocolPrefix(protocol.ID(ProtocolPrefix)),
		kaddht.Mode(cfg.modeOrDefault()),
		// Register a validator for the "membuss" and "memns"
		// namespaces so that app-level values and MemNS records can be
		// securely stored, validated, and selected. The kad-dht default
		// validator only allows "/pk/..." (public-key) records.
		kaddht.NamespacedValidator("membuss", membussValidator{}),
		kaddht.NamespacedValidator("memns", membussValidator{}),
	}

	var pmOpts []dhtrecords.Option
	if cfg.ProviderRecordTTL > 0 {
		pmOpts = append(pmOpts, dhtrecords.ProvideValidity(cfg.ProviderRecordTTL))
	}
	if cfg.ProviderAddrTTL > 0 {
		pmOpts = append(pmOpts, dhtrecords.ProviderAddrTTL(cfg.ProviderAddrTTL))
	}
	if cfg.ProviderCleanupInterval > 0 {
		pmOpts = append(pmOpts, dhtrecords.CleanupInterval(cfg.ProviderCleanupInterval))
	}

	dstore := cfg.Datastore
	if dstore == nil {
		dstore = ds.NewMapDatastore()
	}
	pm, err := dhtrecords.NewProviderManager(ctx, cfg.Host.ID(), cfg.Host.Peerstore(), dstore, pmOpts...)
	if err != nil {
		return nil, fmt.Errorf("dht: build provider manager: %w", err)
	}
	freshStore := &freshnessProviderStore{
		ProviderStore: pm,
		fresh:         make(map[string]time.Time),
	}
	opts = append(opts, kaddht.ProviderStore(freshStore))

	if cfg.Datastore != nil {
		// Provider-record persistence. Without this, the
		// DHT forgets every Provide() the moment the
		// Provide call returns, so FindProviders on
		// other nodes always returns an empty list on
		// a freshly-bootstrapped cluster.
		opts = append(opts, kaddht.Datastore(cfg.Datastore))
	}
	if cfg.OptimisticProvide {
		// IPFS default: skip the last hops of the
		// provide walk. Cuts the time before another
		// node can discover our content from minutes
		// (full DHT walk) to seconds (single hop).
		opts = append(opts, kaddht.EnableOptimisticProvide())
	}
	if cfg.ConnectionGater != nil {
		var rtFilter kaddht.RouteTableFilterFunc = func(dht any, p peer.ID) bool {
			if !cfg.ConnectionGater.InterceptPeerDial(p) {
				return false
			}
			return true
		}
		var qFilter kaddht.QueryFilterFunc = func(dht any, ai peer.AddrInfo) bool {
			if !cfg.ConnectionGater.InterceptPeerDial(ai.ID) {
				return false
			}
			return true
		}
		opts = append(opts, kaddht.RoutingTableFilter(rtFilter))
		opts = append(opts, kaddht.QueryFilter(qFilter))
	}
	d, err := kaddht.New(ctx, cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("dht: build kad-dht: %w", err)
	}
	return &MemDHT{dht: d, dstore: cfg.Datastore, bwc: cfg.BandwidthCounter, freshStore: freshStore}, nil
}

// Provide announces to the DHT that this node can serve the
// given MID.
func (m *MemDHT) Provide(ctx context.Context, id mid.MID) error {
	if m == nil || m.dht == nil {
		return errors.New("dht: nil")
	}
	if id.IsZero() {
		return errors.New("dht: zero MID")
	}
	if id.Codec() == mid.CodecRaw {
		return nil // raw blocks are not announced individually
	}
	c := midToCID(id)
	if !c.Defined() {
		return errors.New("dht: zero MID")
	}
	return m.dht.Provide(ctx, c, true)
}

// FindProviders returns the set of peers the DHT knows are
// providers of the given MID, ranked by their peer score.
func (m *MemDHT) FindProviders(ctx context.Context, id mid.MID) ([]peer.AddrInfo, error) {
	if m == nil || m.dht == nil {
		return nil, errors.New("dht: nil")
	}
	if id.IsZero() {
		return nil, errors.New("dht: zero MID")
	}
	if id.Codec() == mid.CodecRaw {
		return nil, nil // raw blocks are never announced on DHT, bypass timeout
	}
	c := midToCID(id)
	if !c.Defined() {
		return nil, errors.New("dht: zero MID")
	}
	providers, err := m.dht.FindProviders(ctx, c)
	if err != nil {
		return nil, err
	}

	// Sort providers by score descending
	key := c.Hash()
	sort.Slice(providers, func(i, j int) bool {
		return m.scorePeer(key, providers[i].ID) > m.scorePeer(key, providers[j].ID)
	})

	return providers, nil
}

func (m *MemDHT) scorePeer(key []byte, p peer.ID) float64 {
	score := 0.0

	// 1. Latency-based scoring
	latency := m.dht.Host().Peerstore().LatencyEWMA(p)
	if latency > 0 {
		// Lower latency -> higher score
		score += 1000.0 / (float64(latency/time.Millisecond) + 1.0)
	} else {
		// Default score for unknown/not-measured latency (e.g. assume 200ms)
		score += 1000.0 / 201.0
	}

	// 2. Bandwidth-based scoring
	if m.bwc != nil {
		stats := m.bwc.GetBandwidthForPeer(p)
		// 1 point per KB/s rate
		score += stats.RateIn / 1024.0
		// 1 point per MB total transferred in
		score += float64(stats.TotalIn) / (1024.0 * 1024.0)
	}

	// 3. Freshness-based scoring
	if m.freshStore != nil {
		lastSeen := m.freshStore.getFreshness(key, p)
		if !lastSeen.IsZero() {
			age := time.Since(lastSeen)
			// Higher score for younger/fresher records (e.g. up to 500 points)
			score += 500.0 / (age.Hours() + 1.0)
		}
	}

	return score
}

// PutValue stores an arbitrary small value under the given
// key. The key must be in the form "/<namespace>/<path>".
// Membuss reserves the "membuss" namespace and registers a
// permissive validator for it.
func (m *MemDHT) PutValue(ctx context.Context, key string, value []byte) error {
	if m == nil || m.dht == nil {
		return errors.New("dht: nil")
	}
	if key == "" {
		return errors.New("dht: empty key")
	}
	if len(value) == 0 {
		return errors.New("dht: empty value")
	}
	return m.dht.PutValue(ctx, key, value)
}

// GetValue retrieves a value previously stored under key.
func (m *MemDHT) GetValue(ctx context.Context, key string) ([]byte, error) {
	if m == nil || m.dht == nil {
		return nil, errors.New("dht: nil")
	}
	if key == "" {
		return nil, errors.New("dht: empty key")
	}
	return m.dht.GetValue(ctx, key)
}

// SearchValue retrieves multiple values previously stored under key.
func (m *MemDHT) SearchValue(ctx context.Context, key string) (<-chan []byte, error) {
	if m == nil || m.dht == nil {
		return nil, errors.New("dht: nil")
	}
	if key == "" {
		return nil, errors.New("dht: empty key")
	}
	return m.dht.SearchValue(ctx, key)
}

// Bootstrap connects to the configured bootstrap peers in parallel and
// refreshes the routing table.
func (m *MemDHT) Bootstrap(ctx context.Context, peers []peer.AddrInfo) error {
	if m == nil || m.dht == nil {
		return errors.New("dht: nil")
	}
	if err := m.dht.Bootstrap(ctx); err != nil {
		return fmt.Errorf("dht: bootstrap: %w", err)
	}

	peers = dedupeAddrInfo(peers)
	if len(peers) == 0 {
		return nil
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures []error
		success  int
	)

	for _, p := range peers {
		wg.Add(1)
		go func(p peer.AddrInfo) {
			defer wg.Done()
			dialCtx, cancel := context.WithTimeout(ctx, DefaultBootstrapTimeout)
			defer cancel()
			if err := m.dht.Host().Connect(dialCtx, p); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Errorf("peer %s (%v): %w", p.ID, p.Addrs, err))
				mu.Unlock()
			} else {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(p)
	}

	wg.Wait()

	if success == 0 {
		if len(failures) > 0 {
			return fmt.Errorf("dht: all bootstrap peers unreachable: %w", errors.Join(failures...))
		}
		return errors.New("dht: all bootstrap peers unreachable")
	}

	return nil
}

// BootstrapConfig configures BootstrapWithBackoff. Zero values
// fall back to sane defaults.
type BootstrapConfig struct {
	// Initial is the first retry delay. Default 500ms.
	Initial time.Duration
	// Max caps a single backoff sleep. Default 60s.
	Max time.Duration
	// Factor multiplies the previous delay after each failure.
	// Default 2.0.
	Factor float64
	// MaxAttempts bounds the retries per peer. Zero = unlimited.
	MaxAttempts int
	// Logger, if non-nil, receives structured progress events.
	Logger *slog.Logger
}

// BootstrapWithBackoff attempts to connect to each bootstrap peer
// with an exponential backoff schedule. It is a best-effort loop:
// the first successful connect per peer terminates its retry, and
// the function returns the total number of successful connections
// plus the combined error of the last failure (if any). It is safe
// to call concurrently with Bootstrap.
//
// The loop is cancellable via ctx. On cancel it returns
// ctx.Err() alongside the success count.
func (m *MemDHT) BootstrapWithBackoff(ctx context.Context, peers []peer.AddrInfo, cfg BootstrapConfig) (int, error) {
	if m == nil || m.dht == nil {
		return 0, errors.New("dht: nil")
	}
	if cfg.Initial <= 0 {
		cfg.Initial = 500 * time.Millisecond
	}
	if cfg.Max <= 0 {
		cfg.Max = 60 * time.Second
	}
	if cfg.Factor < 1 {
		cfg.Factor = 2.0
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	peers = dedupeAddrInfo(peers)
	// Background the DHT's own bootstrap so our retry loop
	// is the only thing the caller waits on.
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()
	_ = m.dht.Bootstrap(bgCtx)

	h := m.dht.Host()
	hostCtx := func() context.Context { return bgCtx }

	var (
		mu        sync.Mutex
		failures  = make(map[peer.ID]error)
		successes int
		wg        sync.WaitGroup
	)

	for _, p := range peers {
		wg.Add(1)
		go func(p peer.AddrInfo) {
			defer wg.Done()
			delay := cfg.Initial
			for attempt := 1; ; attempt++ {
				if ctx.Err() != nil {
					return
				}
				connectCtx, cancel := context.WithTimeout(hostCtx(), DefaultBootstrapTimeout)
				err := h.Connect(connectCtx, p)
				cancel()
				if err == nil {
					mu.Lock()
					successes++
					delete(failures, p.ID)
					mu.Unlock()
					if cfg.Logger != nil {
						cfg.Logger.Info("dht bootstrap peer connected",
							"peer", p.ID.String(),
							"attempt", attempt,
						)
					}
					break
				}
				mu.Lock()
				failures[p.ID] = fmt.Errorf("peer %s (%v): %w", p.ID, p.Addrs, err)
				mu.Unlock()
				if cfg.Logger != nil {
					if attempt < 5 {
						cfg.Logger.Debug("dht bootstrap peer connect failed (retrying)",
							"peer", p.ID.String(),
							"attempt", attempt,
							"err", err.Error(),
						)
					} else if attempt == 5 || (attempt > 5 && attempt%5 == 0) {
						cfg.Logger.Warn("dht bootstrap peer connect failed",
							"peer", p.ID.String(),
							"attempt", attempt,
							"err", err.Error(),
						)
					}
				}
				if cfg.MaxAttempts > 0 && attempt >= cfg.MaxAttempts {
					break
				}
				// Add jitter (e.g. ±20%) to the backoff delay
				jitter := float64(delay) * 0.2
				minDelay := float64(delay) - jitter
				maxDelay := float64(delay) + jitter
				actualDelay := time.Duration(minDelay + rand.Float64()*(maxDelay-minDelay))

				timer := time.NewTimer(actualDelay)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					return
				case <-timer.C:
				}
				maxLimit := cfg.Max
				if attempt > 5 {
					maxLimit = 10 * time.Minute
					if maxLimit < cfg.Max {
						maxLimit = cfg.Max
					}
				}
				next := float64(delay) * cfg.Factor
				if next > float64(maxLimit) {
					delay = maxLimit
				} else {
					delay = time.Duration(next)
				}
			}
		}(p)
	}

	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if ctx.Err() != nil {
		return successes, ctx.Err()
	}
	joined := make([]error, 0, len(failures))
	for _, err := range failures {
		joined = append(joined, err)
	}
	return successes, errors.Join(joined...)
}

func dedupeAddrInfo(peers []peer.AddrInfo) []peer.AddrInfo {
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
	return out
}

// Close releases the DHT's resources.
func (m *MemDHT) Close() error {
	if m == nil || m.dht == nil {
		return nil
	}
	return m.dht.Close()
}

// Host returns the underlying libp2p host.
func (m *MemDHT) Host() host.Host {
	if m == nil || m.dht == nil {
		return nil
	}
	return m.dht.Host()
}

// RoutingTableSize returns the number of peers in the DHT's
// local routing table. Tests use this to wait for the table
// to fill before exercising Provide / PutValue.
func (m *MemDHT) RoutingTableSize() int {
	if m == nil || m.dht == nil {
		return 0
	}
	return m.dht.RoutingTable().Size()
}

type cidCacheKey struct {
	codec uint64
	hash  string
}

var (
	cidCache   = make(map[cidCacheKey]cid.Cid)
	cidCacheMu sync.RWMutex
)

func midToCID(m mid.MID) cid.Cid {
	if m.IsZero() {
		return cid.Cid{}
	}
	key := cidCacheKey{
		codec: m.Codec(),
		hash:  string(m.Hash),
	}

	cidCacheMu.RLock()
	c, ok := cidCache[key]
	cidCacheMu.RUnlock()
	if ok {
		return c
	}

	c = cid.NewCidV1(uint64(mid.CodecRaw), multihash.Multihash(m.Hash))

	cidCacheMu.Lock()
	if len(cidCache) > 10000 {
		cidCache = make(map[cidCacheKey]cid.Cid)
	}
	cidCache[key] = c
	cidCacheMu.Unlock()

	return c
}

func mhFromMID(m mid.MID) multihash.Multihash {
	return multihash.Multihash(m.Hash)
}

// RemoveProviderRecord deletes the local provider record for the given MID.
func (m *MemDHT) RemoveProviderRecord(id mid.MID) error {
	if m == nil || m.dht == nil {
		return errors.New("dht: nil")
	}
	if m.dstore == nil {
		return nil
	}
	if id.IsZero() {
		return errors.New("dht: zero MID")
	}
	c := midToCID(id)
	if !c.Defined() {
		return errors.New("dht: zero MID")
	}

	rawStd := base32.StdEncoding.WithPadding(base32.NoPadding)
	cidB32 := rawStd.EncodeToString(c.Hash())
	pidB32 := rawStd.EncodeToString([]byte(m.dht.PeerID()))

	key := ds.NewKey("/providers/" + cidB32 + "/" + pidB32)
	if err := m.dstore.Delete(context.Background(), key); err != nil {
		if errors.Is(err, ds.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("dht: delete provider record from datastore: %w", err)
	}
	return nil
}

type freshnessProviderStore struct {
	dhtrecords.ProviderStore
	mu    sync.RWMutex
	fresh map[string]time.Time
}

func (f *freshnessProviderStore) AddProvider(ctx context.Context, key []byte, prov peer.AddrInfo) error {
	rawStd := base32.StdEncoding.WithPadding(base32.NoPadding)
	kStr := rawStd.EncodeToString(key) + "/" + rawStd.EncodeToString([]byte(prov.ID))

	f.mu.Lock()
	f.fresh[kStr] = time.Now()
	f.mu.Unlock()

	return f.ProviderStore.AddProvider(ctx, key, prov)
}

func (f *freshnessProviderStore) getFreshness(key []byte, pid peer.ID) time.Time {
	rawStd := base32.StdEncoding.WithPadding(base32.NoPadding)
	kStr := rawStd.EncodeToString(key) + "/" + rawStd.EncodeToString([]byte(pid))

	f.mu.RLock()
	t, ok := f.fresh[kStr]
	f.mu.RUnlock()

	if ok {
		return t
	}
	return time.Time{}
}

// silence unused import
var _ = record.ErrInvalidRecordType
