// explorerAdapter is the production implementation of
// explorer.Backend, backed by the daemonBackend. It glues
// together the live subsystems (store, PEX, DHT, anchor
// engine, host identity, herald, store size) into the
// read-only surface the explorer needs.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/nnlgsakib/membuss/anchor"
	"github.com/nnlgsakib/membuss/core/descriptor"
	"github.com/nnlgsakib/membuss/core/keyring"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/memlink"
	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/core/version"
	explorer "github.com/nnlgsakib/membuss/gateway/explorer"
	memgate "github.com/nnlgsakib/membuss/gateway/memgate_v2"
	hostpkg "github.com/nnlgsakib/membuss/net/host"
	memex "github.com/nnlgsakib/membuss/net/memex_v2"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

var _ explorer.Backend = (*explorerAdapter)(nil)

// explorerAdapter wraps daemonBackend to satisfy
// explorer.Backend.
type explorerAdapter struct {
	b *daemonBackend
	// started is when the daemon started; used for
	// Uptime. Cached because Time.Now() at process start
	// is the only sensible answer.
	started time.Time
	// anchorMode is the immutable config value the
	// daemon was started with.
	anchorMode bool
	keyring    *keyring.KeyRing
	memnsRes   *memns.Resolver
	// allRoots tracks all root MIDs known to this node
	// (both sealed and unsealed). Populated from sealed
	// list on startup, extended as content is added or
	// fetched.
	allRoots map[string]struct{}

	// Cached statistics to avoid expensive BadgerDB iteration scans on every WS ticker
	statsMu     sync.Mutex
	cachedBytes uint64
	cachedCount uint64
	lastStats   time.Time
	updating    bool
}

func newExplorerAdapter(b *daemonBackend, anchorMode bool, keyring *keyring.KeyRing, memnsRes *memns.Resolver) *explorerAdapter {
	a := &explorerAdapter{b: b, started: time.Now(), anchorMode: anchorMode, keyring: keyring, memnsRes: memnsRes, allRoots: make(map[string]struct{})}
	// Populate allRoots from sealed and object MIDs on startup.
	if b.store != nil {
		if sealed, err := b.store.AllSealed(); err == nil {
			for _, m := range sealed {
				a.allRoots[m.String()] = struct{}{}
			}
		}
		if objMIDs, err := b.store.AllObjectMIDs(); err == nil {
			for _, m := range objMIDs {
				a.allRoots[m.String()] = struct{}{}
			}
		}
	}
	return a
}

// Stat returns a metadata snapshot.
func (a *explorerAdapter) Stat(ctx context.Context, m mid.MID) (explorer.ContentInfo, error) {
	st, err := a.b.Stat(ctx, m.String())
	if err != nil {
		return explorer.ContentInfo{}, err
	}
	info := explorer.ContentInfo{
		MID:           m.String(),
		Size:          st.Size,
		Blocks:        st.Blocks,
		Sealed:        st.Sealed,
		Present:       st.Present,
		Codec:         st.Codec,
		Name:          st.Name,
		MimeType:      st.MimeType,
		Sealers:       st.Sealers,
		AnchorSealers: st.AnchorSealers,
	}
	if st.Erasure != nil {
		info.DataShards = int(st.Erasure.DataShards)
		info.ParityShards = int(st.Erasure.ParityShards)
		info.TotalShards = info.DataShards + info.ParityShards
		info.ShardsAvailable = int(st.Erasure.ShardsAvailable)
		info.Degraded = st.Erasure.Degraded
		info.ShardMIDs = st.Erasure.ShardMIDs
	}
	return info, nil
}

// Seal pins m recursively. We delegate to daemonBackend.
func (a *explorerAdapter) Seal(ctx context.Context, m mid.MID) error {
	_, err := a.b.Seal(ctx, m.String(), true)
	if err == nil {
		a.allRoots[m.String()] = struct{}{}
	}
	return err
}

// Unseal removes the pin.
func (a *explorerAdapter) Unseal(ctx context.Context, m mid.MID) error {
	_, err := a.b.Unseal(ctx, m.String())
	return err
}

// Delete recursively removes the given MID and its children.
func (a *explorerAdapter) Delete(ctx context.Context, m mid.MID) (uint64, uint64, error) {
	res, err := a.b.Delete(ctx, m.String())
	if err != nil {
		return 0, 0, err
	}
	delete(a.allRoots, m.String())
	return res.BlocksDeleted, res.BytesFreed, nil
}

func (a *explorerAdapter) DropAll(ctx context.Context) error {
	b := a.b
	if b == nil || b.store == nil {
		return errors.New("store not initialized")
	}
	if b.memex != nil {
		b.memex.CancelAllSessions()
	}
	err := b.store.DropAll()
	if err != nil {
		return err
	}
	a.statsMu.Lock()
	a.cachedBytes = 0
	a.cachedCount = 0
	a.lastStats = time.Now()
	a.allRoots = make(map[string]struct{})
	a.statsMu.Unlock()
	return nil
}

