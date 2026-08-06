package erasure_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

func TestEndToEndErasureIngestionPlacementRetrievalAndRepair(t *testing.T) {
	// 1. Generate random test payload (100 KB)
	payload := make([]byte, 100*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("Failed to generate random payload: %v", err)
	}
	originalMID := mid.FromBytes(payload)

	// 2. Initialize primary node store & 14 distinct peer provider stores
	primaryStore := store.NewMemstore()
	peerStores := make([]store.Blockstore, 14)
	for i := 0; i < 14; i++ {
		peerStores[i] = store.NewMemstore()
	}

	// 3. Encode payload into 10 data + 4 parity shards (14 total)
	cfg := erasure.DefaultConfig() // 10 data, 4 parity
	encoder, err := erasure.NewEncoder(cfg)
	if err != nil {
		t.Fatalf("Failed to create Reed-Solomon encoder: %v", err)
	}

	encoded, err := encoder.Encode(payload)
	if err != nil {
		t.Fatalf("Failed to encode payload: %v", err)
	}

	if len(encoded.Shards) != 14 {
		t.Fatalf("Expected 14 total shards, got %d", len(encoded.Shards))
	}

	// Store original block & manifest in primaryStore
	if err := primaryStore.Put(originalMID, payload); err != nil {
		t.Fatalf("Failed to store original block: %v", err)
	}
	if err := erasure.SetManifest(primaryStore, originalMID, encoded.Manifest); err != nil {
		t.Fatalf("Failed to store erasure manifest: %v", err)
	}

	// Distribute each of the 14 shards to a distinct failure domain / peer store
	for i, shard := range encoded.Shards {
		if err := peerStores[i].Put(shard.ShardMID, shard.Data); err != nil {
			t.Fatalf("Failed to distribute shard %d to peer %d: %v", i, i, err)
		}
		// Also mirror shard in primaryStore
		_ = primaryStore.Put(shard.ShardMID, shard.Data)
	}

	// 4. Simulate Failure of up to 4 distinct shard providers (remove 4 peers)
	t.Logf("Simulating failure: Removing 4 of 14 distinct shard providers (Peers 0, 1, 2, 3)...")
	for i := 0; i < 4; i++ {
		// Delete primary block and first 4 shard blocks to force reconstruction from remaining 10 shards
		sm, _ := mid.Parse(encoded.Manifest.ShardMids[i])
		_ = primaryStore.Delete(sm)
		peerStores[i] = store.NewMemstore() // Peer offline
	}
	// Also delete original block from primaryStore so it must reconstruct from shards
	_ = primaryStore.Delete(originalMID)

	// Verify original MID is missing locally before reconstruction
	has, _ := primaryStore.Has(originalMID)
	if has {
		t.Fatalf("Expected primary block to be deleted for reconstruction test")
	}

	// 5. Retrieve & Reconstruct from remaining 10 shards (Peers 4..13)
	availableShards := make([][]byte, 14)
	for i := 4; i < 14; i++ {
		sm, _ := mid.Parse(encoded.Manifest.ShardMids[i])
		sData, err := peerStores[i].Get(sm)
		if err != nil {
			t.Fatalf("Failed to retrieve shard from peer %d: %v", i, err)
		}
		if !erasure.VerifyShard(sData, encoded.Manifest.ShardMids[i]) {
			t.Fatalf("Shard from peer %d failed verification", i)
		}
		availableShards[i] = sData
	}

	reconstructedBytes, err := encoder.Decode(availableShards, encoded.Manifest)
	if err != nil {
		t.Fatalf("Failed to reconstruct payload from remaining 10 shards: %v", err)
	}

	if !bytes.Equal(reconstructedBytes, payload) {
		t.Fatalf("Reconstructed payload bytes do not match original input payload!")
	}
	t.Logf("✅ Successfully reconstructed 100%% byte-identical payload from 10/14 remaining shards!")

	// 6. Test Background Repair Worker restoring missing 4 shards
	repairedCount, err := erasure.RepairMID(primaryStore, originalMID)
	if err != nil {
		t.Fatalf("RepairMID failed: %v", err)
	}
	if repairedCount != 4 {
		t.Errorf("Expected RepairMID to reconstruct 4 missing shards, repaired %d", repairedCount)
	}

	// Verify background worker auditing
	worker := erasure.NewRepairWorker(primaryStore, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = primaryStore.Seal(originalMID, true)
	stats := worker.AuditAndRepair(ctx)
	if stats.AuditedMIDs == 0 {
		t.Errorf("Expected background repair worker to audit sealed MID")
	}
	t.Logf("✅ Repair worker audit complete: %d audited, %d repaired shards", stats.AuditedMIDs, stats.RepairedShards)
}
