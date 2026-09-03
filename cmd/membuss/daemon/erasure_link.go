package daemon

import (
	"errors"

	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// maxErasureLinkWalk bounds the per-root DAG traversal of the
// post-ingest linkage pass.
const maxErasureLinkWalk = 10000

// errLinkCapReached stops the linkage walk once the cap is hit.
var errLinkCapReached = errors.New("erasure link: walk cap reached")

// linkLeavesToRoot walks the DAG rooted at root and records, for
// every CodecRaw leaf carrying a local erasure manifest, the
// metadata row erasure_root/<leaf> -> root. Manifests attach to
// leaf MIDs but shard-set discovery keys on the root, so these rows
// are what let a manifest server hand fetchers the right shard-set
// key. Best-effort: a failed row write is skipped, the walk cap is
// not an error. Returns the number of linkage rows written.
func linkLeavesToRoot(s store.Store, root mid.MID) int {
	if s == nil || root.IsZero() {
		return 0
	}
	linked := 0
	visited := 0
	_ = store.Walk(s, root, func(m mid.MID, leaf bool) error {
		visited++
		// ponytail: hard cap keeps ingest bounded on huge DAGs;
		// paginate or stream if real roots exceed 10k nodes.
		if visited > maxErasureLinkWalk {
			return errLinkCapReached
		}
		if !leaf || m.Codec() != mid.CodecRaw {
			return nil
		}
		mf, err := erasure.GetManifest(s, m)
		if err != nil || mf == nil {
			return nil
		}
		if err := erasure.SetManifestRoot(s, m, root); err == nil {
			linked++
		}
		return nil
	})
	return linked
}

// collectShardBlocks gathers the erasure shard blocks of every
// manifest-bearing leaf under root, for the placement engine to
// push to ring owners. Missing shard blocks are skipped: shards
// that already left this node are not placement candidates.
func collectShardBlocks(s store.Store, root mid.MID) []store.Block {
	if s == nil || root.IsZero() {
		return nil
	}
	var blocks []store.Block
	visited := 0
	_ = store.Walk(s, root, func(m mid.MID, leaf bool) error {
		visited++
		if visited > maxErasureLinkWalk {
			return errLinkCapReached
		}
		if !leaf || m.Codec() != mid.CodecRaw {
			return nil
		}
		mf, err := erasure.GetManifest(s, m)
		if err != nil || mf == nil {
			return nil
		}
		for _, shardMIDStr := range mf.ShardMids {
			shardMID, perr := mid.Parse(shardMIDStr)
			if perr != nil {
				continue
			}
			data, gerr := s.Get(shardMID)
			if gerr != nil {
				continue
			}
			blocks = append(blocks, store.Block{MID: shardMID, Data: data})
		}
		return nil
	})
	return blocks
}
