// Backend is the production implementation of server.Backend
// used by cmd/membuss. It wires the gRPC service to the
// live subsystems: Mem-Store, Memex, the libp2p host, the
// DHT, PEX, the herald, and the anchor engine.
package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/anchor"
	"github.com/nnlgsakib/membuss/config"
	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/ingest"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/net/dht"
	"github.com/nnlgsakib/membuss/net/herald"
	memex "github.com/nnlgsakib/membuss/net/memex_v2"
	"github.com/nnlgsakib/membuss/net/pex"
	"github.com/nnlgsakib/membuss/obs/metrics"
	membusspb "github.com/nnlgsakib/membuss/proto"
	serverpkg "github.com/nnlgsakib/membuss/rpc/server"
)

// daemonBackend is the live, production implementation of
// server.Backend. All RPCs dispatch into the local subsystems.
type daemonBackend struct {
	dataDir string

	// host is the local libp2p host. It owns the DHT, PEX,
	// and Memex protocols.
	host host.Host

	// store is the local BadgerDB block store.
	store store.Store

	// dht, pex, memex are the local networking subsystems.
	dht   *dht.MemDHT
	pex   *pex.PEX
	memex *memex.Engine

	// herald is the reprovide loop. May be nil when the
	// anchor engine is the only announcer.
	herald *herald.MemHerald

	// anchor is the Anchor Node engine. nil if AnchorMode is
	// disabled in config.
	anchor *anchor.AnchorEngine

	// metrics is the optional Prometheus facade. nil = no-op.
	metrics *metrics.Metrics

	// retryBackoff configures the Memex session retry schedule.
	retryBackoff config.RetryBackoffConfig

	// logger is the structured-logging handle. nil = no-op.
	logger *slog.Logger

	// anchorsCache caches known anchor peer IDs to prevent blocking network searches on every request.
	anchorsMu        sync.RWMutex
	anchorsCache     map[string]struct{}
	anchorsCacheTime time.Time

	// statCache caches StatInfo for sealed MIDs to eliminate 4800+ BadgerDB block walks per Stat call.
	statMu    sync.RWMutex
	statCache map[string]serverpkg.StatInfo
}

// slogAnchorLogger adapts *slog.Logger to anchor.Logger.
type slogAnchorLogger struct {
	l *slog.Logger
}

func (a *slogAnchorLogger) Infof(format string, args ...any) {
	a.l.Info(fmt.Sprintf(format, args...))
}

func (a *slogAnchorLogger) Errorf(format string, args ...any) {
	a.l.Error(fmt.Sprintf(format, args...))
}

// Compile-time check that daemonBackend satisfies server.Backend.
var _ serverpkg.Backend = (*daemonBackend)(nil)

// Add reads the file, builds the DAG, seals the root, and
// announces it to the DHT. chunker/chunkSize come from the
// gRPC request; if empty/zero, fixed 256 KiB is used.
func (b *daemonBackend) Add(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name, mimeType string) (serverpkg.AddResult, error) {
	return b.AddWithProgress(ctx, path, chunker, chunkSize, sealRoot, name, mimeType, nil)
}

