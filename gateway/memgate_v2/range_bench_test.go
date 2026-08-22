// Benchmark for Range-header parsing (finding.txt XC-002).
package memgate_v2

import "testing"

func BenchmarkParseRange(b *testing.B) {
	specs := []struct {
		name string
		hdr  string
		size int64
	}{
		{"suffix", "bytes=-1024", 1 << 20},
		{"open-end", "bytes=4096-", 1 << 20},
		{"closed", "bytes=100-199", 1 << 20},
	}
	for _, s := range specs {
		b.Run(s.name, func(b *testing.B) {
			for b.Loop() {
				if _, _, err := parseRange(s.hdr, s.size); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
