// Benchmarks for the Pebble-backed MemStore (finding.txt XC-002).
// In-memory Pebble isolates CPU/codec cost from disk variance;
// run against a real path separately when profiling IO.
package store

import (
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
)

func benchStore(b *testing.B) *MemStore {
	s, err := NewMemStore(Options{InMemory: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

func BenchmarkStorePut256K(b *testing.B) {
	s := benchStore(b)
	blk := make([]byte, 256<<10)
	for b.Loop() {
		blk[0] = byte(b.N)
		if err := s.Put(mid.FromBytes(blk), blk); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreGet256K(b *testing.B) {
	s := benchStore(b)
	blk := make([]byte, 256<<10)
	blk[0] = 0xAB
	m := mid.FromBytes(blk)
	if err := s.Put(m, blk); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := s.Get(m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStorePutBatch256x64K(b *testing.B) {
	s := benchStore(b)
	const n = 256
	batch := make([]Block, 0, n)
	for i := range n {
		data := make([]byte, 64<<10)
		data[0] = byte(i)
		batch = append(batch, Block{MID: mid.FromBytes(data), Data: data})
	}
	for b.Loop() {
		if err := s.PutBatch(batch); err != nil {
			b.Fatal(err)
		}
	}
}