// AddWithProgress ingests a local file, reporting bytes read
// from the source through progressFn (when non-nil) so callers
// can stream ingest progress. It is the single ingest
// implementation; Add is the no-progress wrapper.
func (b *daemonBackend) AddWithProgress(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name, mimeType string, progressFn func(processed, total uint64)) (serverpkg.AddResult, error) {
	if path == "" {
		return serverpkg.AddResult{}, errors.New("add: empty path")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return serverpkg.AddResult{}, err
		}
		path = abs
	}
	f, err := os.Open(path)
	if err != nil {
		return serverpkg.AddResult{}, err
	}
	defer f.Close()

	// Total size drives the progress denominator. A stat
	// failure is non-fatal: progress just reports an unknown
	// total (0).
	var totalBytes uint64
	if fi, serr := f.Stat(); serr == nil && fi.Size() > 0 {
		totalBytes = uint64(fi.Size())
	}



	if name == "" {
		name = filepath.Base(path)
	}

	ingestRes, err := ingest.IngestFile(ctx, b.store, f, ingest.Options{
		Name:       name,
		MimeType:   mimeType,
		Chunker:    chunker,
		ChunkSize:  int(chunkSize),
		Seal:       sealRoot,
		ProgressFn: progressFn,
	})
	if err != nil {
		return serverpkg.AddResult{}, fmt.Errorf("add: %w", err)
	}

	root := ingestRes.MID
	size := ingestRes.Size
	blocks := ingestRes.Blocks

	// Emit a terminal 100% so a fast ingest that never
	// tripped the throttle still lands on a full bar.
	if progressFn != nil {
		final := totalBytes
		if final == 0 {
			final = size
		}
		progressFn(final, final)
	}

	// Wire Reed-Solomon Erasure Coding root manifest
	erasureCfg := erasure.AdaptiveConfig(int64(size))
	rootManifest := &membusspb.ErasureManifest{
		OriginalMid:  root.String(),
		DataShards:   uint32(erasureCfg.DataShards),
		ParityShards: uint32(erasureCfg.ParityShards),
		OriginalSize: uint64(size),
	}
	_ = erasure.SetManifest(b.store, root, rootManifest)

	if sealRoot {
		if err := b.store.Seal(root, true); err != nil {
			return serverpkg.AddResult{}, fmt.Errorf("add: seal: %w", err)
		}
		// Announce root and all erasure shards to the DHT
		if b.dht != nil {
			go func(r mid.MID) {
				announceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				provideRecursive(announceCtx, b.dht, b.store, r)
			}(root)
		}
	}

	return serverpkg.AddResult{
		MID:      root.String(),
		Size:     size,
		Blocks:   blocks,
		Sealed:   sealRoot,
		Name:     name,
		MimeType: mimeType,
	}, nil
}

// Get returns the content of midStr. If the MID is not local,
// the backend falls back to a Memex fetch using the DHT's
// provider list. The returned reader is the raw DAG-resolved
// bytes.
func (b *daemonBackend) Get(ctx context.Context, midStr string, offset, limit uint64) (io.ReadCloser, error) {
	root, err := mid.Parse(midStr)
	if err != nil {
		return nil, fmt.Errorf("get: parse mid: %w", err)
	}
	has, err := b.store.Has(root)
	if err != nil {
		return nil, err
	}
	if has {
		if complete, cerr := isDAGComplete(b.store, root); cerr != nil || !complete {
			has = false
		}
	}
	if !has && b.memex != nil {
		// Try DHT to find a provider, then Memex-fetch.
		var provs []peer.AddrInfo
		if b.dht != nil {
			provCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			provs, _ = b.dht.FindProviders(provCtx, root)
			cancel()
		}
		if len(provs) == 0 && b.host != nil {
			// Fallback: query all currently connected swarm peers
			for _, pid := range b.host.Network().Peers() {
				provs = append(provs, b.host.Peerstore().PeerInfo(pid))
			}
		}
		if len(provs) > 0 {
			sess, serr := memex.NewSession(memex.SessionConfig{
				Engine:         b.memex,
				Root:           root,
				Providers:      provs,
				Timeout:        0, // Activity-based idle timeout (streams continuously for multi-GB/TB data)
				ProviderFinder: b.dht.FindProviders,
			})
			if serr == nil {
				if rc, ferr := sess.FetchWithBackoff(ctx, memex.DefaultRetryConfig()); ferr == nil && rc != nil {
					has = true
					_, _ = io.Copy(io.Discard, rc)
					if c, ok := rc.(io.Closer); ok {
						_ = c.Close()
					}
				}
			}
		}
		if !has {
			return nil, fmt.Errorf("get: mid not found locally and no provider available")
		}
	}

	return b.resolveContent(ctx, root, offset, limit)
}

