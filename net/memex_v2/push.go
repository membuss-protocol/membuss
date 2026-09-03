package memex_v2

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/store"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// PushBlocksTo delivers specific blocks to a specific peer as an unsolicited
// Blocks frame over a pooled stream (same stream/frame mechanics as
// OpportunisticPushBlock: GetOrCreateStream + writeFrameLocked). It is the
// targeted shard-delivery primitive for the placement engine to move shards
// onto designated peers.
//
// The frame carries exactly the given blocks; ctx governs stream opening
// (checked upfront, then passed to GetOrCreateStream). An empty block list is
// a no-op returning nil.
func (e *Engine) PushBlocksTo(ctx context.Context, pid peer.ID, blocks []store.Block) error {
	if e == nil || e.streamPool == nil || e.ctx == nil {
		return fmt.Errorf("memex_v2: PushBlocksTo on unstarted engine")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("memex_v2: PushBlocksTo %s: %w", pid, err)
	}
	if len(blocks) == 0 {
		return nil
	}

	msg := &membusspb.MemexMessage{}
	for _, b := range blocks {
		msg.Blocks = append(msg.Blocks, &membusspb.Block{
			Mid:  b.MID.String(),
			Data: b.Data,
			Size: uint64(len(b.Data)),
		})
	}
	if len(msg.Blocks) == 0 {
		return nil
	}

	ps, err := e.streamPool.GetOrCreateStream(ctx, pid)
	if err != nil {
		return fmt.Errorf("memex_v2: PushBlocksTo %s: open stream: %w", pid, err)
	}
	if err := ps.writeFrameLocked(msg); err != nil {
		return fmt.Errorf("memex_v2: PushBlocksTo %s: write frame: %w", pid, err)
	}
	return nil
}
