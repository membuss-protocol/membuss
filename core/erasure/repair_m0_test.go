package erasure_test

import (
	"bytes"
	"testing"

	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

func TestRepairMID_SkipsShardlessManifest(t *testing.T) {
	s := store.NewMemstore()
	defer s.Close()

	root := mid.FromBytes([]byte("root-summary-manifest"))
	shardless := &membusspb.ErasureManifest{
		OriginalMid:  root.String(),
		DataShards:   8,
		ParityShards: 3,
		OriginalSize: 24,
	}
	if err := erasure.SetManifest(s, root, shardless); err != nil {
		t.Fatal(err)
	}

	n, err := erasure.RepairMID(s, root)
	if err != nil {
		t.Fatalf("expected quiet skip, got error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 repairs, got %d", n)
	}
}

func TestRepairMID_RepairsMissingShard(t *testing.T) {
	s := store.NewMemstore()
	defer s.Close()

	data := []byte("repair me: any k of n shards reconstruct the original")
	cfg, _ := erasure.NewConfig(4, 2)
	enc, err := erasure.NewEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := enc.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, shard := range encoded.Shards {
		if shard.Index == 3 {
			continue // simulate loss
		}
		if err := s.Put(shard.ShardMID, shard.Data); err != nil {
			t.Fatal(err)
		}
	}
	leaf := mid.FromBytes(data)
	if err := erasure.SetManifest(s, leaf, encoded.Manifest); err != nil {
		t.Fatal(err)
	}

	n, err := erasure.RepairMID(s, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 repaired shard, got %d", n)
	}
	recovered, gerr := s.Get(encoded.Shards[3].ShardMID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if !bytes.Equal(recovered, encoded.Shards[3].Data) {
		t.Fatal("regenerated shard bytes differ")
	}
}

func TestGetManifest_CorruptReturnsError(t *testing.T) {
	s := store.NewMemstore()
	defer s.Close()

	m := mid.FromBytes([]byte("corrupt-manifest-holder"))
	if err := s.PutMeta(erasure.ManifestMetaKey(m), []byte{0xff, 0x00, 0x01}); err != nil {
		t.Fatal(err)
	}
	if _, err := erasure.GetManifest(s, m); err == nil {
		t.Fatal("expected error for corrupt manifest bytes, got nil")
	}
}