// GetWithProgress returns the content of midStr with progress
// reporting. If the MID is not local, the backend falls back
// to a Memex fetch using the DHT's provider list. progressFn
// is called as blocks arrive with the running total of bytes
// received and total bytes (total may be 0 until all blocks
// are known).
func (b *daemonBackend) GetWithProgress(ctx context.Context, midStr string, offset, limit uint64, progressFn func(update memex.ProgressUpdate)) (io.ReadCloser, serverpkg.ContentMeta, error) {
	root, err := mid.Parse(midStr)
	if err != nil {
		return nil, serverpkg.ContentMeta{}, fmt.Errorf("get: parse mid: %w", err)
	}
	has, err := b.store.Has(root)
	if err != nil {
		return nil, serverpkg.ContentMeta{}, err
	}
	if has {
		if complete, cerr := isDAGComplete(b.store, root); cerr != nil || !complete {
			has = false
		}
	}
	if !has && b.memex != nil {
		// Try DHT to find a provider, then Memex-fetch.
		var provs []peer.AddrInfo
		if b.dht != nil {
			provCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			provs, _ = b.dht.FindProviders(provCtx, root)
			cancel()
		}
		if len(provs) == 0 && b.host != nil {
			// Fallback: query all currently connected swarm peers
			for _, pid := range b.host.Network().Peers() {
				provs = append(provs, b.host.Peerstore().PeerInfo(pid))
			}
		}
		if len(provs) > 0 {
			sess, serr := memex.NewSession(memex.SessionConfig{
				Engine:         b.memex,
				Root:           root,
				Providers:      provs,
				Timeout:        0, // Activity-based idle timeout (streams continuously for multi-GB/TB data)
				ProgressFn:     progressFn,
				ProviderFinder: b.dht.FindProviders,
			})
			if serr == nil {
				if rc, ferr := sess.FetchWithBackoff(ctx, memex.DefaultRetryConfig()); ferr == nil && rc != nil {
					has = true
					_, _ = io.Copy(io.Discard, rc)
					if c, ok := rc.(io.Closer); ok {
						_ = c.Close()
					}
					oi, _ := store.GetObjectInfo(b.store, root)
					if !oi.IsRoot {
						oi.IsRoot = true
						_ = store.SetObjectInfo(b.store, root, oi)
					}
					if b.dht != nil {
						announceCtx, cancelAnnounce := context.WithTimeout(context.Background(), 10*time.Second)
						go func() {
							defer cancelAnnounce()
							provideRecursive(announceCtx, b.dht, b.store, root)
						}()
					}
				}
			}
		}
		if !has {
			return nil, serverpkg.ContentMeta{}, fmt.Errorf("get: mid not found locally and no provider available")
		}
	}

	rc, err := b.resolveContent(ctx, root, offset, limit)
	if err != nil {
		return nil, serverpkg.ContentMeta{}, err
	}
	// Metadata is now resolvable locally: the ObjectInfo was
	// either written at Add time or delivered over the wire by
	// Memex during the fetch above (see Engine.StoreObjectInfos).
	// The server sends this to the client as a header frame so
	// downloads pulled from the network are named / sized just
	// like local ones, instead of falling back to <mid>.bin.
	return rc, b.contentMeta(ctx, root), nil
}

// contentMeta collects the best-effort user-facing metadata
// for a resolved root: the persisted ObjectInfo name / MIME
// type, and the total content size.
//
// Size is resolved the same way the gateway computes it, so
// downloads report the real byte count instead of 0: for a
// MemFS file node it is the node's logical TotalSize (a DAG
// walk over the node only sums the envelope/link blocks, not
// the file payload, which is why the ObjectInfo fallback
// alone reported 0); for a plain Merkle-DAG root it is the
// sum of all block bytes.
func (b *daemonBackend) contentMeta(ctx context.Context, root mid.MID) serverpkg.ContentMeta {
	var meta serverpkg.ContentMeta
	if oi, err := store.GetObjectInfo(b.store, root); err == nil {
		meta.Name = oi.Name
		meta.MimeType = oi.MimeType
		meta.Size = oi.Size
	}
	if meta.Size == 0 {
		if root.Codec() == mid.CodecMemFS {
			mr := memfs.NewResolver(b.store)
			if node, err := mr.Resolve(ctx, root); err == nil {
				meta.Size = node.TotalSize()
				if meta.MimeType == "" {
					meta.MimeType = node.MimeType()
				}
			}
		} else if _, size, err := countDAG(b.store, root); err == nil {
			meta.Size = size
		}
	}
	if meta.MimeType == "" {
		meta.MimeType = store.SniffMime(meta.Name)
	}
	return meta
}