// Providers returns DHT-known providers for m.
func (a *explorerAdapter) Providers(ctx context.Context, m mid.MID, limit int) ([]string, error) {
	b := a.b
	if b == nil {
		return nil, nil
	}
	var provs []peer.AddrInfo
	if b.dht != nil {
		provCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		provs, _ = b.dht.FindProviders(provCtx, m)
		cancel()
	}

	if b.store != nil && b.host != nil {
		if has, err := b.store.Has(m); err == nil && has {
			localID := b.host.ID()
			foundLocal := false
			for _, p := range provs {
				if p.ID == localID {
					foundLocal = true
					break
				}
			}
			if !foundLocal {
				provs = append([]peer.AddrInfo{{
					ID:    localID,
					Addrs: b.host.Addrs(),
				}}, provs...)
			}
		}
	}

	var lim uint32
	if limit > 0 {
		lim = uint32(limit)
	}
	if lim > 0 && uint32(len(provs)) > lim {
		provs = provs[:lim]
	}
	out := make([]string, 0, len(provs))
	for _, p := range provs {
		addrs := make([]string, 0, len(p.Addrs))
		for _, a := range p.Addrs {
			addrs = append(addrs, a.String())
		}
		// Format: peer_id\taddr1,addr2
		if len(addrs) == 0 {
			out = append(out, p.ID.String())
			continue
		}
		out = append(out, p.ID.String()+"\t"+joinStrings(addrs, ","))
	}
	return out, nil
}

// Resolve mirrors memgateAdapter.Resolve: when the MID is
// not local it asks the DHT for providers and runs a
// Memex session to fetch the missing blocks. The returned
// reader streams the reassembled DAG; the explorer closes
// it after draining.
//
// explorer.ErrNotFound is returned when the local store
// is empty AND the DHT has no provider records. The
// explorer package uses this to distinguish "not found"
// from "DHT had providers but Memex failed" so the
// template can show a "try again later" message instead
// of a hard 404.
func (a *explorerAdapter) Resolve(ctx context.Context, m mid.MID) (io.ReadCloser, explorer.ContentInfo, error) {
	return a.ResolveWithProgress(ctx, m, nil)
}

// ResolveWithProgress resolves a MID with progress reporting.
// progressFn is called as blocks arrive with the running total
// of bytes received and total bytes (total may be 0 until all
// blocks are known).
func (a *explorerAdapter) ResolveWithProgress(ctx context.Context, m mid.MID, progressFn func(blocksResolved, blocksTotal uint64)) (io.ReadCloser, explorer.ContentInfo, error) {
	b := a.b
	if b.store == nil {
		return nil, explorer.ContentInfo{}, errors.New("explorer: no store")
	}
	has, err := b.store.Has(m)
	if err != nil {
		return nil, explorer.ContentInfo{}, err
	}
	if has {
		if complete, cerr := isDAGComplete(b.store, m); cerr != nil || !complete {
			has = false
		}
	}
	if !has && b.dht != nil && b.memex != nil {
		provCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		provs, _ := b.dht.FindProviders(provCtx, m)
		cancel()
		if len(provs) == 0 && b.dht == nil {
			// Fallback: use currently connected swarm peers only if DHT is disabled
			for _, pid := range b.host.Network().Peers() {
				provs = append(provs, b.host.Peerstore().PeerInfo(pid))
			}
		}
		if len(provs) == 0 {
			// No DHT providers hold this MID -> return ErrNotFound immediately
			return nil, explorer.ContentInfo{}, explorer.ErrNotFound
		}
		sess, serr := memex.NewSession(memex.SessionConfig{
			Engine:         b.memex,
			Root:           m,
			Providers:      provs,
			Timeout:        10 * time.Minute,
			ProviderFinder: b.dht.FindProviders,
			ProgressFn: func(update memex.ProgressUpdate) {
				if progressFn != nil {
					progressFn(update.BlocksResolved, update.BlocksTotal)
				}
			},
		})
		var ferr error
		if serr == nil {
			var rc io.Reader
			rc, ferr = sess.FetchWithBackoff(ctx, memex.DefaultRetryConfig())
			if ferr == nil && rc != nil {
				has = true
				_, _ = io.Copy(io.Discard, rc)
				if c, ok := rc.(io.Closer); ok {
					_ = c.Close()
				}
			} else {
				// The Memex session reported progress (blocks
				// downloaded) but the final reassembly or
				// verification step failed. Re-check the store
				// because individual blocks may have been stored
				// even though the session-level Fetch errored.
				if complete, cerr := isDAGComplete(b.store, m); cerr == nil && complete {
					has = true
					ferr = nil
				}
			}
		} else {
			ferr = serr
		}
		if !has && ferr != nil {
			return nil, explorer.ContentInfo{}, ferr
		}
	}
	if !has {
		return nil, explorer.ContentInfo{}, explorer.ErrNotFound
	}
	// Track this MID as a known root so it appears in the file list.
	a.allRoots[m.String()] = struct{}{}
	// Reuse the memgate adapter's Resolve so the size /
	// blocks / sealed numbers are computed exactly the
	// same way the public gateway would compute them.
	mg := &memgateAdapter{b: b}
	rc, info, err := mg.Resolve(ctx, m)
	if err != nil {
		if errors.Is(err, errMGNotFound) {
			return nil, explorer.ContentInfo{}, explorer.ErrNotFound
		}
		return nil, explorer.ContentInfo{}, err
	}

	// Write ObjectInfo record so it persists across restarts.
	if oi, oerr := store.GetObjectInfo(b.store, m); oerr == nil {
		if oi.Name == "" {
			oi.Name = info.Name
		}
		if oi.MimeType == "" {
			oi.MimeType = info.MimeType
		}
		if oi.Size == 0 {
			oi.Size = info.Size
		}
		oi.IsRoot = true
		_ = store.SetObjectInfo(b.store, m, oi)
	}

	st, _ := a.b.Stat(ctx, m.String())
	return rc, explorer.ContentInfo{
		MID:           info.MID,
		Size:          info.Size,
		Blocks:        info.Blocks,
		Sealed:        info.Sealed,
		Name:          info.Name,
		MimeType:      info.MimeType,
		Sealers:       st.Sealers,
		AnchorSealers: st.AnchorSealers,
	}, nil
}

