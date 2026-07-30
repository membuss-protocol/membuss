package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

// CountingBloomFilter is a thread-safe Counting Bloom Filter supporting
// O(1) insertions, O(1) deletions, and O(1) membership tests using
// 8-bit saturating counter buckets.
type CountingBloomFilter struct {
	mu       sync.RWMutex
	m        uint64 // Number of counter buckets
	k        uint64 // Number of hash functions
	counters []byte // 8-bit saturating counters per bucket
	count    uint64 // Total items currently present
}

// NewCountingBloomFilter constructs a Counting Bloom Filter optimized for
// expected capacity n and false positive rate p.
func NewCountingBloomFilter(n uint, p float64) *CountingBloomFilter {
	if n == 0 {
		n = 10_000_000
	}
	if p <= 0 || p >= 1.0 {
		p = 0.001
	}

	m, k := calculateCBFEstimates(uint64(n), p)
	return &CountingBloomFilter{
		m:        m,
		k:        k,
		counters: make([]byte, m),
	}
}

// calculateCBFEstimates computes optimal m (buckets) and k (hash count).
func calculateCBFEstimates(n uint64, p float64) (m uint64, k uint64) {
	m = uint64(math.Ceil(-1.0 * float64(n) * math.Log(p) / math.Pow(math.Log(2), 2)))
	if m == 0 {
		m = 1
	}
	k = uint64(math.Round(float64(m) / float64(n) * math.Log(2)))
	if k == 0 {
		k = 1
	}
	return m, k
}

// hash64 computes 64-bit FNV-1a hash.
func hash64(data []byte) (h1 uint64, h2 uint64) {
	var h uint64 = 14695981039346656037
	for _, b := range data {
		h ^= uint64(b)
		h *= 1099511628211
	}
	h1 = h
	// Derived second hash for Kirsch-Mitzenmacher double hashing
	h2 = (h >> 32) | (h << 32)
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	return h1, h2
}

// Add inserts data into the filter in O(1) time.
func (c *CountingBloomFilter) Add(data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == 0 || c.k == 0 {
		return
	}

	h1, h2 := hash64(data)
	for i := uint64(0); i < c.k; i++ {
		idx := (h1 + i*h2) % c.m
		if c.counters[idx] < 255 {
			c.counters[idx]++
		}
	}
	c.count++
}

// Remove decrements counter buckets for data in O(1) time.
func (c *CountingBloomFilter) Remove(data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == 0 || c.k == 0 {
		return
	}

	h1, h2 := hash64(data)
	for i := uint64(0); i < c.k; i++ {
		idx := (h1 + i*h2) % c.m
		if c.counters[idx] > 0 && c.counters[idx] < 255 {
			c.counters[idx]--
		}
	}
	if c.count > 0 {
		c.count--
	}
}

// Test checks if data is potentially in the filter in O(1) time.
func (c *CountingBloomFilter) Test(data []byte) bool {
	if c == nil || len(data) == 0 {
		return true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.m == 0 || c.k == 0 || len(c.counters) == 0 {
		return true
	}

	h1, h2 := hash64(data)
	for i := uint64(0); i < c.k; i++ {
		idx := (h1 + i*h2) % c.m
		if c.counters[idx] == 0 {
			return false
		}
	}
	return true
}

// MarshalBinary serializes the CountingBloomFilter into a binary byte slice.
func (c *CountingBloomFilter) MarshalBinary() ([]byte, error) {
	if c == nil {
		return nil, errors.New("cbf: nil filter")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Format: m (8 bytes) + k (8 bytes) + count (8 bytes) + counters (m bytes)
	buf := make([]byte, 24+len(c.counters))
	binary.BigEndian.PutUint64(buf[0:8], c.m)
	binary.BigEndian.PutUint64(buf[8:16], c.k)
	binary.BigEndian.PutUint64(buf[16:24], c.count)
	copy(buf[24:], c.counters)
	return buf, nil
}

// UnmarshalBinary deserializes a binary byte slice into the CountingBloomFilter.
func (c *CountingBloomFilter) UnmarshalBinary(data []byte) error {
	if len(data) < 24 {
		return errors.New("cbf: invalid binary payload length")
	}
	m := binary.BigEndian.Uint64(data[0:8])
	k := binary.BigEndian.Uint64(data[8:16])
	count := binary.BigEndian.Uint64(data[16:24])
	countersData := data[24:]

	if uint64(len(countersData)) != m {
		return fmt.Errorf("cbf: payload size mismatch (want %d, got %d)", m, len(countersData))
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = m
	c.k = k
	c.count = count
	c.counters = make([]byte, len(countersData))
	copy(c.counters, countersData)
	return nil
}
