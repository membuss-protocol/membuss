// Package memplace distributes erasure shards to their
// rendezvous-assigned peers after ingest. It is the single
// placement decision point: given a shard set and a ring, it
// computes which peers hold which shards, pushes shard payloads
// to owners that are not self, and announces the shard set on
// the DHT. Enabled only when config.shard_placement is true.
package memplace

import (
	"context"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/shard"
	"github.com/nnlgsakib/membuss/core/store"
)

// Pusher delivers blocks to a specific peer. Production impl is
// memex_v2.Engine.PushBlocksTo; tests inject a recorder.
type Pusher interface {
	PushBlocksTo(ctx context.Context, pid peer.ID, blocks []store.Block) error
}

// ShardSetAnnouncer announces that this node holds the shard set
// of a root. Production impl is MemDHT.ProvideShardSet.
type ShardSetAnnouncer interface {
	ProvideShardSet(ctx context.Context, root mid.MID) error
}

// Config controls placement behavior.
type Config struct {
	// Replicas is the replication factor per shard MID.
	Replicas int
}

// Placer pushes shards to their ring owners.
type Placer struct {
	cfg  Config
	ring *shard.HashRing
	self peer.ID
	push Pusher
	dht  ShardSetAnnouncer
	log  *slog.Logger
}

// New builds a Placer. Any nil dep disables the corresponding
// step (no push, no announce) instead of erroring, so partially
// wired daemons still ingest cleanly.
func New(cfg Config, ring *shard.HashRing, self peer.ID, push Pusher, dht ShardSetAnnouncer, log *slog.Logger) *Placer {
	if log == nil {
		log = slog.Default()
	}
	return &Placer{cfg: cfg, ring: ring, self: self, push: push, dht: dht, log: log}
}

// Distribution is the per-peer block plan for one shard set.
type Distribution map[peer.ID][]store.Block

// shardDistribution computes which peers should hold which
// shards. It never includes self: those shards already live in
// the local store. Pure function of ring + shard list.
func shardDistribution(ring *shard.HashRing, self peer.ID, replicas int, shards []store.Block) Distribution {
	dist := make(Distribution)
	if ring == nil || len(shards) == 0 {
		return dist
	}
	for _, blk := range shards {
		owners, err := ring.Assign(blk.MID, replicas)
		if err != nil {
			continue
		}
		for _, pidStr := range owners {
			pid := peer.ID(pidStr)
			if pid == self {
				continue
			}
			dist[pid] = append(dist[pid], blk)
		}
	}
	return dist
}

// PlaceShards pushes each shard to its remote owners, then
// announces the shard set. Best-effort: one peer failure never
// aborts the rest. Returns the number of remote shards pushed.
func (p *Placer) PlaceShards(ctx context.Context, root mid.MID, shards []store.Block) (int, error) {
	if p == nil || p.ring == nil {
		return 0, nil
	}
	dist := shardDistribution(p.ring, p.self, p.cfg.Replicas, shards)
	pushed := 0
	for pid, blocks := range dist {
		if p.push == nil {
			break
		}
		if err := p.push.PushBlocksTo(ctx, pid, blocks); err != nil {
			p.log.Warn("placement push failed", "peer", pid.String(), "blocks", len(blocks), "err", err.Error())
			continue
		}
		pushed += len(blocks)
	}
	if p.dht != nil {
		if err := p.dht.ProvideShardSet(ctx, root); err != nil {
			p.log.Warn("shard-set announce failed", "root", root.String(), "err", err.Error())
		}
	}
	return pushed, nil
}
