package anchor

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestReputation_ScoreCalculation(t *testing.T) {
	rep := NewPeerReputationTracker()
	p1, _ := peer.Decode("12D3KooWPjceQrSwdWXPyLLeABRXmuqt69Rg3sBYbU1Nft9HyQ6X")
	p2, _ := peer.Decode("12D3KooWGA3sBYbU1Nft9HyQ6XjceQrSwdWXPyLLeABRXmuqt69R")

	// Fast & reliable peer p1: 10ms latency, 100% success
	rep.RecordAttempt(p1, true, 10*time.Millisecond)
	rep.RecordAttempt(p1, true, 10*time.Millisecond)

	// Slow & flaky peer p2: 500ms latency, failures
	rep.RecordAttempt(p2, false, 500*time.Millisecond)
	rep.RecordAttempt(p2, true, 500*time.Millisecond)

	score1 := rep.GetScore(p1)
	score2 := rep.GetScore(p2)

	if score1 <= score2 {
		t.Fatalf("expected fast reliable peer p1 score (%.2f) > slow flaky peer p2 score (%.2f)", score1, score2)
	}
}

func TestReputation_SortPeers(t *testing.T) {
	rep := NewPeerReputationTracker()
	p1, _ := peer.Decode("12D3KooWPjceQrSwdWXPyLLeABRXmuqt69Rg3sBYbU1Nft9HyQ6X")
	p2, _ := peer.Decode("12D3KooWGA3sBYbU1Nft9HyQ6XjceQrSwdWXPyLLeABRXmuqt69R")

	rep.RecordAttempt(p1, true, 5*time.Millisecond)
	rep.RecordAttempt(p2, false, 2000*time.Millisecond)

	peers := []peer.AddrInfo{
		{ID: p2}, // Flaky peer at index 0
		{ID: p1}, // Fast peer at index 1
	}

	sorted := rep.SortPeers(peers)

	if sorted[0].ID != p1 {
		t.Fatalf("expected fast peer p1 to be sorted first, got %s", sorted[0].ID)
	}
}
