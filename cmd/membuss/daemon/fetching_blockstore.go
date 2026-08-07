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
	dataNet := f.fetchBlockFromNetwork(m)
	if len(dataNet) > 0 {
		return dataNet, nil
	}

	// Direct block fetch failed. Try Reed-Solomon Erasure Coding Reconstruction.
	manifest, _ := erasure.GetManifest(f.Blockstore, m)
	if manifest != nil {
		dataShards := int(manifest.DataShards)
		parityShards := int(manifest.ParityShards)
		totalShards := dataShards + parityShards

		if len(manifest.ShardMids) == totalShards {
			shards := make([][]byte, totalShards)
			presentCount := 0

			for i, smStr := range manifest.ShardMids {
				sm, perr := mid.Parse(smStr)
				if perr != nil {
					continue
				}
				sData, sErr := f.Blockstore.Get(sm)
				if sErr != nil {
					sData = f.fetchBlockFromNetwork(sm)
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

func (f *fetchingBlockstore) fetchBlockFromNetwork(m mid.MID) []byte {
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
