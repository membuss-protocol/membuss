package memex_v2

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

type PeerWantState struct {
	MID       mid.MID
	WantType  membusspb.WantType
	Priority  int32
	UpdatedAt time.Time
}

type PeerWantlistManager struct {
	mu    sync.RWMutex
	wants map[peer.ID]map[string]*PeerWantState
}

func newPeerWantlistManager() *PeerWantlistManager {
	return &PeerWantlistManager{
		wants: make(map[peer.ID]map[string]*PeerWantState),
	}
}

// UpdatePeerWantlist applies incoming want entries or cancel requests from a remote peer.
func (p *PeerWantlistManager) UpdatePeerWantlist(pid peer.ID, entries []*membusspb.WantEntry, cancels []string) {
	if pid == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	peerMap, exists := p.wants[pid]
	if !exists {
		peerMap = make(map[string]*PeerWantState)
		p.wants[pid] = peerMap
	}

	now := time.Now()

	for _, cMid := range cancels {
		delete(peerMap, cMid)
	}

	for _, entry := range entries {
		if entry == nil || entry.Mid == "" {
			continue
		}
		if entry.Cancel {
			delete(peerMap, entry.Mid)
			continue
		}
		m, err := mid.Parse(entry.Mid)
		if err != nil {
			continue
		}
		peerMap[entry.Mid] = &PeerWantState{
			MID:       m,
			WantType:  entry.WantType,
			Priority:  entry.Priority,
			UpdatedAt: now,
		}
	}

	if len(peerMap) == 0 {
		delete(p.wants, pid)
	}
}

// RemovePeer removes tracking state when a stream or peer disconnects.
func (p *PeerWantlistManager) RemovePeer(pid peer.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.wants, pid)
}

// GetPeersWantingBlock returns all peer IDs that have an active WANT_BLOCK entry for the given MID.
func (p *PeerWantlistManager) GetPeersWantingBlock(m mid.MID) []peer.ID {
	p.mu.RLock()
	defer p.mu.RUnlock()

	midStr := m.String()
	var peers []peer.ID
	for pid, peerMap := range p.wants {
		if state, ok := peerMap[midStr]; ok {
			if state.WantType == membusspb.WantType_WANT_BLOCK {
				peers = append(peers, pid)
			}
		}
	}
	return peers
}

// GetPeersWanting returns peer lists separated into those wanting WANT_BLOCK and WANT_HAVE for a given MID.
func (p *PeerWantlistManager) GetPeersWanting(m mid.MID) ([]peer.ID, []peer.ID) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	midStr := m.String()
	var blockPeers []peer.ID
	var havePeers []peer.ID
	for pid, peerMap := range p.wants {
		if state, ok := peerMap[midStr]; ok {
			if state.WantType == membusspb.WantType_WANT_BLOCK {
				blockPeers = append(blockPeers, pid)
			} else {
				havePeers = append(havePeers, pid)
			}
		}
	}
	return blockPeers, havePeers
}

// HasPeerWant checks if a specific peer has an active want for a given MID.
func (p *PeerWantlistManager) HasPeerWant(pid peer.ID, m mid.MID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	peerMap, exists := p.wants[pid]
	if !exists {
		return false
	}
	_, ok := peerMap[m.String()]
	return ok
}
