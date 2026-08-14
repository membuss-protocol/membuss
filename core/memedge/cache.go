package memedge

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// CodeCache is an in-memory LRU cache for compiled artifacts.
type CodeCache struct {
	mu       sync.RWMutex
	capacity int
	items    map[string]any
	order    []string
}

// NewCodeCache creates a new CodeCache with the given capacity.
func NewCodeCache(capacity int) *CodeCache {
	if capacity <= 0 {
		capacity = 128
	}
	return &CodeCache{
		capacity: capacity,
		items:    make(map[string]any),
		order:    make([]string, 0, capacity),
	}
}

// KeyForCode computes a deterministic sha256 hash string for bytecode.
func KeyForCode(code []byte) string {
	h := sha256.Sum256(code)
	return hex.EncodeToString(h[:])
}

// Get retrieves an item by key.
func (c *CodeCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	val, found := c.items[key]
	if !found {
		return nil, false
	}

	// Move to back (most recently used)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			break
		}
	}

	return val, true
}

// Set adds or updates an item in the cache.
func (c *CodeCache) Set(key string, val any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.items[key]; exists {
		c.items[key] = val
		return
	}

	// Evict oldest if capacity reached
	if len(c.items) >= c.capacity && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}

	c.items[key] = val
	c.order = append(c.order, key)
}

// Clear empties the cache.
func (c *CodeCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]any)
	c.order = c.order[:0]
}
