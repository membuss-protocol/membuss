// Package shard distributes MIDs to peer IDs using rendezvous
// (highest-random-weight / HRW) hashing.
//
// The mapping MID -> [peer1, peer2, ...] is deterministic and
// depends only on the set of peers and the MID. Adding or
// removing a peer only remaps a fraction of MIDs: with N peers,
// the expected number of MIDs reassigned when a single peer is
// removed is 1/N of the total.
//
// Rendezvous hashing is preferred over a hash ring here because
// it has O(N) cost per assignment (no virtual nodes), produces
// the optimal minimum-disruption property, and is trivial to
// implement correctly.
package shard

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"sync"

	"github.com/nnlgsakib/membuss/core/mid"
)

// MinReplicas is the smallest replica count accepted by Assign.
const MinReplicas = 1

// MaxReplicas is the largest replica count accepted by Assign.
// It is bounded because the assignment cost is O(N * replicas),
// and we want to keep it predictable.
const MaxReplicas = 64

// HashRing maps MIDs to peer IDs using rendezvous hashing.
// It is safe for concurrent use after construction.
type HashRing struct {
	mu      sync.RWMutex
	peers   []string
	domains map[string]string // peerID -> domain/subnet tag
}

// NewHashRing returns an empty HashRing. Peers are added with
// AddPeer or AddPeerWithDomain.
func NewHashRing() *HashRing {
	return &HashRing{
		domains: make(map[string]string),
	}
}

// AddPeer registers a peer ID with default domain. Duplicate peer IDs are silently
// de-duplicated so a peer that joins twice does not skew the
// assignment distribution.
func (r *HashRing) AddPeer(peerID string) {
	r.AddPeerWithDomain(peerID, "")
}

// AddPeerWithDomain registers a peer ID with a failure domain tag (e.g. subnet, rack, or region).
func (r *HashRing) AddPeerWithDomain(peerID string, domain string) {
	if peerID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.domains == nil {
		r.domains = make(map[string]string)
	}
	r.domains[peerID] = domain

	for _, p := range r.peers {
		if p == peerID {
			return
		}
	}
	r.peers = append(r.peers, peerID)
}

// RemovePeer drops a peer ID. Missing peers are not an error.
func (r *HashRing) RemovePeer(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.domains, peerID)
	for i, p := range r.peers {
		if p == peerID {
			r.peers = append(r.peers[:i], r.peers[i+1:]...)
			return
		}
	}
}

// Peers returns a copy of the current peer set, in arbitrary
// order. Callers MUST NOT mutate the returned slice.
func (r *HashRing) Peers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.peers))
	copy(out, r.peers)
	return out
}

// Len returns the number of registered peers.
func (r *HashRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers)
}

// IsOwner returns true if peerID is assigned as one of the replica peers for MID m.
func (r *HashRing) IsOwner(peerID string, m mid.MID, replicas int) bool {
	if peerID == "" || m.IsZero() {
		return false
	}
	assigned, err := r.Assign(m, replicas)
	if err != nil {
		return false
	}
	for _, p := range assigned {
		if p == peerID {
			return true
		}
	}
	return false
}

// Assign returns the top-replicas peer IDs responsible for
// storing the given MID, ordered by score (highest first).
//
// If peers have failure domains assigned, Assign prioritizes selecting
// distinct failure domains across replicas to survive localized node failures.
func (r *HashRing) Assign(m mid.MID, replicas int) ([]string, error) {
	if m.IsZero() {
		return nil, errors.New("shard: zero MID")
	}
	if replicas < MinReplicas {
		return nil, errors.New("shard: replicas below minimum")
	}
	if replicas > MaxReplicas {
		return nil, errors.New("shard: replicas above maximum")
	}

	r.mu.RLock()
	peers := make([]string, len(r.peers))
	copy(peers, r.peers)
	domains := make(map[string]string, len(r.domains))
	for k, v := range r.domains {
		domains[k] = v
	}
	r.mu.RUnlock()

	if len(peers) == 0 {
		return nil, errors.New("shard: no peers in ring")
	}

	type scored struct {
		peer   string
		domain string
		score  uint64
	}
	scores := make([]scored, len(peers))
	midBytes := []byte(m.String())
	for i, p := range peers {
		scores[i] = scored{
			peer:   p,
			domain: domains[p],
			score:  hrwScore(p, midBytes),
		}
	}
	sort.Slice(scores, func(a, b int) bool {
		if scores[a].score != scores[b].score {
			return scores[a].score > scores[b].score
		}
		return scores[a].peer < scores[b].peer
	})

	if replicas > len(scores) {
		replicas = len(scores)
	}

	// Domain-aware selection: select top scores, preferring distinct domains
	selected := make([]string, 0, replicas)
	usedDomains := make(map[string]bool)
	deferred := make([]scored, 0, len(scores))

	for _, sc := range scores {
		if len(selected) >= replicas {
			break
		}
		if sc.domain != "" && usedDomains[sc.domain] {
			deferred = append(deferred, sc)
			continue
		}
		selected = append(selected, sc.peer)
		if sc.domain != "" {
			usedDomains[sc.domain] = true
		}
	}

	// Fill remaining slots from deferred scores if distinct domain selection didn't fill all replicas
	for _, sc := range deferred {
		if len(selected) >= replicas {
			break
		}
		selected = append(selected, sc.peer)
	}

	return selected, nil
}

// ComputeMigration computes which MIDs move onto or off the local peer when ring membership changes from r to newRing.
func (r *HashRing) ComputeMigration(newRing *HashRing, localPeerID string, mids []mid.MID, replicas int) (gained []mid.MID, lost []mid.MID) {
	if localPeerID == "" || newRing == nil {
		return nil, nil
	}
	for _, m := range mids {
		wasOwner := r != nil && r.IsOwner(localPeerID, m, replicas)
		isOwner := newRing.IsOwner(localPeerID, m, replicas)

		if !wasOwner && isOwner {
			gained = append(gained, m)
		} else if wasOwner && !isOwner {
			lost = append(lost, m)
		}
	}
	return gained, lost
}

// hrwScore computes a 64-bit rendezvous score for a (peer, mid)
// pair. SHA-256 of "peer || mid" is taken and the first 8
// bytes are interpreted as a big-endian uint64. This is the
// canonical "highest random weight" formulation.
func hrwScore(peer string, midBytes []byte) uint64 {
	h := sha256.New()
	h.Write([]byte(peer))
	h.Write([]byte{0x00}) // domain separator
	h.Write(midBytes)
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}
