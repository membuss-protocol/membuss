// Package herald implements Mem-Herald, the Membuss
// reprovisioner.
//
// Mem-Herald keeps content discoverable. Every ReprovideInterval
// (default 12h) it walks the local store and re-announces a
// subset of its MIDs to the DHT as provider records, so the
// network can find the data even if no peer has asked for it
// recently.
//
// Three strategies are supported:
//
//   - roots  (default): the sealed root MIDs plus the MemFS
//     entry nodes reachable from them (directories and file
//     envelopes). Raw content leaves and DAGPB intermediates
//     are NOT announced — a peer that locates any entry node
//     fetches its children from that provider directly (Memex
//     walks the DAG over the existing stream), so per-leaf
//     provider records are redundant. This keeps every
//     addressable object discoverable while announcing O(files)
//     records instead of O(blocks). Cheapest, and the right
//     default for most nodes.
//   - all: every block MID in the store. Used by Anchor
//     nodes that back up the whole network.
//   - shards: only erasure shard MIDs this node is responsible
//     for. The most selective; requires a shard ring.
//
// Provides are rate-limited to 100/minute (≈ 1.67/second) so
// the DHT is not flooded at startup. A leaky-bucket style
// limiter is used so bursts are tolerated up to a small bucket
// size.
package herald

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/keyring"
	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/shard"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/net/dht"
	"github.com/nnlgsakib/membuss/obs/metrics"
)

// Strategy selects which MIDs the herald re-announces.
type Strategy string

const (
	// StrategyRoots announces the sealed root MIDs plus the
	// MemFS entry nodes (directories and file envelopes)
	// reachable from them — everything a peer needs to *find*
	// content in the DHT. Raw content leaves and DAGPB
	// intermediates are not announced; Memex pulls those from
	// a located provider by walking the DAG. Default.
	StrategyRoots Strategy = "roots"
	// StrategyAll announces every block MID in the store.
	// Used by Anchor nodes.
	StrategyAll Strategy = "all"
	// StrategyShards announces only the erasure shard MIDs
	// this node is responsible for. The node is expected to
	// have a configured shard ring; see core/shard.
	StrategyShards Strategy = "shards"

	// DefaultRate is the long-run rate of provider
	// announcements: 100 per minute.
	DefaultRate = 100.0 / 60.0
	// DefaultBurst is the maximum burst the rate limiter
	// allows before throttling kicks in.
	DefaultBurst = 32
)

// SealedLister is the subset of the store that the herald
// needs. Production code passes *store.MemStore; tests can
// supply an in-memory fake.
type SealedLister interface {
	// AllSealed returns every directly sealed root MID.
	AllSealed() ([]mid.MID, error)
	// AllBlocks returns every block MID the store holds.
	// Required by the "all" strategy. For stores that
	// cannot enumerate blocks cheaply, return AllSealed
	// instead and the "all" strategy will degrade to
	// "roots".
	AllBlocks() ([]mid.MID, error)
	// Get returns the block payload for the given MID.
	Get(mid.MID) ([]byte, error)
	// IterateBlocks invokes fn for every block/DAG MID.
	IterateBlocks(fn func(mid.MID) error) error
	// IterateSealed invokes fn for every sealed root MID.
	IterateSealed(fn func(mid.MID) error) error
}

// Provider announces that this node is a provider of m. The
// DHT facade in net/dht satisfies this interface.
type Provider interface {
	Provide(ctx context.Context, m mid.MID) error
}

// Config configures a MemHerald.
type Config struct {
	// Store is the local store to enumerate MIDs from.
	// Required.
	Store SealedLister
	// DHT is the local DHT facade. Required.
	DHT Provider
	// Strategy selects which MIDs to re-announce. The
	// default is StrategyRoots.
	Strategy Strategy
	// Interval is the time between reprovide rounds. The
	// default is 12 hours.
	Interval time.Duration
	// Rate is the long-run rate of provider announcements
	// in messages/second. Default is DefaultRate
	// (100/minute).
	Rate float64
	// Burst is the maximum burst the limiter allows.
	// Default is DefaultBurst.
	Burst int
	// Now overrides the wall clock for tests. Default
	// is time.Now.
	Now func() time.Time

	// Phase 18: MemNS record re-publishing fields
	KeyRing *keyring.KeyRing
	MemDHT  *dht.MemDHT

	// ShardRing is the rendezvous hash ring used by the
	// shards strategy. When nil, StrategyShards falls back
	// to StrategyRoots (all sealed MIDs).
	ShardRing *shard.HashRing
	// PeerID is this node's peer ID, used to check shard
	// ownership. Required when Strategy is StrategyShards.
	PeerID string
	// Replicas is the number of replicas per MID for shard
	// assignment. Default is 3.
	Replicas int

	// Metrics is the live instrumentation handle. Optional.
	Metrics *metrics.Metrics

	// ReprovideGroups controls the number of incremental reprovide groups.
	// When > 1, the reprovide cycle is split into N runs. In each run,
	// only 1/N of the total keys are announced. Default is 1 (disabled).
	ReprovideGroups int
}

