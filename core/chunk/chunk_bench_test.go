// Benchmarks for chunker throughput (finding.txt XC-002).
// Run: go test -bench BenchmarkChunker -benchmem ./core/chunk/
package chunk

import (
	"bytes"
	"io"
	"testing"
)

// benchData returns a deterministic pseudo-random payload so CDC
// boundaries are exercised realistically without rand overhead.
func benchData(n int) []byte {
	data := make([]byte, n)
	var x uint64 = 0x9e3779b97f4a7c15
	for i := range data {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		data[i] = byte(x)
	}
	return data
}

func benchChunker(b *testing.B, f ChunkerFactory) {
	const size = 8 << 20 // 8 MiB
	data := benchData(size)
	b.SetBytes(int64(size))
	b.ResetTimer()
	for b.Loop() {
		ch, err := f(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		var total int
		for {
			blk, err := ch.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			total += len(blk.Data())
		}
		if total != size {
			b.Fatalf("chunked %d of %d bytes", total, size)
		}
	}
}

func BenchmarkChunkerFixed256K(b *testing.B) { benchChunker(b, NewFixed(DefaultBlockSize)) }

func BenchmarkChunkerRabin(b *testing.B) { benchChunker(b, NewRabin()) }

func BenchmarkChunkerFastCDC(b *testing.B) { benchChunker(b, NewFastCDC()) }
