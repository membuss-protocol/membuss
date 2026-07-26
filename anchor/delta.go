package anchor

import (
	"hash/fnv"

	"github.com/nnlgsakib/membuss/core/mid"
)

// BloomFilter represents a lightweight bitset Bloom filter for delta sync.
type BloomFilter struct {
	bits   []byte
	m      uint32 // number of bits
	hashes uint32 // k hash functions
}

// NewBloomFilter creates a BloomFilter optimized for n items.
func NewBloomFilter(itemCount int) *BloomFilter {
	if itemCount <= 0 {
		itemCount = 100
	}
	// 10 bits per item provides ~1% false positive rate
	numBits := uint32(itemCount * 10)
	if numBits < 64 {
		numBits = 64
	}
	// Align to byte boundary
	numBytes := (numBits + 7) / 8
	numBits = numBytes * 8

	return &BloomFilter{
		bits:   make([]byte, numBytes),
		m:      numBits,
		hashes: 3,
	}
}

// NewBloomFilterFromBytes reconstructs a BloomFilter from raw bitset bytes and hash count.
func NewBloomFilterFromBytes(bytes []byte, hashes uint32) *BloomFilter {
	if len(bytes) == 0 {
		return nil
	}
	if hashes == 0 {
		hashes = 3
	}
	return &BloomFilter{
		bits:   bytes,
		m:      uint32(len(bytes) * 8),
		hashes: hashes,
	}
}

func (b *BloomFilter) hashLocations(item string) []uint32 {
	locs := make([]uint32, b.hashes)
	h := fnv.New64a()
	_, _ = h.Write([]byte(item))
	h1 := h.Sum64()
	h2 := h1 >> 32

	for i := uint32(0); i < b.hashes; i++ {
		combined := h1 + uint64(i)*h2
		locs[i] = uint32(combined % uint64(b.m))
	}
	return locs
}

// Add inserts a MID string into the Bloom filter.
func (b *BloomFilter) Add(item string) {
	if b == nil || b.m == 0 {
		return
	}
	for _, loc := range b.hashLocations(item) {
		byteIdx := loc / 8
		bitIdx := loc % 8
		b.bits[byteIdx] |= (1 << bitIdx)
	}
}

// Contains checks if a MID string is in the Bloom filter.
func (b *BloomFilter) Contains(item string) bool {
	if b == nil || b.m == 0 || len(b.bits) == 0 {
		return false
	}
	for _, loc := range b.hashLocations(item) {
		byteIdx := loc / 8
		bitIdx := loc % 8
		if (b.bits[byteIdx] & (1 << bitIdx)) == 0 {
			return false
		}
	}
	return true
}

// BuildInventoryBloom constructs a Bloom filter representing known MIDs.
func BuildInventoryBloom(knownMIDs []mid.MID) ([]byte, uint32) {
	if len(knownMIDs) == 0 {
		return nil, 0
	}
	bf := NewBloomFilter(len(knownMIDs))
	for _, m := range knownMIDs {
		bf.Add(m.String())
	}
	return bf.bits, bf.hashes
}

// FilterDelta returns only those sealed MIDs that are NOT present in the provided Bloom filter bytes.
func FilterDelta(sealedMIDs []mid.MID, bloomBytes []byte, hashes uint32) []mid.MID {
	if len(bloomBytes) == 0 {
		return sealedMIDs
	}
	bf := NewBloomFilterFromBytes(bloomBytes, hashes)
	if bf == nil {
		return sealedMIDs
	}

	var delta []mid.MID
	for _, m := range sealedMIDs {
		if !bf.Contains(m.String()) {
			delta = append(delta, m)
		}
	}
	return delta
}