// MemHerald is the long-lived reprovisioner.
type MemHerald struct {
	cfg Config
	lim *tokenBucket
	nsLim *tokenBucket // dedicated rate limiter for MemNS republishing

	mu         sync.Mutex
	lastRun    time.Time
	lastCount  int
	cycleCount int // tracks the current incremental cycle index

	// triggerCh is a non-blocking signal channel. Sending
	// on it causes the loop to run an immediate reprovide
	// pass. Used by the mDNS peer-found callback so newly
	// discovered peers get announced content quickly.
	triggerCh chan struct{}
}

// New returns a MemHerald ready to be started. Call Start to
// begin the background loop.
func New(cfg Config) (*MemHerald, error) {
	if cfg.Store == nil {
		return nil, errors.New("herald: nil store")
	}
	if cfg.DHT == nil {
		return nil, errors.New("herald: nil dht")
	}
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyRoots
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 12 * time.Hour
	}
	if cfg.Rate <= 0 {
		cfg.Rate = DefaultRate
	}
	if cfg.Burst <= 0 {
		cfg.Burst = DefaultBurst
	}
	if cfg.Replicas <= 0 {
		cfg.Replicas = 3
	}
	if cfg.ReprovideGroups <= 0 {
		cfg.ReprovideGroups = 1
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &MemHerald{
		cfg:       cfg,
		lim:       newTokenBucket(cfg.Rate, cfg.Burst, cfg.Now),
		nsLim:     newTokenBucket(1.0, 5, cfg.Now), // 1 republish/sec rate-limit, burst 5
		triggerCh: make(chan struct{}, 1),
	}, nil
}

// Start launches the background reprovide loop. It returns
// immediately; the loop runs until ctx is cancelled. A first
// pass is also fired immediately so the node announces its
// content right at startup.
func (h *MemHerald) Start(ctx context.Context) {
	go h.loop(ctx)
	if h.cfg.MemDHT == nil {
		h.RunOnce(ctx)
		return
	}
	go func() {
		// Wait until at least 1 peer connects, then announce sealed roots immediately
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()

		announcedOnFirstPeer := false
		hadPeers := false

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				hasPeers := h.cfg.MemDHT != nil && h.cfg.MemDHT.RoutingTableSize() > 0
				if hasPeers {
					if !announcedOnFirstPeer {
						announcedOnFirstPeer = true
						hadPeers = true
						go h.RunOnce(ctx)
					} else if !hadPeers {
						hadPeers = true
						h.Trigger()
					}
				} else {
					hadPeers = false
				}
			}
		}
	}()
	go h.RunOnce(ctx)
}

// Stop is a no-op kept for symmetry with other long-lived
// engines; the loop terminates when ctx is cancelled.
func (h *MemHerald) Stop() {}

// Trigger sends a non-blocking signal to the background loop
// to run an immediate reprovide pass. It is safe to call from
// any goroutine and does not block even if a pass is already
// in progress.
func (h *MemHerald) Trigger() {
	select {
	case h.triggerCh <- struct{}{}:
	default:
	}
}