// resolveContent reassembles the content of root into a
// sequential reader, dispatching on the MID codec so that
// MemFS FILE nodes (codec 0x72) are read through the MemFS
// reader and plain Merkle-DAG roots through the DAG resolver.
//
// A file added through the HTTP /api/v1/add endpoint (and
// every child of a directory upload) is stored as a MemFS
// FILE node, not a DAGNode. Resolving such a MID with the
// DAG resolver yields the protobuf envelope bytes instead of
// the file payload, which surfaced to users as a 0-byte /
// corrupt download. Dispatching on the codec — the same way
// store.Walk and the gateway's Resolve already do — keeps
// both ingest paths readable.
//
// The blockstore is wrapped so leaves that are not yet local
// are pulled from the network on demand, matching the
// gateway/explorer resolvers.
func (b *daemonBackend) resolveContent(ctx context.Context, root mid.MID, offset, limit uint64) (io.ReadCloser, error) {
	bs := &fetchingBlockstore{Blockstore: b.store, b: b, ctx: ctx}

	var rc io.Reader
	if root.Codec() == mid.CodecMemFS {
		mr := memfs.NewResolver(bs)
		node, err := mr.Resolve(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("get: resolve memfs node: %w", err)
		}
		if !node.IsFile() {
			return nil, fmt.Errorf("get: %s is a %s, not a file; use the path or ls API", root.String(), memFSTypeString(node.GetType()))
		}
		openRc, err := mr.Open(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("get: open memfs file: %w", err)
		}
		rc = openRc
	} else {
		resolver := dag.NewResolver(bs)
		rawRc, err := resolver.Resolve(root, nil)
		if err != nil {
			return nil, err
		}
		rc = rawRc
	}

	if offset == 0 && limit == 0 {
		return readerCloser(rc), nil
	}
	return io.NopCloser(sectionReader(rc, offset, limit)), nil
}

// readerCloser wraps rc in an io.ReadCloser, calling the
// underlying Close if rc already implements io.Closer (as the
// MemFS reader does) and a no-op Close otherwise.
func readerCloser(rc io.Reader) io.ReadCloser {
	if c, ok := rc.(io.ReadCloser); ok {
		return c
	}
	return io.NopCloser(rc)
}

// Seal pins midStr. If recursive is true, the daemon walks the
// DAG and seals every reachable block.
//
// A Seal is a forward-looking pin: the seal record is written
// even when the recursive walk does not reach every block
// (e.g. the operator pins a MID they have not fetched yet).
// Missing blocks are surfaced as a soft warning through the
// daemon's logger when one is configured; the RPC still
// succeeds so the CLI / explorer can complete the action.
func (b *daemonBackend) Seal(ctx context.Context, midStr string, recursive bool) (serverpkg.SealResult, error) {
	root, err := mid.Parse(midStr)
	if err != nil {
		return serverpkg.SealResult{}, fmt.Errorf("seal: parse mid: %w", err)
	}
	// Idempotency check.
	if sealed, _ := b.store.IsSealed(root); sealed {
		return serverpkg.SealResult{Pinned: 0, Already: true}, nil
	}
	if err := b.store.Seal(root, recursive); err != nil {
		// A walk-incomplete error is informational: the
		// pin record is already on disk and the missing
		// blocks will be filled in by a later fetch.
		// Log it and continue with the success path.
		if errors.Is(err, store.ErrSealWalkIncomplete) {
			if b.logger != nil {
				b.logger.Warn("seal: walk incomplete; missing blocks will be filled in on first fetch",
					"mid", midStr, "err", err.Error())
			}
		} else {
			return serverpkg.SealResult{}, fmt.Errorf("seal: %w", err)
		}
	}
	// Count newly pinned blocks. The walk is best-effort;
	// missing blocks simply do not contribute to the count.
	blocks := uint64(0)
	if recursive {
		seen := map[string]struct{}{}
		_ = walkDAG(b.store, root, func(m mid.MID) error {
			if _, ok := seen[m.String()]; !ok {
				seen[m.String()] = struct{}{}
				blocks++
			}
			return nil
		})
	} else {
		blocks = 1
	}
	// Announce.
	if b.dht != nil {
		go func(r mid.MID) {
			announceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			provideRecursive(announceCtx, b.dht, b.store, r)
		}(root)
	}
	return serverpkg.SealResult{Pinned: blocks, Already: false}, nil
}

