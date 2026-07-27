package memgate_v2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/mid"
)

// --- Fix #1: sharded LRU ---

func TestShardedLRU_BasicGetPut(t *testing.T) {
	l := newShardedLRU(1<<20, defaultCacheShards)
	l.put("a", []byte("hello"))
	if data, ok := l.get("a"); !ok || string(data) != "hello" {
		t.Errorf("get a: %q ok=%v", data, ok)
	}
	if _, ok := l.get("missing"); ok {
		t.Errorf("missing key should not be found")
	}
}

func TestShardedLRU_TotalCapEqualsMax(t *testing.T) {
	// The sum of per-shard caps must exactly equal the requested max,
	// even when max is not evenly divisible by the shard count.
	for _, max := range []uint64{100, 1023, 64 * 1024 * 1024, 17} {
		l := newShardedLRU(max, defaultCacheShards)
		if l.max() != max {
			t.Errorf("max=%d: total cap %d != %d", max, l.max(), max)
		}
	}
}

func TestShardedLRU_RespectsTotalBudget(t *testing.T) {
	l := newShardedLRU(4096, defaultCacheShards)
	// Insert far more than the budget; total bytes must never exceed max.
	for i := 0; i < 5000; i++ {
		l.put("key"+strconv.Itoa(i), make([]byte, 64))
	}
	if l.bytes() > l.max() {
		t.Errorf("bytes over cap: %d > %d", l.bytes(), l.max())
	}
}

func TestShardedLRU_SmallMaxNoZeroShard(t *testing.T) {
	// A max smaller than the shard count must not create zero-byte
	// shards (which would evict every entry immediately). Instead the
	// shard count is reduced so every shard keeps a nonzero budget,
	// while the total cap is still preserved exactly.
	l := newShardedLRU(4, defaultCacheShards)
	if l.max() != 4 {
		t.Errorf("max: got %d want 4", l.max())
	}
	for _, sh := range l.shards {
		if sh.max() == 0 {
			t.Errorf("shard with zero budget")
		}
	}
	// A value that fits within a single shard's budget is retained.
	l.put("x", []byte("a"))
	if _, ok := l.get("x"); !ok {
		t.Errorf("value within a shard budget should fit")
	}
}

// TestNew_ShardCountRespectsItemCap verifies the gateway couples shard
// count to the item cap so any cacheable item fits in a single shard.
// Otherwise an item larger than a shard's budget would be buffered
// into memory and then evicted immediately on put.
func TestNew_ShardCountRespectsItemCap(t *testing.T) {
	b := newMemBackend()
	// Item cap is a quarter of the total; every shard must therefore
	// hold at least the item cap so a max-size item is not evicted.
	mg, err := New(Config{Backend: b, MaxCacheBytes: 4096, MaxCacheItemBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, sh := range mg.lru.shards {
		if sh.max() < mg.cfg.MaxCacheItemBytes {
			t.Errorf("shard budget %d smaller than item cap %d", sh.max(), mg.cfg.MaxCacheItemBytes)
		}
	}
	// An item exactly at the cap must survive a put.
	item := make([]byte, mg.cfg.MaxCacheItemBytes)
	mg.lru.put("k", item)
	if _, ok := mg.lru.get("k"); !ok {
		t.Errorf("item at the cap should be retained, not evicted")
	}
}

func TestShardedLRU_MarshalJSON(t *testing.T) {
	l := newShardedLRU(1<<20, defaultCacheShards)
	l.put("a", []byte("hello"))
	b, err := jsonMarshal(l)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !contains(s, `"entries":1`) {
		t.Errorf("marshal entries: %s", s)
	}
	if !contains(s, `"shards":`) {
		t.Errorf("marshal shards: %s", s)
	}
}

func TestShardedLRU_ConcurrentAccess(t *testing.T) {
	// Hammer the cache from many goroutines. Meaningful under -race;
	// otherwise this at least exercises the sharded paths concurrently.
	l := newShardedLRU(1<<20, defaultCacheShards)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				k := "k" + strconv.Itoa((g*500+i)%2000)
				l.put(k, []byte(k))
				l.get(k)
			}
		}(g)
	}
	wg.Wait()
	if l.bytes() > l.max() {
		t.Errorf("bytes over cap after concurrent load: %d > %d", l.bytes(), l.max())
	}
}

