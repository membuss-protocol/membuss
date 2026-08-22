// Fuzz target for descriptor parsing (finding.txt XC-001).
// Parse consumes untrusted .mbuss bytes from the network; magic,
// version, checksum, protobuf payload and embedded MIDs are all
// attacker-controlled.
package descriptor

import (
	"testing"

	"github.com/nnlgsakib/membuss/core/mid"
)

func FuzzDescriptorParse(f *testing.F) {
	// Seed: a valid serialized descriptor built the same way the
	// existing tests build one.
	root := mid.FromBytes([]byte("fuzz-root"))
	d := &Descriptor{
		RootMID:   root,
		TotalSize: 4,
		Name:      "fuzz",
		MimeType:  "application/octet-stream",
	}
	d.Blocks = []BlockEntry{{MID: mid.FromBytes([]byte("b1")), Size: 4}}
	blob, err := d.Serialize()
	if err == nil {
		f.Add(blob)
	}
	f.Add([]byte{})
	f.Add([]byte("MEMB"))
	f.Add([]byte("MEMB\x01"))
	f.Add(make([]byte, 37))

	f.Fuzz(func(t *testing.T, data []byte) {
		dsc, err := Parse(data)
		if err != nil {
			return
		}
		if dsc.RootMID.IsZero() || len(dsc.Blocks) == 0 && dsc.TotalSize > 0 {
			t.Fatalf("parsed descriptor with zero root MID or missing blocks: %+v", dsc)
		}
	})
}