// Unseal removes the pin on midStr.
func (b *daemonBackend) Unseal(ctx context.Context, midStr string) (uint64, error) {
	root, err := mid.Parse(midStr)
	if err != nil {
		return 0, fmt.Errorf("unseal: parse mid: %w", err)
	}
	if err := b.store.Unseal(root); err != nil {
		return 0, err
	}
	b.statMu.Lock()
	if b.statCache != nil {
		delete(b.statCache, midStr)
	}
	b.statMu.Unlock()
	return 1, nil
}

// Stat returns a snapshot describing midStr.
func (b *daemonBackend) Stat(ctx context.Context, midStr string) (serverpkg.StatInfo, error) {
	root, err := mid.Parse(midStr)
	if err != nil {
		return serverpkg.StatInfo{Present: false}, nil
	}

	b.statMu.RLock()
	if b.statCache != nil {
		if cached, ok := b.statCache[midStr]; ok && cached.Present && cached.Sealed {
			b.statMu.RUnlock()
			return cached, nil
		}
	}
	b.statMu.RUnlock()

	has, err := b.store.Has(root)
	if err != nil {
		return serverpkg.StatInfo{}, err
	}
	if !has {
		return serverpkg.StatInfo{Present: false}, nil
	}
	complete, err := isDAGComplete(b.store, root)
	if err != nil {
		return serverpkg.StatInfo{}, err
	}
	if !complete {
		return serverpkg.StatInfo{Present: false}, nil
	}
	sealed, _ := b.store.IsSealed(root)
	blocks, size, err := countDAG(b.store, root)
	if err != nil {
		return serverpkg.StatInfo{}, err
	}
	// Phase 19: attach the per-MID ObjectInfo so the
	// CLI and the explorer can show / render the
	// upload name and the sniffed MIME type.
	oi, _ := store.GetObjectInfo(b.store, root)
	info := serverpkg.StatInfo{
		Present:  true,
		Size:     size,
		Blocks:   blocks,
		Sealed:   sealed,
		Codec:    root.Codec(),
		Name:     oi.Name,
		MimeType: oi.MimeType,
	}

	manifest, _ := erasure.GetManifest(b.store, root)
	if manifest != nil {
		available := uint32(0)
		for _, smStr := range manifest.ShardMids {
			if sm, perr := mid.Parse(smStr); perr == nil {
				if has, _ := b.store.Has(sm); has {
					available++
				}
			}
		}
		total := manifest.DataShards + manifest.ParityShards
		info.Erasure = &serverpkg.ErasureInfo{
			DataShards:      manifest.DataShards,
			ParityShards:    manifest.ParityShards,
			ShardMIDs:       manifest.ShardMids,
			ShardsAvailable: available,
			Degraded:        available < total,
		}
	}

	if b.dht != nil {
		provCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		provs, _ := b.dht.FindProviders(provCtx, root)
		cancel()

		if b.store != nil && b.host != nil {
			if has, err := b.store.Has(root); err == nil && has {
				localID := b.host.ID()
				foundLocal := false
				for _, p := range provs {
					if p.ID == localID {
						foundLocal = true
						break
					}
				}
				if !foundLocal {
					provs = append(provs, peer.AddrInfo{ID: localID, Addrs: b.host.Addrs()})
				}
			}
		}

		info.Sealers = len(provs)

		anchors := b.getKnownAnchors(ctx)
		for _, p := range provs {
			if _, ok := anchors[p.ID.String()]; ok {
				info.AnchorSealers++
			}
		}
	}

	if sealed {
		b.statMu.Lock()
		if b.statCache == nil {
			b.statCache = make(map[string]serverpkg.StatInfo)
		}
		b.statCache[midStr] = info
		b.statMu.Unlock()
	}

	return info, nil
}