// memgate.ContentInfo is referenced via the embedded
// memgateAdapter call; keep an unused import guard so
// the file compiles even if the type is removed.
var _ memgate.ContentInfo

// Peers returns the local PEX peer table enriched with live host network metrics.
func (a *explorerAdapter) Peers(ctx context.Context, limit int) ([]explorer.PeerInfo, error) {
	infos, _, err := a.b.Peers(uint32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]explorer.PeerInfo, 0, len(infos))
	for _, p := range infos {
		info := explorer.PeerInfo{
			PeerID:    p.PeerID,
			Addrs:     p.Addrs,
			IsAnchor:  p.IsAnchor,
			Connected: false,
		}

		if pid, serr := peer.Decode(p.PeerID); serr == nil && a.b.host != nil {
			// 1. Live ping latency from libp2p peerstore
			lat := a.b.host.Peerstore().LatencyEWMA(pid)
			if lat > 0 {
				info.LatencyMs = lat.Milliseconds()
			}

			// 2. Real agent version negotiated during identify
			if av, gerr := a.b.host.Peerstore().Get(pid, "AgentVersion"); gerr == nil {
				if s, ok := av.(string); ok && s != "" {
					info.AgentVersion = s
				}
			}

			// 3. Open streams & active connection check
			conns := a.b.host.Network().ConnsToPeer(pid)
			if len(conns) > 0 {
				info.Connected = true
				seenStreams := make(map[string]bool)
				var streamList []string
				for _, c := range conns {
					for _, s := range c.GetStreams() {
						protoStr := string(s.Protocol())
						if protoStr != "" && !seenStreams[protoStr] {
							seenStreams[protoStr] = true
							streamList = append(streamList, protoStr)
						}
					}
				}
				info.Streams = streamList
			}
		}

		out = append(out, info)
	}
	return out, nil
}

// SealedMIDs lists all sealed MIDs in the local store.
func (a *explorerAdapter) SealedMIDs(ctx context.Context) ([]mid.MID, error) {
	b := a.b
	if b.store == nil {
		return nil, nil
	}
	return b.store.AllSealed()
}

// AllStoredMIDs lists every root MID in the local store,
// regardless of seal status, with its sealed flag.
//
// The union of roots is read directly from the store on every
// call (sealed MIDs + ObjectInfo roots), so the file list
// reflects reality no matter which path ingested the content:
// the explorer's own HTTP handlers, the HTTP API, or the
// gRPC/CLI add path. Every add path persists an ObjectInfo
// with IsRoot=true before (optionally) sealing, so this union
// is complete for sealed and unsealed uploads alike. We read
// from the store rather than the in-memory allRoots cache
// because the gRPC/CLI path writes to the store without going
// through this adapter, and would otherwise stay invisible
// until the daemon restarts.
func (a *explorerAdapter) AllStoredMIDs(ctx context.Context) ([]explorer.StoredMIDView, error) {
	b := a.b
	if b.store == nil {
		return nil, nil
	}
	sealed, err := b.store.AllSealed()
	if err != nil {
		return nil, err
	}
	sealedSet := make(map[string]struct{}, len(sealed))
	for _, m := range sealed {
		sealedSet[m.String()] = struct{}{}
	}

	// Union of every known root: sealed roots plus ObjectInfo
	// roots persisted by any add path.
	roots := make(map[string]struct{}, len(sealed))
	for key := range sealedSet {
		roots[key] = struct{}{}
	}
	if objMIDs, oerr := b.store.AllObjectMIDs(); oerr == nil {
		for _, m := range objMIDs {
			roots[m.String()] = struct{}{}
		}
	}

	out := make([]explorer.StoredMIDView, 0, len(roots))
	for key := range roots {
		m, err := mid.Parse(key)
		if err != nil {
			continue
		}
		name := ""
		var size uint64
		mime := "application/octet-stream"
		if info, serr := store.GetObjectInfo(b.store, m); serr == nil {
			name = info.Name
			size = info.Size
			if info.MimeType != "" {
				mime = info.MimeType
			}
		}
		out = append(out, explorer.StoredMIDView{
			MID:      key,
			Name:     name,
			Sealed:   func() bool { _, ok := sealedSet[key]; return ok }(),
			Size:     size,
			MimeType: mime,
		})
	}
	return out, nil
}