// --- Fix #4: binary-search findBlockIndex ---

// linearFindBlockIndex is the original O(n) implementation, kept here
// as the reference oracle for the binary-search version.
func linearFindBlockIndex(blocks []dagBlock, pos int64) int {
	for i, b := range blocks {
		if pos >= b.offset && pos < b.offset+b.size {
			return i
		}
	}
	return -1
}

func TestFindBlockIndex_MatchesLinear(t *testing.T) {
	// Build a synthetic block list with varied sizes, including a
	// zero-size block, in monotonic offset order.
	sizes := []int64{10, 1, 0, 500, 64, 1, 4096, 7}
	var blocks []dagBlock
	var off int64
	for _, s := range sizes {
		blocks = append(blocks, dagBlock{size: s, offset: off})
		off += s
	}
	r := &dagReader{blocks: blocks}

	// Exhaustively check every position from before the start to past
	// the end against the linear oracle.
	for pos := int64(-2); pos <= off+2; pos++ {
		got := r.findBlockIndex(pos)
		want := linearFindBlockIndex(blocks, pos)
		if got != want {
			t.Fatalf("pos=%d: got %d want %d", pos, got, want)
		}
	}
}

func TestFindBlockIndex_LargeList(t *testing.T) {
	// A large list where binary search matters; check boundaries.
	const n = 10000
	const size = int64(256)
	blocks := make([]dagBlock, n)
	for i := 0; i < n; i++ {
		blocks[i] = dagBlock{size: size, offset: int64(i) * size}
	}
	r := &dagReader{blocks: blocks}
	total := int64(n) * size

	for _, pos := range []int64{0, size - 1, size, total/2 - 1, total / 2, total - 1, total, total + 100, -1} {
		if got, want := r.findBlockIndex(pos), linearFindBlockIndex(blocks, pos); got != want {
			t.Errorf("pos=%d: got %d want %d", pos, got, want)
		}
	}
}

func TestFindBlockIndex_Empty(t *testing.T) {
	r := &dagReader{blocks: nil}
	if got := r.findBlockIndex(0); got != -1 {
		t.Errorf("empty: got %d want -1", got)
	}
}

// --- Fix #3: block list cache ---

func TestBlockListCache_EvictsOldest(t *testing.T) {
	c := newBlockListCache(2)
	c.put("a", &blockListEntry{totalSize: 1})
	c.put("b", &blockListEntry{totalSize: 2})
	c.put("c", &blockListEntry{totalSize: 3}) // evicts "a"
	if _, ok := c.get("a"); ok {
		t.Errorf("a should have been evicted")
	}
	if _, ok := c.get("b"); !ok {
		t.Errorf("b should be present")
	}
	if _, ok := c.get("c"); !ok {
		t.Errorf("c should be present")
	}
}

func TestBlockListCache_LRUOrdering(t *testing.T) {
	c := newBlockListCache(2)
	c.put("a", &blockListEntry{totalSize: 1})
	c.put("b", &blockListEntry{totalSize: 2})
	// Touch "a" so "b" becomes the least-recently-used.
	if _, ok := c.get("a"); !ok {
		t.Fatalf("a missing")
	}
	c.put("c", &blockListEntry{totalSize: 3}) // should evict "b"
	if _, ok := c.get("b"); ok {
		t.Errorf("b should have been evicted (was LRU)")
	}
	if _, ok := c.get("a"); !ok {
		t.Errorf("a should survive (recently used)")
	}
}

// countingBackend wraps memBackend and counts RawBlock calls, so we can
// assert the block list is built once and then served from cache.
type countingBackend struct {
	*memBackend
	rawBlockCalls int64
}