func (b *daemonBackend) getKnownAnchors(ctx context.Context) map[string]struct{} {
	b.anchorsMu.RLock()
	if b.anchorsCache != nil && time.Since(b.anchorsCacheTime) < 30*time.Second {
		defer b.anchorsMu.RUnlock()
		return b.anchorsCache
	}
	b.anchorsMu.RUnlock()

	anchors := make(map[string]struct{})
	if b.anchor != nil {
		for _, a := range b.anchor.AnchorPeers() {
			anchors[a.ID.String()] = struct{}{}
		}
	}
	if b.dht != nil {
		sCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		ch, err := b.dht.SearchValue(sCtx, anchor.AnchorRegistryKey)
		if err == nil {
			for val := range ch {
				ai, err := anchor.DecodeAnchorValue(val)
				if err == nil && ai.ID != "" {
					anchors[ai.ID.String()] = struct{}{}
				}
			}
		}
	}

	b.anchorsMu.Lock()
	b.anchorsCache = anchors
	b.anchorsCacheTime = time.Now()
	b.anchorsMu.Unlock()

	return anchors
}

// Peers returns the local PEX peer table.
func (b *daemonBackend) Peers(limit uint32) ([]serverpkg.NodePeerInfo, uint32, error) {
	if b.pex == nil {
		return nil, 0, nil
	}
	anchors := b.getKnownAnchors(context.Background())
	infos := b.pex.Peers()
	out := make([]serverpkg.NodePeerInfo, 0, len(infos))
	for _, p := range infos {
		addrs := make([]string, 0, len(p.Addrs))
		for _, a := range p.Addrs {
			addrs = append(addrs, a)
		}
		_, isAnchor := anchors[p.PeerId]
		out = append(out, serverpkg.NodePeerInfo{
			PeerID:   p.PeerId,
			Addrs:    addrs,
			IsAnchor: isAnchor,
		})
	}
	if limit > 0 && uint32(len(out)) > limit {
		out = out[:limit]
	}
	return out, uint32(len(infos)), nil
}