// SealedCount returns the count of sealed MIDs.
func (a *explorerAdapter) SealedCount(ctx context.Context) (int, error) {
	mids, err := a.SealedMIDs(ctx)
	if err != nil {
		return 0, err
	}
	return len(mids), nil
}

func (a *explorerAdapter) updateStatsCached(ctx context.Context) {
	a.statsMu.Lock()
	if a.updating || time.Since(a.lastStats) < 5*time.Second {
		a.statsMu.Unlock()
		return
	}
	a.updating = true
	a.statsMu.Unlock()

	defer func() {
		a.statsMu.Lock()
		a.updating = false
		a.statsMu.Unlock()
	}()

	var size uint64
	var count uint64

	if s, ok := a.b.store.(interface {
		AllBlocks() ([]mid.MID, error)
	}); ok {
		if mids, err := s.AllBlocks(); err == nil {
			count = uint64(len(mids))
		}
	}
	if sz, err := a.b.store.Size(); err == nil {
		size = sz
	}

	a.statsMu.Lock()
	a.cachedBytes = size
	a.cachedCount = count
	a.lastStats = time.Now()
	a.statsMu.Unlock()
}

// BlockCount returns the count of all blocks in the
// local store. Only meaningful for the BadgerDB-backed
// store; returns 0 for the in-memory store.
func (a *explorerAdapter) BlockCount(ctx context.Context) (uint64, error) {
	if a.b.store == nil {
		return 0, nil
	}
	a.updateStatsCached(ctx)
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	return a.cachedCount, nil
}

// StoreBytes returns the total bytes used by the store.
func (a *explorerAdapter) StoreBytes(ctx context.Context) (uint64, error) {
	if a.b.store == nil {
		return 0, nil
	}
	a.updateStatsCached(ctx)
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	return a.cachedBytes, nil
}

// AnchorPeers returns the registered anchor peers.
func (a *explorerAdapter) AnchorPeers(ctx context.Context) ([]explorer.AnchorRow, error) {
	var out []explorer.AnchorRow
	if a.b.anchor != nil {
		for _, ai := range a.b.anchor.AnchorPeers() {
			addrs := make([]string, 0, len(ai.Addrs))
			for _, m := range ai.Addrs {
				addrs = append(addrs, m.String())
			}
			out = append(out, explorer.AnchorRow{
				PeerID: ai.ID.String(),
				Addrs:  addrs,
			})
		}
	} else if a.b.dht != nil {
		sCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ch, err := a.b.dht.SearchValue(sCtx, "/membuss/anchors/v1")
		if err == nil {
			for val := range ch {
				ai, err := anchor.DecodeAnchorValue(val)
				if err == nil && ai.ID != "" {
					addrs := make([]string, 0, len(ai.Addrs))
					for _, m := range ai.Addrs {
						addrs = append(addrs, m.String())
					}
					out = append(out, explorer.AnchorRow{
						PeerID: ai.ID.String(),
						Addrs:  addrs,
					})
				}
			}
		}
	}
	return out, nil
}

// AnchorStatus returns the local anchor engine stats.
func (a *explorerAdapter) AnchorStatus(ctx context.Context) explorer.AnchorInfo {
	if a.b.anchor == nil {
		return explorer.AnchorInfo{
			PeerID: peerIDString(a.b.host),
		}
	}
	st := a.b.anchor.Status()
	return explorer.AnchorInfo{
		PeerID:     st.PeerID,
		UptimeSecs: int64(st.Uptime.Seconds()),
		BlocksHeld: st.BlocksHeld,
		Anchors:    int32(st.Anchors),
		Backlog:    int32(st.Backlog),
		Synced:     st.Synced,
	}
}

// LocalPeerID returns the local node's peer ID.
func (a *explorerAdapter) LocalPeerID(ctx context.Context) string {
	return peerIDString(a.b.host)
}

