// Fuzz targets for memex v2 wire format (finding.txt XC-001).
// readFrame is the entry point for all inbound protocol frames;
// after framing succeeds the payload goes through protobuf
// unmarshal, which this target also exercises.
package memex_v2

import (
	"bytes"
	"testing"

	"github.com/nnlgsakib/membuss/internal/wiretest"
	membusspb "github.com/nnlgsakib/membuss/proto"
	"google.golang.org/protobuf/proto"
)

func FuzzMemexReadFrame(f *testing.F) {
	f.Add(append([]byte{0x00, 0x00, 0x10, 0x00}, make([]byte, 0x1000)...))
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x08})
	f.Add([]byte{})
	f.Add([]byte{0xFF})
	f.Add(append([]byte{0x7F, 0xFF, 0xFF, 0xFF}, make([]byte, 16)...))

	f.Fuzz(func(t *testing.T, data []byte) {
		s := wiretest.NewStream(bytes.NewReader(data))
		frame := readFrame(s)
		if frame == nil {
			return
		}
		var m membusspb.MemexMessage
		_ = proto.Unmarshal(frame, &m)
	})
}
