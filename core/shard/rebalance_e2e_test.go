package shard_test

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/shard"
)

func TestConsistentPlacementAndRebalanceE2E(t *testing.T) {
	// 1. Create a 5-node ring
	ring1 := shard.NewHashRing()
	peers := []string{"peer-A", "peer-B", "peer-C", "peer-D", "peer-E"}
	for _, p := range peers {
		ring1.AddPeer(p)
	}

	// Generate 100 sample MIDs
	mids := make([]mid.MID, 100)
	for i := 0; i < 100; i++ {
		payload := fmt.Sprintf("content-payload-%d-%d", i, rand.Intn(100000))
		mids[i] = mid.FromBytes([]byte(payload))
	}

	// Verify deterministic assignment across 2 separate ring instances with identical peers
	ring2 := shard.NewHashRing()
	for _, p := range peers {
		ring2.AddPeer(p)
	}

	for _, m := range mids {
		ass1, err1 := ring1.Assign(m, 3)
		ass2, err2 := ring2.Assign(m, 3)
		if err1 != nil || err2 != nil {
			t.Fatalf("Assign failed: err1=%v err2=%v", err1, err2)
		}
		if len(ass1) != 3 || len(ass2) != 3 {
			t.Fatalf("replica count mismatch: %d, %d", len(ass1), len(ass2))
		}
		for k := 0; k < 3; k++ {
			if ass1[k] != ass2[k] {
				t.Fatalf("non-deterministic placement for MID %s: %v vs %v", m, ass1, ass2)
			}
		}
	}

	// 2. Test Minimum Disruption property during node addition
	// When adding a 6th peer ("peer-F"), expected reassignments per peer should be ~1/6th of total
	ringExpanded := shard.NewHashRing()
	for _, p := range peers {
		ringExpanded.AddPeer(p)
	}
	ringExpanded.AddPeer("peer-F")

	gainedF, lostF := ring1.ComputeMigration(ringExpanded, "peer-F", mids, 3)
	if len(gainedF) == 0 {
		t.Error("newly added peer F must gain ownership of a subset of MIDs")
	}
	if len(lostF) != 0 {
		t.Error("newly added peer F should not lose any MIDs")
	}

	// 3. Test Failure Domain Awareness
	domainRing := shard.NewHashRing()
	domainRing.AddPeerWithDomain("node-rack1-a", "rack-1")
	domainRing.AddPeerWithDomain("node-rack1-b", "rack-1")
	domainRing.AddPeerWithDomain("node-rack2-a", "rack-2")
	domainRing.AddPeerWithDomain("node-rack3-a", "rack-3")

	domainAssigned, err := domainRing.Assign(mids[0], 3)
	if err != nil {
		t.Fatalf("domain Assign: %v", err)
	}
	if len(domainAssigned) != 3 {
		t.Fatalf("domain assigned count = %d, want 3", len(domainAssigned))
	}

	// Assert replicas land on distinct failure domains (rack-1, rack-2, rack-3)
	domainCounts := make(map[string]int)
	for _, p := range domainAssigned {
		if p == "node-rack1-a" || p == "node-rack1-b" {
			domainCounts["rack-1"]++
		} else if p == "node-rack2-a" {
			domainCounts["rack-2"]++
		} else if p == "node-rack3-a" {
			domainCounts["rack-3"]++
		}
	}

	if domainCounts["rack-1"] > 1 {
		t.Errorf("failure domain selection placed multiple replicas on rack-1: %v", domainAssigned)
	}
}