// LocalAddrs returns the local node's listen addrs.
func (a *explorerAdapter) LocalAddrs(ctx context.Context) []string {
	if a.b.host == nil {
		return nil
	}
	addrs := make([]string, 0, len(a.b.host.Addrs()))
	for _, ma := range a.b.host.Addrs() {
		addrs = append(addrs, ma.String())
	}
	return addrs
}

// NodeVersion returns the version + build string for the
// local node. Build is the value passed via --build.
func (a *explorerAdapter) NodeVersion(ctx context.Context) (string, string) {
	commit := version.GitCommit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if commit == "" {
		commit = "dev"
	}
	return version.Version, commit
}

// Uptime returns the time since the daemon started.
func (a *explorerAdapter) Uptime(ctx context.Context) time.Duration {
	return time.Since(a.started)
}

// AnchorMode reports whether the daemon was started with
// anchor mode enabled.
func (a *explorerAdapter) AnchorMode(ctx context.Context) bool {
	return a.anchorMode
}

// BandwidthStats returns the real-time bandwidth totals and rates.
func (a *explorerAdapter) BandwidthStats(ctx context.Context) (totalIn, totalOut int64, rateIn, rateOut float64, err error) {
	if wh, ok := a.b.host.(*hostpkg.Host); ok && wh != nil {
		totIn, totOut, rIn, rOut := wh.BandwidthTotals()
		return totIn, totOut, rIn, rOut, nil
	}
	return 0, 0, 0, 0, nil
}

// joinStrings is a tiny helper to format a peer addr list.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}

// Add ingests a stream from the explorer upload form.
func (a *explorerAdapter) Add(ctx context.Context, name string, r io.Reader) (explorer.ContentInfo, error) {
	b := a.b
	if b == nil || b.store == nil {
		return explorer.ContentInfo{}, errors.New("explorer: no backend")
	}
	if r == nil {
		return explorer.ContentInfo{}, errors.New("explorer: nil reader")
	}

	memBuilder := memfs.NewBuilder(b.store)
	mime := store.SniffMime(name)
	res, err := memBuilder.AddFile(name, r, 0o644, time.Time{}, mime)
	if err != nil {
		return explorer.ContentInfo{}, err
	}

	// Update ObjectInfo for the root MID to set IsRoot: true.
	if oi, err := store.GetObjectInfo(b.store, res.MID); err == nil {
		oi.IsRoot = true
		_ = store.SetObjectInfo(b.store, res.MID, oi)
	}

	_ = b.store.Seal(res.MID, true)
	if b.dht != nil {
		go func(r mid.MID) {
			announceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			provideRecursive(announceCtx, b.dht, b.store, r)
		}(res.MID)
	}

	a.allRoots[res.MID.String()] = struct{}{}
	return explorer.ContentInfo{
		MID:      res.MID.String(),
		Size:     res.Size,
		Blocks:   res.Block,
		Sealed:   true,
		Name:     name,
		MimeType: mime,
		Present:  true,
	}, nil
}

// AddDirectory ingests a directory as MemFS from a set of files with relative paths.
func (a *explorerAdapter) AddDirectory(ctx context.Context, name string, files []explorer.DirectoryFile) (explorer.ContentInfo, error) {
	b := a.b
	if b == nil || b.store == nil {
		return explorer.ContentInfo{}, errors.New("explorer: no backend")
	}
	if len(files) == 0 {
		return explorer.ContentInfo{}, errors.New("explorer: no files")
	}

	commonPrefix := ""
	if len(files) > 0 {
		first := strings.ReplaceAll(files[0].Path, "\\", "/")
		sub := strings.Split(first, "/")
		if len(sub) > 1 {
			prefix := sub[0] + "/"
			allHave := true
			for _, f := range files {
				relPath := strings.ReplaceAll(f.Path, "\\", "/")
				if !strings.HasPrefix(relPath, prefix) {
					allHave = false
					break
				}
			}
			if allHave {
				commonPrefix = prefix
			}
		}
	}

	var streamEntries []memfs.StreamEntry
	for _, f := range files {
		rel := strings.ReplaceAll(f.Path, "\\", "/")
		if commonPrefix != "" {
			rel = strings.TrimPrefix(rel, commonPrefix)
		}
		streamEntries = append(streamEntries, memfs.StreamEntry{
			Path: rel,
			Size: f.Size,
			R:    f.R,
		})
	}

	memBuilder := memfs.NewBuilder(b.store)
	res, err := memBuilder.AddDirectoryStream(streamEntries)
	if err != nil {
		return explorer.ContentInfo{}, err
	}

	_ = b.store.Seal(res.MID, true)
	if b.dht != nil {
		go func(r mid.MID) {
			announceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			provideRecursive(announceCtx, b.dht, b.store, r)
		}(res.MID)
	}

	// Use uploader-supplied folder name, or fallback.
	name = filepath.Clean(name)
	if name == "." || name == "/" || name == "\\" {
		name = ""
	}
	dirName := name
	if dirName == "" {
		dirName = "upload"
		if len(files) > 0 && files[0].Path != "" {
			parts := strings.Split(strings.ReplaceAll(files[0].Path, "\\", "/"), "/")
			if len(parts) > 0 && parts[0] != "" {
				dirName = parts[0]
			}
		}
	}

	_ = store.SetObjectInfo(b.store, res.MID, store.ObjectInfo{
		Name:     dirName,
		MimeType: "inode/directory",
		Size:     res.Size,
		IsRoot:   true,
	})

	a.allRoots[res.MID.String()] = struct{}{}

	return explorer.ContentInfo{
		MID:      res.MID.String(),
		Size:     res.Size,
		Blocks:   res.Block,
		Sealed:   true,
		Present:  true,
		Name:     dirName,
		MimeType: "inode/directory",
	}, nil
}

