// Benchmarks for Reed-Solomon encode/decode (finding.txt XC-002).
package erasure

import (
	"testing"
)

func benchEncoder(b *testing.B) *Encoder {
	enc, err := NewEncoder(DefaultConfig())
	if err != nil {
		b.Fatal(err)
	}
	return enc
}

func BenchmarkErasureEncode1MiB(b *testing.B) {
	enc := benchEncoder(b)
	data := make([]byte, 1<<20)
	b.SetBytes(1 << 20)
	for b.Loop() {
		if _, err := enc.Encode(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkErasureDecode1MiB(b *testing.B) {
	enc := benchEncoder(b)
	data := make([]byte, 1<<20)
	encd, err := enc.Encode(data)
	if err != nil {
		b.Fatal(err)
	}
	shards := make([][]byte, len(encd.Shards))
	for i, sh := range encd.Shards {
		shards[i] = sh.Data
	}
	for b.Loop() {
		if _, err := enc.Decode(shards, encd.Manifest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkErasureVerify1MiB(b *testing.B) {
	enc := benchEncoder(b)
	data := make([]byte, 1<<20)
	encd, err := enc.Encode(data)
	if err != nil {
		b.Fatal(err)
	}
	shards := make([][]byte, len(encd.Shards))
	for i, sh := range encd.Shards {
		shards[i] = sh.Data
	}
	for b.Loop() {
		if ok, err := enc.Verify(shards); err != nil || !ok {
			b.Fatal("verify failed")
		}
	}
}
