package memex_v2

import (
	"context"
	"log"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// BroadcastCancel broadcasts a Cancel frame to all active streams when MIDs resolve locally.
func (e *Engine) BroadcastCancel(mids ...mid.MID) {
	if len(mids) == 0 {
		return
	}
	cancelStrs := make([]string, 0, len(mids))
	for _, m := range mids {
		if !m.IsZero() {
			cancelStrs = append(cancelStrs, m.String())
		}
	}
	if len(cancelStrs) == 0 {
		return
	}

	msg := &membusspb.MemexMessage{
		Cancel: cancelStrs,
	}

	e.streamPool.Broadcast(msg)
}

// BroadcastWants broadcasts want entries (e.g. WANT_HAVE or WANT_BLOCK) to connected peers.
func (e *Engine) BroadcastWants(wants []*membusspb.WantEntry) {
	if len(wants) == 0 {
		return
	}
	msg := &membusspb.MemexMessage{
		Wants: wants,
	}
	e.streamPool.Broadcast(msg)
}

// OpportunisticPushBlock inspects connected peer wantlists and immediately streams
// freshly stored blocks to peers requesting WANT_BLOCK for that MID.
func (e *Engine) OpportunisticPushBlock(id mid.MID, data []byte) {
	if e.peerWantlist == nil {
		return
	}
	wantingPeers := e.peerWantlist.GetPeersWantingBlock(id)
	if len(wantingPeers) == 0 {
		return
	}

	blockMsg := &membusspb.MemexMessage{
		Blocks: []*membusspb.Block{
			{
				Mid:  id.String(),
				Data: data,
				Size: uint64(len(data)),
			},
		},
	}

	ctx := context.Background()
	for _, pid := range wantingPeers {
		ps, err := e.streamPool.GetOrCreateStream(ctx, pid)
		if err != nil {
			continue
		}
		if err := ps.writeFrameLocked(blockMsg); err != nil {
			log.Printf("memex_v2 opportunistic push to peer %s failed: %v", pid, err)
		}
	}
}

// HandleRemoteWants evaluates incoming want lists and cancel signals from remote peers.
func (e *Engine) HandleRemoteWants(from peer.ID, wants []*membusspb.WantEntry, cancels []string) *membusspb.MemexMessage {
	if e.peerWantlist != nil {
		e.peerWantlist.UpdatePeerWantlist(from, wants, cancels)
	}

	resp := &membusspb.MemexMessage{}

	for _, w := range wants {
		if w == nil || w.Mid == "" || w.Cancel {
			continue
		}
		id, err := mid.Parse(w.Mid)
		if err != nil {
			continue
		}
		has, err := e.bs.Has(id)
		if err != nil || !has {
			if w.SendDontHave {
				resp.DontHaves = append(resp.DontHaves, w.Mid)
			}
			continue
		}

		// Local blockstore HAS this block
		if w.WantType == membusspb.WantType_WANT_HAVE {
			// Lightweight positive ACK (HAVE response)
			resp.HaveMids = append(resp.HaveMids, w.Mid)
		} else {
			// WANT_BLOCK: deliver block data payload
			data, err := e.bs.Get(id)
			if err != nil {
				continue
			}
			resp.Blocks = append(resp.Blocks, &membusspb.Block{
				Mid:  w.Mid,
				Data: data,
				Size: uint64(len(data)),
			})
			if oi, ok := e.objectInfoFor(id); ok {
				if resp.ObjectInfos == nil {
					resp.ObjectInfos = make(map[string]*membusspb.ObjectInfo)
				}
				resp.ObjectInfos[w.Mid] = oi
			}
		}
	}

	return resp
}