// TrackRootWithMetadata writes root ObjectInfo metadata and registers it in allRoots.
func (a *explorerAdapter) TrackRootWithMetadata(m mid.MID, name string, mime string, size uint64) error {
	b := a.b
	if b == nil || b.store == nil {
		return errors.New("explorer: no backend")
	}
	_ = store.SetObjectInfo(b.store, m, store.ObjectInfo{
		Name:     name,
		MimeType: mime,
		Size:     size,
		IsRoot:   true,
	})
	a.allRoots[m.String()] = struct{}{}
	return nil
}

// --- Phase 17: MemFS methods on explorerAdapter ---

// MemFSInfo returns the metadata for a MemFS node.
func (a *explorerAdapter) MemFSInfo(ctx context.Context, m mid.MID) (explorer.MemFSInfo, error) {
	r := memfs.NewResolver(&fetchingBlockstore{
		Blockstore: a.b.store,
		b:          a.b,
		ctx:        ctx,
	})
	st, err := r.Stat(ctx, m)
	if err != nil {
		return explorer.MemFSInfo{}, err
	}
	return explorer.MemFSInfo{
		MID:   m.String(),
		Type:  memFSTypeString(st.Type),
		Size:  st.Size,
		Mode:  uint32(st.Mode),
		MTime: st.MTime.Unix(),
		Mime:  st.MimeType,
	}, nil
}

// MemFSList returns the entries of a MemFS directory.
func (a *explorerAdapter) MemFSList(ctx context.Context, m mid.MID) ([]explorer.MemFSEntry, error) {
	r := memfs.NewResolver(&fetchingBlockstore{
		Blockstore: a.b.store,
		b:          a.b,
		ctx:        ctx,
	})
	st, err := r.Stat(ctx, m)
	if err != nil {
		return nil, err
	}
	if st.Type != memfs.TypeDir {
		return nil, errors.New("not a directory")
	}
	out := make([]explorer.MemFSEntry, 0, len(st.Entries))
	for _, e := range st.Entries {
		out = append(out, explorer.MemFSEntry{
			Name: e.Name,
			MID:  e.Mid.String(),
			Type: memFSTypeString(e.Type),
			Size: e.Size,
		})
	}
	return out, nil
}

// MemFSPathGet returns a streaming reader for the file at
// m/path. Used by the explorer's preview pane.
func (a *explorerAdapter) MemFSPathGet(ctx context.Context, m mid.MID, path string) (io.ReadSeekCloser, uint64, string, error) {
	r := memfs.NewResolver(&fetchingBlockstore{
		Blockstore: a.b.store,
		b:          a.b,
		ctx:        ctx,
	})
	node, err := r.ResolvePath(ctx, m, path)
	if err != nil {
		return nil, 0, "", err
	}
	if !node.IsFile() {
		return nil, 0, "", errors.New("not a file")
	}
	rc, err := r.Open(ctx, node.MustMID())
	if err != nil {
		return nil, 0, "", err
	}
	return rc, node.TotalSize(), node.MimeType(), nil
}

// KeyringKeys lists the keyring keys.
func (a *explorerAdapter) KeyringKeys(ctx context.Context) ([]explorer.KeyringKeyInfo, error) {
	if a.keyring == nil {
		return nil, errors.New("keyring not configured")
	}
	keys, err := a.keyring.List()
	if err != nil {
		return nil, err
	}
	out := make([]explorer.KeyringKeyInfo, 0, len(keys))
	for _, k := range keys {
		kType := "ed25519"
		if key, err := a.keyring.Get(k.Name); err == nil {
			kType = strings.ToLower(key.PubKey.Type().String())
		}
		out = append(out, explorer.KeyringKeyInfo{
			Name:      k.Name,
			MemNSName: k.MemNSName,
			Type:      kType,
			CreatedAt: k.CreatedAt,
		})
	}
	return out, nil
}

