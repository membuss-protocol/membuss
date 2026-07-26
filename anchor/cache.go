package anchor

import (
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

// SealedCache maintains a thread-safe in-memory cache of sealed MIDs
// to eliminate O(N) database key scans on every stream request.
type SealedCache struct {
	mu        sync.RWMutex
	mids      []mid.MID
	set       map[string]struct{}
	ttl       time.Duration
	lastFetch time.Time
}

// NewSealedCache initializes a SealedCache with the specified TTL duration.
func NewSealedCache(ttl time.Duration) *SealedCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &SealedCache{
		set: make(map[string]struct{}),
		ttl: ttl,
	}
}

// GetSealed returns cached sealed MIDs if fresh, or uses fetcher to update the cache.
func (c *SealedCache) GetSealed(fetcher func() ([]mid.MID, error)) ([]mid.MID, error) {
	c.mu.RLock()
	if !c.lastFetch.IsZero() && time.Since(c.lastFetch) < c.ttl {
		mids := c.mids
		c.mu.RUnlock()
		return mids, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.lastFetch.IsZero() && time.Since(c.lastFetch) < c.ttl {
		return c.mids, nil
	}

	mids, err := fetcher()
	if err != nil {
		return nil, err
	}

	c.mids = mids
	c.set = make(map[string]struct{}, len(mids))
	for _, m := range mids {
		c.set[m.String()] = struct{}{}
	}
	c.lastFetch = time.Now()

	return mids, nil
}

// Add adds a newly sealed MID to the in-memory cache immediately.
func (c *SealedCache) Add(m mid.MID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.set[m.String()]; !exists {
		c.set[m.String()] = struct{}{}
		c.mids = append(c.mids, m)
	}
}
