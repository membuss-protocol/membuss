package memgate_v2

import (
	"encoding/json"
	"hash/fnv"
	"sync"
)

// --- LRU ---

// lru is a small bounded-byte LRU. The data structure is
// hand-rolled so the package does not pull in a heavy
// dependency. For a CDN edge, a simple list + map is more
// than adequate.
type lru struct {
	mu       sync.Mutex
	maxBytes uint64
	curBytes uint64
	// items is ordered most-recent-first.
	items map[string]*listEntry
	head  *listEntry
	tail  *listEntry
}

type listEntry struct {
	key  string
	data []byte
	prev *listEntry
	next *listEntry
}

func newLRU(maxBytes uint64) *lru {
	return &lru{maxBytes: maxBytes, items: make(map[string]*listEntry)}
}

func (l *lru) get(key string) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.items[key]
	if !ok {
		return nil, false
	}
	l.moveToFront(e)
	return e.data, true
}

func (l *lru) put(key string, data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.items[key]; ok {
		l.curBytes -= uint64(len(e.data))
		e.data = data
		l.curBytes += uint64(len(data))
		l.moveToFront(e)
	} else {
		e := &listEntry{key: key, data: data}
		l.items[key] = e
		l.curBytes += uint64(len(data))
		l.pushFront(e)
	}
	for l.curBytes > l.maxBytes && l.tail != nil {
		old := l.tail
		l.remove(old)
		delete(l.items, old.key)
		l.curBytes -= uint64(len(old.data))
	}
}

func (l *lru) moveToFront(e *listEntry) {
	if l.head == e {
		return
	}
	l.remove(e)
	l.pushFront(e)
}

func (l *lru) pushFront(e *listEntry) {
	e.prev = nil
	e.next = l.head
	if l.head != nil {
		l.head.prev = e
	}
	l.head = e
	if l.tail == nil {
		l.tail = e
	}
}

func (l *lru) remove(e *listEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		l.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

// len returns the current number of entries.
func (l *lru) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

// Bytes returns the current cache size in bytes.
func (l *lru) bytes() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curBytes
}

// MaxBytes returns the configured cache cap.
func (l *lru) max() uint64 { return l.maxBytes }

// MarshalJSON renders the cache as a small JSON object.
func (l *lru) MarshalJSON() ([]byte, error) {
	type view struct {
		Entries int    `json:"entries"`
		Bytes   uint64 `json:"bytes"`
		Max     uint64 `json:"max_bytes"`
	}
	v := view{Entries: l.len(), Bytes: l.bytes(), Max: l.max()}
	return json.Marshal(v)
}

// --- Sharded LRU ---

// defaultCacheShards is the number of independent LRU shards
// backing the gateway cache. A power of two so the shard index
// is a cheap mask. Sixteen shards keep lock contention low even
// under thousands of concurrent requests while bounding overhead.
const defaultCacheShards = 16

// shardedLRU spreads keys across N independent lru shards, each
// with its own mutex and its own byte budget. This removes the
// single-mutex serialization bottleneck: concurrent requests for
// keys in different shards never contend. The total byte cap is
// preserved (sum of per-shard caps == maxBytes).
type shardedLRU struct {
	shards []*lru
	mask   uint64
}

// newShardedLRU builds a sharded cache holding at most maxBytes
// total across shardCount shards. shardCount is rounded down to a
// power of two (min 1). If maxBytes is too small to give each
// shard at least one byte, the shard count is reduced so no shard
// gets a zero budget.
func newShardedLRU(maxBytes uint64, shardCount int) *shardedLRU {
	if shardCount < 1 {
		shardCount = 1
	}
	// Round down to a power of two for masking.
	n := 1
	for n*2 <= shardCount {
		n *= 2
	}
	// Never give a shard a zero-byte budget: a zero cap would evict
	// every entry immediately. Cap the shard count by maxBytes.
	if maxBytes > 0 && uint64(n) > maxBytes {
		n = 1
		for uint64(n*2) <= maxBytes {
			n *= 2
		}
	}
	per := maxBytes / uint64(n)
	rem := maxBytes % uint64(n)
	shards := make([]*lru, n)
	for i := range shards {
		cap := per
		// Distribute the remainder to the first shards so the total
		// cap exactly equals maxBytes.
		if uint64(i) < rem {
			cap++
		}
		shards[i] = newLRU(cap)
	}
	return &shardedLRU{shards: shards, mask: uint64(n - 1)}
}

// shard returns the lru responsible for key.
func (s *shardedLRU) shard(key string) *lru {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return s.shards[h.Sum64()&s.mask]
}

func (s *shardedLRU) get(key string) ([]byte, bool) {
	return s.shard(key).get(key)
}

func (s *shardedLRU) put(key string, data []byte) {
	s.shard(key).put(key, data)
}

// len returns the total number of entries across all shards.
func (s *shardedLRU) len() int {
	total := 0
	for _, sh := range s.shards {
		total += sh.len()
	}
	return total
}

// bytes returns the total bytes cached across all shards.
func (s *shardedLRU) bytes() uint64 {
	var total uint64
	for _, sh := range s.shards {
		total += sh.bytes()
	}
	return total
}

// max returns the configured total cache cap across all shards.
func (s *shardedLRU) max() uint64 {
	var total uint64
	for _, sh := range s.shards {
		total += sh.max()
	}
	return total
}

// MarshalJSON renders the aggregate cache stats as a small JSON
// object, matching the single-lru shape so the status endpoint is
// unchanged.
func (s *shardedLRU) MarshalJSON() ([]byte, error) {
	type view struct {
		Entries int    `json:"entries"`
		Bytes   uint64 `json:"bytes"`
		Max     uint64 `json:"max_bytes"`
		Shards  int    `json:"shards"`
	}
	v := view{Entries: s.len(), Bytes: s.bytes(), Max: s.max(), Shards: len(s.shards)}
	return json.Marshal(v)
}