// ResolveMemNSRecord resolves a MemNS record.
func (a *explorerAdapter) ResolveMemNSRecord(ctx context.Context, name string) (explorer.MemNSRecordInfo, error) {
	if a.memnsRes == nil {
		return explorer.MemNSRecordInfo{}, errors.New("memns resolver not configured")
	}

	cleanName := name
	if strings.HasPrefix(cleanName, "/memns/") {
		cleanName = cleanName[7:]
	}

	var rec *membusspb.MemNSRecord
	var err error

	// If we own the key, try loading the record locally first
	if a.keyring != nil {
		keys, _ := a.keyring.List()
		for _, k := range keys {
			kMemNS := k.MemNSName
			if strings.HasPrefix(kMemNS, "/memns/") {
				kMemNS = kMemNS[7:]
			}
			if kMemNS == cleanName {
				rec, err = a.keyring.LoadRecord(k.Name)
				break
			}
		}
	}

	// If not owned or not found locally, fetch from DHT
	if rec == nil {
		rec, err = memns.ResolveDHT(ctx, a.memnsRes.DHTClient(), cleanName)
		if err != nil {
			return explorer.MemNSRecordInfo{}, err
		}
	}

	// Map routes
	routes := make([]explorer.MemRouteInfo, 0, len(rec.Routes))
	for _, r := range rec.Routes {
		routes = append(routes, explorer.MemRouteInfo{
			Target: string(r.Target),
			Weight: r.Weight,
			Label:  r.Label,
		})
	}

	// Map delegates
	delegates := make([]string, 0, len(rec.Delegates))
	for _, d := range rec.Delegates {
		delegates = append(delegates, string(d))
	}

	// Map changelog
	changelog := make([]explorer.MemLogEntryInfo, 0)
	if rec.Changelog != nil {
		for _, e := range rec.Changelog.Entries {
			changelog = append(changelog, explorer.MemLogEntryInfo{
				Sequence:  e.Sequence,
				Value:     string(e.Value),
				Timestamp: time.Unix(0, e.Timestamp),
				Message:   e.Message,
			})
		}
	}

	return explorer.MemNSRecordInfo{
		Name:      "/memns/" + cleanName,
		Value:     string(rec.Value),
		Sequence:  rec.Sequence,
		ExpiresAt: time.Unix(0, rec.Validity),
		TTL:       time.Duration(rec.Ttl),
		Routes:    routes,
		Delegates: delegates,
		Changelog: changelog,
	}, nil
}

// ResolveMemLink resolves a MemLink domain and returns its resolution details.
func (a *explorerAdapter) ResolveMemLink(ctx context.Context, domain string) (explorer.MemLinkInfo, error) {
	if a.memnsRes == nil {
		return explorer.MemLinkInfo{}, errors.New("memns resolver not configured")
	}
	dnsResAPI := a.memnsRes.DNS()
	if dnsResAPI == nil {
		return explorer.MemLinkInfo{}, errors.New("dns resolver not configured")
	}

	dnsRes, ok := dnsResAPI.(*memlink.DNSResolver)
	if !ok {
		return explorer.MemLinkInfo{}, errors.New("unexpected DNS resolver type")
	}

	rawTXT, err := dnsRes.LookupTXTRecord(domain)
	if err != nil {
		return explorer.MemLinkInfo{}, fmt.Errorf("lookup txt record failed: %w", err)
	}

	parsed, err := memlink.ParseTXTRecord(rawTXT)
	if err != nil {
		return explorer.MemLinkInfo{}, fmt.Errorf("failed to parse TXT record: %w", err)
	}

	resolved, err := dnsRes.Resolve(ctx, domain)
	if err != nil {
		return explorer.MemLinkInfo{}, fmt.Errorf("dns resolve failed: %w", err)
	}

	ttl := 300
	if parsed.TTL > 0 {
		ttl = parsed.TTL
	}

	return explorer.MemLinkInfo{
		Domain:            domain,
		RawTXT:            rawTXT,
		ResolvedMemNSName: parsed.MemNSName,
		ResolvedMID:       resolved,
		TTLRemaining:      ttl,
	}, nil
}

// ConnectPeer parses a multiaddr and dials the peer.
func (a *explorerAdapter) ConnectPeer(ctx context.Context, multiaddr string) error {
	ai, err := peer.AddrInfoFromString(multiaddr)
	if err != nil {
		return fmt.Errorf("parse multiaddr: %w", err)
	}
	if a.b.host == nil {
		return errors.New("host not ready")
	}
	return a.b.host.Connect(ctx, *ai)
}

func (a *explorerAdapter) KeyringGenerate(ctx context.Context, name, keyType string) (explorer.KeyringKeyInfo, error) {
	if a.keyring == nil {
		return explorer.KeyringKeyInfo{}, errors.New("keyring not configured")
	}
	k, err := a.keyring.Generate(name, keyType)
	if err != nil {
		return explorer.KeyringKeyInfo{}, err
	}
	kType := "ed25519"
	if key, err := a.keyring.Get(k.Name); err == nil {
		kType = strings.ToLower(key.PubKey.Type().String())
	}
	return explorer.KeyringKeyInfo{
		Name:      k.Name,
		MemNSName: k.MemNSName,
		Type:      kType,
		CreatedAt: time.Now(),
	}, nil
}

