// Fuzz targets for PEX wire-format parsing (finding.txt XC-001).
// readMsg accepts untrusted bytes from any connected peer; this
// target hammers both the length-prefixed path and the raw fallback
// path with arbitrary inputs.
package pex

import (
	"bytes"
	"testing"

	"github.com/nnlgsakib/membuss/internal/wiretest"
)

const pexMaxFrame = 1 << 20 // mirrors readMsg's internal frame cap

func FuzzPEXReadMsg(f *testing.F) {
	f.Add(append([]byte{0x00, 0x00, 0x00, 0x04}, []byte("abcd")...))
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add(append([]byte{0x7F, 0xFF, 0xFF, 0xFF}, make([]byte, 16)...))
	f.Add([]byte("raw fallback bytes without length prefix"))

	f.Fuzz(func(t *testing.T, data []byte) {
		s := wiretest.NewStream(bytes.NewReader(data))
		msg := readMsg(s)
		if len(msg) > pexMaxFrame {
			t.Fatalf("readMsg returned %d bytes, exceeds cap %d", len(msg), pexMaxFrame)
		}
	})
}
