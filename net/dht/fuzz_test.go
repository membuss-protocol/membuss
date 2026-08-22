// Fuzz target for MemNS record validation (finding.txt XC-001).
// validateMemNS unmarshals attacker-controlled protobuf records,
// verifies signatures, owner/delegate binding and name binding.
package dht

import (
	"testing"

	membusspb "github.com/nnlgsakib/membuss/proto"
	"google.golang.org/protobuf/proto"
)

func FuzzMemNSValidate(f *testing.F) {
	f.Add("/memns/k1", []byte{})
	var empty membusspb.MemNSRecord
	if b, err := proto.Marshal(&empty); err == nil {
		f.Add("/memns/k1", b)
	}
	f.Add("/memns/k1", []byte{0x0a, 0x03, 0x61, 0x62, 0x63})

	f.Fuzz(func(t *testing.T, key string, value []byte) {
		_ = validateMemNS(key, value)
	})
}
