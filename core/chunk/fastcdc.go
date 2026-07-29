package chunk

import (
	"errors"
	"fmt"
	"io"

	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

var gearTable [256]uint64

func init() {
	// Generate deterministic pseudo-random 64-bit values for the Gear table
	var val uint64 = 0x8a5cd789635d2d01
	for i := 0; i < 256; i++ {
		val = val*6364136223846793005 + 1442695040888963407
		gearTable[i] = val
	}
}

const (
	fastcdcMin  = 64 * 1024  // 64 KiB
	fastcdcAvg  = 256 * 1024 // 256 KiB
	fastcdcMax  = 512 * 1024 // 512 KiB
	fastcdcMid  = 128 * 1024 // 128 KiB normalized threshold
	maskNormal  = (1 << 17) - 1
	maskTight   = (1 << 19) - 1
)

type fastCDCChunker struct {
	r       io.Reader
	buf     []byte
	pos     int
	limit   int
	eofSeen bool
}

func (c *fastCDCChunker) Close() error {
	c.eofSeen = true
	if closer, ok := c.r.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// NewFastCDC returns a ChunkerFactory that splits the input using FastCDC.
func NewFastCDC() ChunkerFactory {
	return func(r io.Reader) (Chunker, error) {
		if r == nil {
			return nil, errors.New("chunk: nil reader")
		}
		// Allocate a buffer large enough to hold at least two maximum chunks
		return &fastCDCChunker{
			r:   r,
			buf: make([]byte, fastcdcMax*2),
		}, nil
	}
}

func (c *fastCDCChunker) Next() (Block, error) {
	for {
		// Fill buffer if we don't have enough data
		needed := fastcdcMax
		if c.limit-c.pos < needed && !c.eofSeen {
			// Shift remaining bytes to start of buffer
			remaining := c.limit - c.pos
			if remaining > 0 {
				copy(c.buf, c.buf[c.pos:c.limit])
			}
			c.pos = 0
			c.limit = remaining

			// Read more data
			n, err := c.r.Read(c.buf[c.limit:])
			c.limit += n
			if err != nil {
				if errors.Is(err, io.EOF) {
					c.eofSeen = true
				} else {
					c.eofSeen = true
					closeReader(c.r)
					return Block{}, fmt.Errorf("chunk: fastcdc read: %w", err)
				}
			}
			continue
		}

		available := c.limit - c.pos
		if available == 0 {
			closeReader(c.r)
			return Block{}, io.EOF
		}

		// If remaining data is smaller than minimum size, return it as the final chunk
		if available <= fastcdcMin {
			data := c.buf[c.pos:c.limit]
			c.pos = c.limit
			return makeFastCDCBlock(data)
		}

		// Scan for boundary using Gear Hash
		h := uint64(0)
		end := c.pos + available
		if end > c.pos+fastcdcMax {
			end = c.pos + fastcdcMax
		}

		cutPos := end
		for i := c.pos + fastcdcMin; i < end; i++ {
			h = (h << 1) + gearTable[c.buf[i]]
			if i-c.pos < fastcdcMid {
				if h&maskNormal == 0 {
					cutPos = i
					break
				}
			} else {
				if h&maskTight == 0 {
					cutPos = i
					break
				}
			}
		}

		data := c.buf[c.pos:cutPos]
		c.pos = cutPos
		return makeFastCDCBlock(data)
	}
}

func makeFastCDCBlock(data []byte) (Block, error) {
	if len(data) == 0 {
		return Block{}, errors.New("chunk: refusing to emit empty block")
	}
	m := mid.FromBytes(data)
	return Block{Block: &membusspb.Block{
		Data: append([]byte(nil), data...),
		Mid:  m.String(),
		Size: uint64(len(data)),
	}}, nil
}
