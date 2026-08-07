package memfs_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	membusspb "github.com/nnlgsakib/membuss/proto"
	"google.golang.org/protobuf/proto"
)

func TestMemFSStreamingRecursiveTree(t *testing.T) {
	memStore, err := store.NewMemStore(store.Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer memStore.Close()

	// Use 1024B (MinBlockSize) to easily trigger 3+ level deep trees
	smallBlockSize := 1024
	totalBlocks := dag.Fanout*dag.Fanout + 10 // > 174*174 + 10 = 30,286 blocks -> requires 3 levels
	totalSize := totalBlocks * smallBlockSize

	data := make([]byte, totalSize)
	_, _ = rand.Read(data)

	bld := memfs.NewBuilder(memStore).WithBlockSize(smallBlockSize)
	res, err := bld.AddFile("large_stream.bin", bytes.NewReader(data), 0o644, time.Now(), "application/octet-stream")
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	if res.Size != uint64(totalSize) {
		t.Errorf("size got %d want %d", res.Size, totalSize)
	}
	if res.Block != uint64(totalBlocks) {
		t.Errorf("blocks got %d want %d", res.Block, totalBlocks)
	}

	// Assert that no MemFSNode in the tree exceeds dag.Fanout
	var assertFanout func(m mid.MID)
	assertFanout = func(m mid.MID) {
		raw, err := memStore.Get(m)
		if err != nil {
			return
		}
		var node membusspb.MemFSNode
		if uerr := proto.Unmarshal(raw, &node); uerr == nil && node.GetType() == membusspb.MemFSType_FILE {
			if len(node.GetBlocks()) > dag.Fanout {
				t.Fatalf("node %s exceeded fanout limit: got %d blocks, max %d", m.String(), len(node.GetBlocks()), dag.Fanout)
			}
			for _, b := range node.GetBlocks() {
				if b.Size == 0 { // size == 0 means intermediate node
					childMID, _ := mid.Parse(string(b.Mid))
					assertFanout(childMID)
				}
			}
		}
	}
	assertFanout(res.MID)

	// Roundtrip verification via memfs.Open
	resolver := memfs.NewResolver(memStore)
	rc, err := resolver.Open(context.Background(), res.MID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()

	readBuf, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(readBuf, data) {
		t.Fatal("roundtrip bytes do not match original data")
	}
}
