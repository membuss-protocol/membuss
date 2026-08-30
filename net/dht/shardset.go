package dht

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"

	"github.com/nnlgsakib/membuss/core/mid"
)

// ShardSetNamespace is the logical namespace under which
// erasure shard-holder announcements for a root live.
const ShardSetNamespace = "/membuss/shardset/1"

// shardSetKey derives the DHT key on which every holder of
// erasure shards of root announces itself. The derivation is
// domain-separated by ShardSetNamespace and pinned to SHA2-256
// (not mid.FromBytes, whose algorithm follows mutable global
// config) so all nodes agree on the key for a given root.
//
// Announcements are DHT provider records on this synthetic MID:
// provider records natively support many providers per key and
// carry full AddrInfo, so multiple holders of one root coexist.
func shardSetKey(root mid.MID) mid.MID {
	buf := make([]byte, 0, len(ShardSetNamespace)+len(root.Hash))
	buf = append(buf, ShardSetNamespace...)
	buf = append(buf, root.Hash...)
	sum := sha256.Sum256(buf)
	key, err := mid.FromBytesWithHash(sum[:], multihash.SHA2_256)
	if err != nil {
		// Unreachable: SHA2-256 over 32 bytes is always a valid
		// supported multihash.
		return mid.MID{}
	}
	return key
}

// ProvideShardSet announces to the DHT that this node holds
// erasure shards of root, so that any peer reconstructing a
// missing block of root can discover it as a shard source.
func (m *MemDHT) ProvideShardSet(ctx context.Context, root mid.MID) error {
	if m == nil || m.dht == nil {
		return errors.New("dht: nil")
	}
	if root.IsZero() {
		return errors.New("dht: zero MID")
	}
	c := midToCID(shardSetKey(root))
	if !c.Defined() {
		return errors.New("dht: zero MID")
	}
	return m.dht.Provide(ctx, c, true)
}

// FindShardSets returns the peers that announced themselves as
// holders of erasure shards of root. Results may include this
// node itself; callers skip self as they already do for
// manifest fetches.
func (m *MemDHT) FindShardSets(ctx context.Context, root mid.MID) ([]peer.AddrInfo, error) {
	if m == nil || m.dht == nil {
		return nil, errors.New("dht: nil")
	}
	if root.IsZero() {
		return nil, errors.New("dht: zero MID")
	}
	c := midToCID(shardSetKey(root))
	if !c.Defined() {
		return nil, errors.New("dht: zero MID")
	}
	return m.dht.FindProviders(ctx, c)
}
