package anchor

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PeerStats tracks moving average latency, success/failure counts, and reputation score for a peer.
type PeerStats struct {
	LatencyMs    float64   `json:"latency_ms"`
	SuccessCount int64     `json:"success_count"`
	FailCount    int64     `json:"fail_count"`
	LastSeen     time.Time `json:"last_seen"`
	Score        float64   `json:"score"`
}

// PeerReputationTracker maintains thread-safe reputation metrics per peer ID.
type PeerReputationTracker struct {
	mu    sync.RWMutex
	stats map[peer.ID]*PeerStats
}

// NewPeerReputationTracker creates a fresh PeerReputationTracker.
func NewPeerReputationTracker() *PeerReputationTracker {
	return &PeerReputationTracker{
		stats: make(map[peer.ID]*PeerStats),
	}
}

// RecordAttempt records a probe or fetch outcome and latency for pid.
func (r *PeerReputationTracker) RecordAttempt(pid peer.ID, success bool, latency time.Duration) {
	if r == nil || pid == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	ps, ok := r.stats[pid]
	if !ok {
		ps = &PeerStats{
			LatencyMs: float64(latency.Milliseconds()),
			LastSeen:  time.Now(),
		}
		r.stats[pid] = ps
	}

	ps.LastSeen = time.Now()
	latMs := float64(latency.Milliseconds())

	if success {
		ps.SuccessCount++
		// Exponential Moving Average (alpha = 0.2)
		if ps.LatencyMs <= 0 {
			ps.LatencyMs = latMs
		} else {
			ps.LatencyMs = 0.8*ps.LatencyMs + 0.2*latMs
		}
	} else {
		ps.FailCount++
		// Penalty latency increase on failure
		ps.LatencyMs = math.Max(ps.LatencyMs*1.5, 1000.0)
	}

	ps.Score = r.computeScore(ps)
}

func (r *PeerReputationTracker) computeScore(ps *PeerStats) float64 {
	total := ps.SuccessCount + ps.FailCount
	if total == 0 {
		return 50.0 // Neutral default score
	}
	successRatio := float64(ps.SuccessCount) / float64(total)

	// Base score 0 to 100 based on success ratio
	score := successRatio * 100.0

	// Deduct up to 40 points for high latency (> 2000ms max penalty)
	latPenalty := math.Min(ps.LatencyMs/50.0, 40.0)
	score -= latPenalty

	if score < 0 {
		score = 0
	}
	return score
}

// GetScore returns the current reputation score for a peer (0.0 to 100.0).
func (r *PeerReputationTracker) GetScore(pid peer.ID) float64 {
	if r == nil {
		return 50.0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if ps, ok := r.stats[pid]; ok {
		return ps.Score
	}
	return 50.0 // Default neutral score
}

// SortPeers sorts a slice of AddrInfo in descending order of peer reputation score.
func (r *PeerReputationTracker) SortPeers(peers []peer.AddrInfo) []peer.AddrInfo {
	if r == nil || len(peers) <= 1 {
		return peers
	}
	out := make([]peer.AddrInfo, len(peers))
	copy(out, peers)

	r.mu.RLock()
	defer r.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool {
		scoreI := 50.0
		if psI, ok := r.stats[out[i].ID]; ok {
			scoreI = psI.Score
		}
		scoreJ := 50.0
		if psJ, ok := r.stats[out[j].ID]; ok {
			scoreJ = psJ.Score
		}
		return scoreI > scoreJ
	})
	return out
}