func (b *countingBackend) RawBlock(ctx context.Context, m mid.MID) ([]byte, error) {
	atomic.AddInt64(&b.rawBlockCalls, 1)
	return b.memBackend.RawBlock(ctx, m)
}

func TestCachedBlockList_BuildsOnce(t *testing.T) {
	cb := &countingBackend{memBackend: newMemBackend()}
	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i)
	}
	m := putRandom(cb.memBackend, body)

	mg, err := New(Config{Backend: cb, MaxCacheBytes: 1 << 20})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if _, _, err := mg.cachedBlockList(ctx, m); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first := atomic.LoadInt64(&cb.rawBlockCalls)
	if first == 0 {
		t.Fatalf("expected RawBlock to be called on first build")
	}
	// Second request for the same root must not hit RawBlock again.
	if _, _, err := mg.cachedBlockList(ctx, m); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if second := atomic.LoadInt64(&cb.rawBlockCalls); second != first {
		t.Errorf("expected no new RawBlock calls on cache hit: first=%d second=%d", first, second)
	}
}

// --- Fix #2: large items are streamed, not cached ---

func TestLargeResolvedItem_NotCached(t *testing.T) {
	b := newMemBackend()
	// Item larger than MaxCacheItemBytes but within MaxCacheBytes.
	big := make([]byte, 4096)
	for i := range big {
		big[i] = byte(i)
	}
	m := putRandom(b, big)

	mg, err := New(Config{Backend: b, MaxCacheBytes: 1 << 20, MaxCacheItemBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(mg.Router())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/mem/" + m.String())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(got) != len(big) {
		t.Fatalf("body length: got %d want %d", len(got), len(big))
	}
	for i := range got {
		if got[i] != big[i] {
			t.Fatalf("body mismatch at %d", i)
		}
	}
	// A large item must NOT populate the cache.
	if n := mg.lru.len(); n != 0 {
		t.Errorf("large item should not be cached, cache has %d entries", n)
	}
}

func TestSmallResolvedItem_IsCached(t *testing.T) {
	b := newMemBackend()
	small := []byte("small body under the item cap")
	m := putRandom(b, small)

	mg, err := New(Config{Backend: b, MaxCacheBytes: 1 << 20, MaxCacheItemBytes: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(mg.Router())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/mem/" + m.String())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if n := mg.lru.len(); n != 1 {
		t.Errorf("small item should be cached, cache has %d entries", n)
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestProductionSubdomainSSERoute(t *testing.T) {
	b := newMemBackend()
	missingMID := mid.FromBytes([]byte("test missing sse content"))

	mg, err := New(Config{Backend: b, MemNSResolver: memns.NewResolver(nil, nil, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 1. Production domain request: Host = <mid>.memgate.io, Path = /index.html
	req1 := httptest.NewRequest("GET", "/index.html", nil)
	req1.Host = missingMID.String() + ".memgate.io"
	req1.Header.Set("Accept", "text/html")
	rec1 := httptest.NewRecorder()

	mg.Handler().ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d, body: %s", rec1.Code, rec1.Body.String())
	}
	body1 := rec1.Body.String()
	if !strings.Contains(body1, "/~status") || strings.Contains(body1, "/mem/"+missingMID.String()+"/~status") {
		t.Errorf("expected /~status for production subdomain, got body:\n%s", body1)
	}

	// 2. Path-based request: Host = localhost:8083, Path = /mem/<mid>/index.html
	req2 := httptest.NewRequest("GET", "/mem/"+missingMID.String()+"/index.html", nil)
	req2.Host = "localhost:8083"
	req2.Header.Set("Accept", "text/html")
	rec2 := httptest.NewRecorder()

	mg.Handler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d, body: %s", rec2.Code, rec2.Body.String())
	}
	body2 := rec2.Body.String()
	expectedSSE := "/mem/" + missingMID.String() + "/~status"
	if !strings.Contains(body2, expectedSSE) {
		t.Errorf("expected %s for path gateway request, got body:\n%s", expectedSSE, body2)
	}
}
