// Fuzz target for gateway Range header parsing (finding.txt XC-001).
// parseRange consumes attacker-controlled header strings on every
// media request; bounds math must stay inside [0,size].
package memgate_v2

import (
	"testing"
)

func FuzzParseRange(f *testing.F) {
	f.Add("bytes=0-", int64(100))
	f.Add("bytes=-5", int64(100))
	f.Add("bytes=5-9", int64(100))
	f.Add("bytes=", int64(100))
	f.Add("", int64(0))
	f.Add("bytes=0-18446744073709551615", int64(100))
	f.Add("bytes=18446744073709551615-", int64(100))

	f.Fuzz(func(t *testing.T, spec string, size int64) {
		start, end, err := parseRange(spec, size)
		if err != nil {
			return
		}
		if start < 0 || end > size || start >= end {
			t.Fatalf("parseRange(%q,%d) = [%d,%d): out of bounds", spec, size, start, end)
		}
	})
}