// RunOnce performs a single reprovide pass synchronously and
// returns the number of MIDs announced.
func (h *MemHerald) RunOnce(ctx context.Context) int {
	h.mu.Lock()
	h.cycleCount++
	h.mu.Unlock()

	announced := 0
	failed := 0
	seen := make(map[string]struct{})

	announceFn := func(m mid.MID) error {
		if m.IsZero() {
			return nil
		}
		mStr := m.String()
		if _, ok := seen[mStr]; ok {
			return nil
		}
		seen[mStr] = struct{}{}

		// Rate limit check
		if err := h.lim.Wait(ctx); err != nil {
			return err
		}

		// Provide/Announce with up to 3 retries on transient errors
		var provideErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			provideErr = h.cfg.DHT.Provide(ctx, m)
			if provideErr == nil {
				break
			}
			
			if errors.Is(provideErr, context.Canceled) || errors.Is(provideErr, context.DeadlineExceeded) {
				break
			}
			
			if h.cfg.Metrics != nil {
				h.cfg.Metrics.IncDHTProvideFailed()
			}
			
			log.Printf("herald: Provide attempt %d failed for MID %s: %v. Retrying...", attempt, m, provideErr)
			
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 50 * time.Millisecond):
			}
		}

		if provideErr != nil {
			failed++
			log.Printf("herald: failed to provide MID %s after 3 attempts: %v", m, provideErr)
			return nil // continue walking/collecting
		}
		
		announced++
		if h.cfg.Metrics != nil {
			h.cfg.Metrics.IncDHTProvide()
		}
		
		// Progress tracking log every 100 successful announcements
		if announced%100 == 0 {
			log.Printf("herald progress: successfully provided %d MIDs (failed: %d) in this round", announced, failed)
		}

		return nil
	}

	err := h.collectStream(ctx, announceFn)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("herald: reprovide run error: %v", err)
	}

	h.mu.Lock()
	h.lastRun = h.cfg.Now()
	h.lastCount = announced
	h.mu.Unlock()

	// Phase 18: MemNS record re-publishing
	if h.cfg.KeyRing != nil && h.cfg.MemDHT != nil {
		keys, err := h.cfg.KeyRing.List()
		if err == nil {
			republished := 0
			for _, kInfo := range keys {
				key, err := h.cfg.KeyRing.Get(kInfo.Name)
				if err != nil {
					log.Printf("herald: failed to get key %s: %v", kInfo.Name, err)
					continue
				}
				rec, err := h.cfg.KeyRing.LoadRecord(kInfo.Name)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						// It is normal for a key to exist in the keyring without a published record
						continue
					}
					log.Printf("herald: failed to load record for key %s: %v", kInfo.Name, err)
					continue
				}

				// Update validity and re-sign record to keep it alive
				ttl := 24 * time.Hour
				if rec.Ttl > 0 {
					ttl = time.Duration(rec.Ttl)
				}
				rec.Validity = h.cfg.Now().Add(ttl).UnixNano()

				canonical := memns.CanonicalBytes(rec)
				sig, err := key.PrivKey.Sign(canonical)
				if err != nil {
					log.Printf("herald: failed to re-sign record for key %s: %v", kInfo.Name, err)
					continue
				}
				rec.Signature = sig

				// Save updated record back to disk
				if err := h.cfg.KeyRing.SaveRecord(kInfo.Name, rec); err != nil {
					log.Printf("herald: failed to save updated record for key %s: %v", kInfo.Name, err)
				}

				// Rate limit check before publishing record to DHT
				if err := h.nsLim.Wait(ctx); err != nil {
					log.Printf("herald: MemNS rate-limiter wait error: %v", err)
					break
				}

				if err := memns.PublishDHT(ctx, h.cfg.MemDHT, key, rec); err != nil {
					log.Printf("herald: failed to publish record for key %s to DHT: %v", kInfo.Name, err)
				} else {
					republished++
				}
			}
			if republished > 0 {
				log.Printf("herald: re-published %d MemNS records", republished)
			}
		} else {
			log.Printf("herald: failed to list keys in keyring: %v", err)
		}
	}

	return announced
}

// LastRun returns the time of the most recent completed
// reprovide pass. The zero value means RunOnce has not yet
// completed.
func (h *MemHerald) LastRun() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastRun
}

// LastCount returns the number of MIDs announced in the most
// recent reprovide pass.
func (h *MemHerald) LastCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastCount
}

// Strategy returns the configured strategy.
func (h *MemHerald) Strategy() Strategy { return h.cfg.Strategy }

func (h *MemHerald) loop(ctx context.Context) {
	interval := h.cfg.Interval
	if h.cfg.ReprovideGroups > 1 {
		interval = h.cfg.Interval / time.Duration(h.cfg.ReprovideGroups)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = h.RunOnce(ctx)
		case <-h.triggerCh:
			_ = h.RunOnce(ctx)
		}
	}
}

func (h *MemHerald) collect(ctx context.Context) []mid.MID {
	var collected []mid.MID
	_ = h.collectStream(ctx, func(m mid.MID) error {
		collected = append(collected, m)
		return nil
	})
	return collected
}

