package daemon

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/net/manifest_rpc"
	memex "github.com/nnlgsakib/membuss/net/memex_v2"
)

var _ store.Blockstore = (*fetchingBlockstore)(nil)

// fetchingBlockstore wraps a store.Blockstore to intercept Get calls.
// If a block is not found locally, it queries the DHT and retrieves it via Memex.
type fetchingBlockstore struct {
	store.Blockstore
	b   *daemonBackend
	ctx context.Context
}

// Get retrieves a block by MID. If it's missing locally, it fetches it from the network.
func (f *fetchingBlockstore) Get(m mid.MID) ([]byte, error) {
	data, err := f.Blockstore.Get(m)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if f.b == nil || f.b.memex == nil || f.b.dht == nil {
		return nil, store.ErrNotFound
	}

	// Not found locally. Try fetching direct block from network.
	dataNet := f.fetchBlockFromNetwork(m, nil)
	if len(dataNet) > 0 {
		return dataNet, nil
	}

	// Direct block fetch failed. Try Reed-Solomon Erasure Coding Reconstruction.
	// Manifests live only in the origin node's meta keyspace, so a remote
	// node asks its peers for the manifest before giving up on reconstruction.
	manifest, _ := erasure.GetManifest(f.Blockstore, m)
	rootHint := ""
	if manifest == nil && f.b.host != nil {
		res := f.fetchManifestFromPeers(m)
		manifest = res.Manifest
		rootHint = res.RootMid
		if manifest != nil {
			// Persist so subsequent Gets skip the peer round trip.
			_ = erasure.SetManifest(f.Blockstore, m, manifest)
		}
	}
	if rootHint == "" {
		// Fall back to this node's own ingest-time linkage rows.
		if lr, lerr := erasure.GetManifestRoot(f.Blockstore, m); lerr == nil && !lr.IsZero() {
			rootHint = lr.String()
		}
	}

	// Shard holders announce under the root's shard-set key. Raw
	// shard blocks are never announced individually, so these peers
	// are the only DHT-visible sources for reconstruction.
	var holders []peer.AddrInfo
	if f.b.dht != nil && len(rootHint) > 0 {
		if rm, perr := mid.Parse(rootHint); perr == nil {
			hctx, hcancel := context.WithTimeout(f.ctx, 3*time.Second)
			holders, _ = f.b.dht.FindShardSets(hctx, rm)
			hcancel()
		}
	}

	if manifest != nil {
		dataShards := int(manifest.DataShards)
		parityShards := int(manifest.ParityShards)
		totalShards := dataShards + parityShards

		if len(manifest.ShardMids) == totalShards {
			shards := make([][]byte, totalShards)
			presentCount := 0

			// Stop-at-k: any DataShards valid shards reconstruct; parity
			// is only needed when data shards are missing. Fetching all n
			// transfers 1+k/n times more bytes than necessary.
			for i, smStr := range manifest.ShardMids {
				if presentCount >= dataShards {
					break
				}
				sm, perr := mid.Parse(smStr)
				if perr != nil {
					continue
				}
				sData, sErr := f.Blockstore.Get(sm)
				if sErr != nil {
					sData = f.fetchBlockFromNetwork(sm, holders)
				}
				if len(sData) > 0 && erasure.VerifyShard(sData, smStr) {
					shards[i] = sData
					presentCount++
				}
			}

			if presentCount >= dataShards {
				cfg, cerr := erasure.NewConfig(dataShards, parityShards)
				if cerr == nil {
					enc, eerr := erasure.NewEncoder(cfg)
					if eerr == nil {
						reconstructed, decErr := enc.Decode(shards, manifest)
						if decErr == nil && len(reconstructed) > 0 {
							_ = f.Blockstore.Put(m, reconstructed)
							return reconstructed, nil
						}
					}
				}
			}
		}
	}

	return nil, store.ErrNotFound
}

// fetchManifestFromPeers collects candidate peers (DHT providers of the
// root block plus every currently connected peer) and asks them for the
// erasure manifest of m. Returns a zero result when no candidate holds it.
func (f *fetchingBlockstore) fetchManifestFromPeers(m mid.MID) manifest_rpc.ManifestResult {
	if f.b == nil || f.b.host == nil {
		return manifest_rpc.ManifestResult{}
	}

	// Overall cap so a long candidate list cannot stall Get indefinitely;
	// each peer gets its own bounded slice of it.
	ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
	defer cancel()

	seen := make(map[peer.ID]struct{})
	var candidates []peer.ID
	if f.b.dht != nil {
		provCtx, provCancel := context.WithTimeout(ctx, 2*time.Second)
		provs, err := f.b.dht.FindProviders(provCtx, m)
		provCancel()
		if err == nil {
			for _, pi := range provs {
				if _, dup := seen[pi.ID]; !dup {
					seen[pi.ID] = struct{}{}
					candidates = append(candidates, pi.ID)
				}
			}
		}
	}
	for _, pid := range f.b.host.Network().Peers() {
		if _, dup := seen[pid]; !dup {
			seen[pid] = struct{}{}
			candidates = append(candidates, pid)
		}
	}

	return manifest_rpc.FetchManifestFromPeers(ctx, f.b.host, candidates, m, 2*time.Second)
}

// fetchBlockFromNetwork pulls a missing block over Memex. extra are
// additional provider candidates (e.g. shard-set holders discovered via
// the DHT) tried alongside the usual DHT/connected-peer sources.
func (f *fetchingBlockstore) fetchBlockFromNetwork(m mid.MID, extra []peer.AddrInfo) []byte {
	if f.b == nil || f.b.memex == nil || f.b.dht == nil {
		return nil
	}
	provCtx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	provs, perr := f.b.dht.FindProviders(provCtx, m)
	cancel()
	if perr != nil || len(provs) == 0 {
		if f.b.host != nil {
			for _, pid := range f.b.host.Network().Peers() {
				provs = append(provs, f.b.host.Peerstore().PeerInfo(pid))
			}
		}
	}
	for _, ai := range extra {
		if f.b.host == nil || ai.ID == "" || ai.ID == f.b.host.ID() {
			continue
		}
		provs = append(provs, ai)
	}
	if len(provs) == 0 {
		return nil
	}

	var finder func(ctx context.Context, m mid.MID) ([]peer.AddrInfo, error)
	if f.b.dht != nil {
		finder = f.b.dht.FindProviders
	}
	sess, serr := memex.NewSession(memex.SessionConfig{
		Engine:         f.b.memex,
		Root:           m,
		Providers:      provs,
		Timeout:        memex.DefaultSessionTimeout,
		ProviderFinder: finder,
	})
	if serr != nil {
		return nil
	}

	rc, ferr := sess.FetchWithBackoff(f.ctx, memex.DefaultRetryConfig())
	if ferr != nil {
		return nil
	}

	if rc != nil {
		_, _ = io.Copy(io.Discard, rc)
		if c, ok := rc.(io.Closer); ok {
			_ = c.Close()
		}
	}

	data, err := f.Blockstore.Get(m)
	if err == nil {
		return data
	}
	return nil
}