// DHTPeek asks the DHT who provides midStr.
func (b *daemonBackend) DHTPeek(ctx context.Context, midStr string, limit uint32) ([]serverpkg.NodePeerInfo, error) {
	if b.dht == nil {
		return nil, nil
	}
	root, err := mid.Parse(midStr)
	if err != nil {
		return nil, err
	}
	provCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	provs, err := b.dht.FindProviders(provCtx, root)
	if err != nil {
		return nil, err
	}
	anchors := b.getKnownAnchors(ctx)
	out := make([]serverpkg.NodePeerInfo, 0, len(provs))
	for _, p := range provs {
		addrs := make([]string, 0, len(p.Addrs))
		for _, a := range p.Addrs {
			addrs = append(addrs, a.String())
		}
		_, isAnchor := anchors[p.ID.String()]
		out = append(out, serverpkg.NodePeerInfo{
			PeerID:   p.ID.String(),
			Addrs:    addrs,
			IsAnchor: isAnchor,
		})
	}
	if limit > 0 && uint32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GC runs garbage collection on the local store.
func (b *daemonBackend) GC(ctx context.Context, all bool) (serverpkg.GCInfo, error) {
	if b.store == nil {
		return serverpkg.GCInfo{}, errors.New("gc: no store")
	}
	freed, err := b.store.GC(ctx)
	if err != nil {
		return serverpkg.GCInfo{}, err
	}
	// Count kept blocks. The Store interface does not
	// expose a direct kept count post-GC, but we can use
	// AllBlocks on the BADGER store. If the in-memory
	// store is in use (tests), this returns 0.
	if s, ok := b.store.(interface {
		AllBlocks() ([]mid.MID, error)
	}); ok {
		mids, err := s.AllBlocks()
		if err == nil {
			return serverpkg.GCInfo{BytesFreed: freed, BlocksKept: uint64(len(mids))}, nil
		}
	}
	return serverpkg.GCInfo{BytesFreed: freed, BlocksKept: 0}, nil
}

// Delete recursively removes the given MID and its children from the store.
func (b *daemonBackend) Delete(ctx context.Context, midStr string) (serverpkg.DeleteResult, error) {
	if b.store == nil {
		return serverpkg.DeleteResult{}, errors.New("delete: no store")
	}
	m, err := mid.Parse(midStr)
	if err != nil {
		return serverpkg.DeleteResult{}, fmt.Errorf("delete: parse mid: %w", err)
	}

	type recursiveDeleter interface {
		DeleteRecursive(m mid.MID) (uint64, uint64, error)
	}

	if rd, ok := b.store.(recursiveDeleter); ok {
		deleted, freed, err := rd.DeleteRecursive(m)
		if err != nil {
			return serverpkg.DeleteResult{}, err
		}
		if b.dht != nil {
			_ = b.dht.RemoveProviderRecord(m)
		}
		b.statMu.Lock()
		if b.statCache != nil {
			delete(b.statCache, midStr)
		}
		b.statMu.Unlock()
		return serverpkg.DeleteResult{
			BlocksDeleted: deleted,
			BytesFreed:    freed,
		}, nil
	}

	return serverpkg.DeleteResult{}, errors.New("delete: store does not support recursive deletion")
}

// AnchorStatus returns the anchor engine's stats. If the
// anchor engine is not running, returns zero-valued info
// with the host's PeerID.
func (b *daemonBackend) AnchorStatus() serverpkg.AnchorInfo {
	if b.anchor == nil {
		return serverpkg.AnchorInfo{
			PeerID: peerIDString(b.host),
		}
	}
	st := b.anchor.Status()
	return serverpkg.AnchorInfo{
		PeerID:     st.PeerID,
		UptimeSecs: int64(st.Uptime.Seconds()),
		BlocksHeld: st.BlocksHeld,
		Anchors:    int32(st.Anchors),
		Backlog:    int32(st.Backlog),
		Synced:     st.Synced,
	}
}

func peerIDString(h host.Host) string {
	if h == nil {
		return ""
	}
	return h.ID().String()
}

// countDAG walks a DAG rooted at root and returns the number
// of nodes and the total bytes (sum of leaf payload sizes).
func countDAG(bs store.Store, root mid.MID) (uint64, uint64, error) {
	var (
		nodes uint64
		bytes uint64
	)
	err := walkDAG(bs, root, func(m mid.MID) error {
		nodes++
		raw, err := bs.Get(m)
		if err != nil {
			return err
		}
		bytes += uint64(len(raw))
		return nil
	})
	return nodes, bytes, err
}

// walkDAG performs a depth-first walk of the DAG and calls
// visit for every MID encountered (the root plus all
// descendants).
func walkDAG(bs store.Store, root mid.MID, visit func(mid.MID) error) error {
	return store.Walk(bs, root, func(m mid.MID, leaf bool) error {
		return visit(m)
	})
}

// countingReader wraps a reader and reports the running byte
// count through fn on every read. It is used to drive ingest
// progress: the DAG builder pulls the whole source through it,
// so the count tracks how much of the file has been consumed.
// fn must be cheap and non-blocking (the server funnels it
// through a non-blocking channel send).
type countingReader struct {
	r     io.Reader
	total uint64
	read  uint64
	fn    func(processed, total uint64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.read += uint64(n)
		c.fn(c.read, c.total)
	}
	return n, err
}

// sectionReader returns an io.ReadCloser that yields up to
// limit bytes from rc starting at offset.
func sectionReader(rc io.Reader, offset, limit uint64) io.Reader {
	if offset > 0 {
		// Discard offset bytes.
		if _, err := io.CopyN(io.Discard, rc, int64(offset)); err != nil {
			return io.NopCloser(bytes.NewReader(nil))
		}
	}
	if limit == 0 {
		return io.NopCloser(rc)
	}
	return io.NopCloser(io.LimitReader(rc, int64(limit)))
}

// provideRecursive walks the DAG starting at root, announcing the root MID
// and all child block MIDs to the DHT using a parallel worker pool.
func provideRecursive(ctx context.Context, dht *dht.MemDHT, s store.Store, root mid.MID) {
	if dht == nil || s == nil || root.IsZero() {
		return
	}
	_ = dht.Provide(ctx, root)

	workCh := make(chan mid.MID, 256)
	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	if workers < 2 {
		workers = 2
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range workCh {
				if ctx.Err() != nil {
					return
				}
				_ = dht.Provide(ctx, m)
			}
		}()
	}

	_ = store.Walk(s, root, func(m mid.MID, leaf bool) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case workCh <- m:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})

	close(workCh)
	wg.Wait()
}

// isDAGComplete checks if all blocks in the Merkle DAG rooted at root are
// present in the store.
func isDAGComplete(s store.Store, root mid.MID) (bool, error) {
	if s == nil || root.IsZero() {
		return false, nil
	}
	err := store.Walk(s, root, func(m mid.MID, leaf bool) error {
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || strings.Contains(err.Error(), "block not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