func (h *MemHerald) collectStream(ctx context.Context, fn func(mid.MID) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	h.mu.Lock()
	cycle := h.cycleCount
	h.mu.Unlock()

	filterFn := func(m mid.MID) error {
		if h.cfg.ReprovideGroups > 1 {
			hVal := deterministicHash(m)
			if hVal%uint32(h.cfg.ReprovideGroups) != uint32(cycle%h.cfg.ReprovideGroups) {
return nil // skip this MID in this cycle
			}
		}
		return fn(m)
	}

	switch h.cfg.Strategy {
	case StrategyAll:
		err := h.cfg.Store.IterateBlocks(func(m mid.MID) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return filterFn(m)
		})
		return err

	case StrategyShards:
		err := h.cfg.Store.IterateSealed(func(m mid.MID) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if h.cfg.ShardRing == nil || h.cfg.PeerID == "" {
				return filterFn(m)
			}
			if m.IsZero() {
				return nil
			}
			peers, err := h.cfg.ShardRing.Assign(m, h.cfg.Replicas)
			if err != nil {
				return nil
			}
			for _, p := range peers {
				if p == h.cfg.PeerID {
					if err := filterFn(m); err != nil {
						return err
					}
					break
				}
			}
			return nil
		})
		return err

	case StrategyRoots, "":
		// Announce the entry points a peer needs to *find*
		// content in the DHT: the sealed roots, plus the MemFS
		// nodes reachable from them (directories and file
		// envelopes). Raw content leaves (codec 0x55) and
		// DAGPB intermediates (codec 0x70) are deliberately
		// NOT announced.
		//
		// This is safe because Memex fetches a DAG by locating
		// a provider of an entry node and then walking the tree
		// from that same provider (see net/memex_v2 session
		// enqueueChildren): child blocks are pulled over the
		// existing stream, not rediscovered through the DHT. So
		// one provider record per entry node is sufficient for
		// full retrieval, while every inner file of a directory
		// stays independently discoverable by its bare MID.
		//
		// Announcing every leaf instead would cost one full
		// Kademlia lookup per 256 KiB block — overhead that
		// scales with total bytes stored, not with the number
		// of addressable objects. Anchor nodes that intentionally
		// back up the whole network use StrategyAll for that.
		seen := make(map[string]struct{})
		err := h.cfg.Store.IterateSealed(func(r mid.MID) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if r.IsZero() {
				return nil
			}
			// Announce the root itself (any codec: a bare file
			// root, a directory, or a legacy DAGPB root).
			if _, ok := seen[r.String()]; !ok {
				seen[r.String()] = struct{}{}
				if err := filterFn(r); err != nil {
					return err
				}
			}

			// Walk the DAG but announce only MemFS entry nodes.
			walkErr := store.WalkOptions(h.cfg.Store, r, true, func(m mid.MID, _ bool) error {
				if m.Equal(r) {
					return nil
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Only MemFS nodes (directories + file
				// envelopes) are addressable entry points.
				// Skip raw leaves and DAGPB intermediates.
				if m.Codec() != mid.CodecMemFS {
					return nil
				}
				mStr := m.String()
				if _, ok := seen[mStr]; ok {
					return nil
				}
				seen[mStr] = struct{}{}
				return filterFn(m)
			})
			if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
				if !errors.Is(walkErr, store.ErrNotFound) && !strings.Contains(walkErr.Error(), "not found") && !strings.Contains(walkErr.Error(), "closed") {
					log.Printf("herald: walk error for root %s: %v", r, walkErr)
				}
			}
			return nil
		})
		return err

	default:
		return fmt.Errorf("herald: unknown strategy %q", h.cfg.Strategy)
	}
}

// tokenBucket is a simple rate limiter with a fixed capacity
// and refill rate. It is safe for concurrent use.
type tokenBucket struct {
	mu       sync.Mutex
	rate     float64 // tokens/second
	burst    float64
	tokens   float64
	lastFill time.Time
	now      func() time.Time
}

func newTokenBucket(rate float64, burst int, now func() time.Time) *tokenBucket {
	return &tokenBucket{
		rate:     rate,
		burst:    float64(burst),
		tokens:   float64(burst),
		lastFill: now(),
		now:      now,
	}
}

// Wait blocks until one token is available or ctx is done.
// It returns ctx.Err() if the context fires first.
func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		b.refillLocked()
		if b.tokens >= 1.0 {
			b.tokens -= 1.0
			b.mu.Unlock()
			return nil
		}
		// Compute the time until the next token.
		need := 1.0 - b.tokens
		wait := time.Duration(float64(time.Second) * need / b.rate)
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (b *tokenBucket) refillLocked() {
	now := b.now()
	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.lastFill = now
}

func deterministicHash(m mid.MID) uint32 {
	h := fnv.New32a()
	_, _ = h.Write(m.Bytes())
	return h.Sum32()
}