func (a *explorerAdapter) KeyringDelete(ctx context.Context, name string) error {
	if a.keyring == nil {
		return errors.New("keyring not configured")
	}
	return a.keyring.Delete(name)
}

func (a *explorerAdapter) MemNSPublish(ctx context.Context, keyName, value string, ttl uint32, message string) (explorer.MemNSRecordInfo, error) {
	if a.keyring == nil || a.memnsRes == nil {
		return explorer.MemNSRecordInfo{}, errors.New("keyring or resolver not configured")
	}
	key, err := a.keyring.Get(keyName)
	if err != nil {
		return explorer.MemNSRecordInfo{}, fmt.Errorf("get key: %w", err)
	}

	cleanName := key.MemNSName
	if strings.HasPrefix(cleanName, "/memns/") {
		cleanName = cleanName[7:]
	}

	current, err := memns.ResolveDHT(ctx, a.memnsRes.DHTClient(), cleanName)
	seq := uint64(1)
	if err == nil && current != nil {
		seq = current.Sequence + 1
	}

	recTTL := uint64(ttl)
	if recTTL == 0 {
		recTTL = 86400
	}

	record, err := memns.BuildRecord(key, value, seq, time.Duration(recTTL)*time.Second, nil, message)
	if err != nil {
		return explorer.MemNSRecordInfo{}, err
	}

	err = memns.PublishDHT(ctx, a.memnsRes.DHTClient(), key, record)
	if err != nil {
		return explorer.MemNSRecordInfo{}, fmt.Errorf("publish dht: %w", err)
	}

	if a.memnsRes.PubSub() != nil {
		_ = a.memnsRes.PubSub().PublishPub(ctx, key, record)
	}

	_ = a.keyring.SaveRecord(keyName, record)

	return a.ResolveMemNSRecord(ctx, cleanName)
}

// --- Phase 21: Descriptor support ---

func (a *explorerAdapter) DescriptorExport(ctx context.Context, midStr string) ([]byte, error) {
	b := a.b
	if b == nil || b.store == nil {
		return nil, errors.New("explorer: no backend")
	}
	m, err := mid.Parse(midStr)
	if err != nil {
		return nil, fmt.Errorf("descriptor: parse mid: %w", err)
	}
	has, err := b.store.Has(m)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("descriptor: MID not found locally")
	}

	var opts []descriptor.Option
	if b.host != nil && b.dht != nil {
		opts = append(opts, descriptor.WithBootstrapPeers(a.getBootstrapPeers()))
	}

	d, err := descriptor.Build(b.store, m, opts...)
	if err != nil {
		return nil, err
	}
	return d.Serialize()
}

func (a *explorerAdapter) DescriptorMeta(ctx context.Context, midStr string) (map[string]interface{}, error) {
	b := a.b
	if b == nil || b.store == nil {
		return nil, errors.New("explorer: no backend")
	}
	m, err := mid.Parse(midStr)
	if err != nil {
		return nil, fmt.Errorf("descriptor: parse mid: %w", err)
	}
	has, err := b.store.Has(m)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("descriptor: MID not found locally")
	}

	d, err := descriptor.Build(b.store, m)
	if err != nil {
		return nil, err
	}

	meta := map[string]interface{}{
		"root_mid":    d.RootMID.String(),
		"total_size":  d.TotalSize,
		"block_count": d.BlockCount,
		"name":        d.Name,
		"mime_type":   d.MimeType,
		"created_at":  d.CreatedAt.Unix(),
		"chunker":     d.Chunker,
		"chunk_size":  d.ChunkSize,
	}
	if d.Erasure != nil {
		meta["erasure"] = map[string]interface{}{
			"data_shards":      d.Erasure.DataShards,
			"parity_shards":    d.Erasure.ParityShards,
			"shard_mids":       d.Erasure.ShardMIDs,
			"shards_available": len(d.Erasure.ShardMIDs),
		}
	}
	if len(d.BootstrapPeers) > 0 {
		meta["bootstrap_peers"] = d.BootstrapPeers
	}
	if d.MemNSName != "" {
		meta["memns_name"] = d.MemNSName
	}
	return meta, nil
}

func (a *explorerAdapter) DescriptorImport(ctx context.Context, data []byte) (string, error) {
	b := a.b
	if b == nil || b.store == nil {
		return "", errors.New("explorer: no backend")
	}
	d, err := descriptor.Parse(data)
	if err != nil {
		return "", err
	}

	missing, err := d.Verify(b.store)
	if err != nil {
		return "", fmt.Errorf("descriptor: verify: %w", err)
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("descriptor: %d blocks missing locally; fetch from peers first", len(missing))
	}

	return d.RootMID.String(), nil
}

func (a *explorerAdapter) getBootstrapPeers() []string {
	return nil
}
