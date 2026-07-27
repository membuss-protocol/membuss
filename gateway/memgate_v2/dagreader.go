package memgate_v2

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
	"google.golang.org/protobuf/proto"
)

type dagBlock struct {
	mid    mid.MID
	size   int64
	offset int64
}

// defaultBlockListCacheEntries bounds how many distinct roots'
// flattened block lists are cached at once.
const defaultBlockListCacheEntries = 512

// blockListEntry is an immutable, fully-built flattened block list
// for one root MID. Because MIDs are content hashes, the mapping
// from root to block list never changes, so entries never need
// invalidation — only eviction under the size bound.
type blockListEntry struct {
	blocks    []dagBlock
	totalSize int64
}

// blockListCache memoizes buildBlockList results keyed by root MID
// string. Range requests to a large file would otherwise re-walk
// and re-deserialize the entire DAG on every request; this turns
// that into a single build per root. Eviction is simple LRU by
// access order, bounded by maxEntries.
type blockListCache struct {
	mu         sync.Mutex
	maxEntries int
	entries    map[string]*blockListNode
	head       *blockListNode // most-recently-used
	tail       *blockListNode // least-recently-used
}

type blockListNode struct {
	key   string
	entry *blockListEntry
	prev  *blockListNode
	next  *blockListNode
}

func newBlockListCache(maxEntries int) *blockListCache {
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &blockListCache{
		maxEntries: maxEntries,
		entries:    make(map[string]*blockListNode),
	}
}

func (c *blockListCache) get(key string) (*blockListEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	node, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.moveToFront(node)
	return node.entry, true
}

func (c *blockListCache) put(key string, e *blockListEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if node, ok := c.entries[key]; ok {
		node.entry = e
		c.moveToFront(node)
	} else {
		node := &blockListNode{key: key, entry: e}
		c.entries[key] = node
		c.pushFront(node)
	}
	for len(c.entries) > c.maxEntries && c.tail != nil {
		oldest := c.tail
		c.remove(oldest)
		delete(c.entries, oldest.key)
	}
}

func (c *blockListCache) moveToFront(node *blockListNode) {
	if c.head == node {
		return
	}
	c.remove(node)
	c.pushFront(node)
}

func (c *blockListCache) pushFront(node *blockListNode) {
	node.prev = nil
	node.next = c.head
	if c.head != nil {
		c.head.prev = node
	}
	c.head = node
	if c.tail == nil {
		c.tail = node
	}
}

func (c *blockListCache) remove(node *blockListNode) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		c.head = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		c.tail = node.prev
	}
	node.prev, node.next = nil, nil
}

// cachedBlockList returns the flattened block list for root, building
// it via buildBlockList on a cache miss and memoizing the result.
// The returned slice is shared read-only: dagReader only ever reads
// from r.blocks, so sharing is safe and avoids a per-request copy.
func (m *MemGate) cachedBlockList(ctx context.Context, root mid.MID) ([]dagBlock, int64, error) {
	key := root.String()
	if m.blockLists != nil {
		if e, ok := m.blockLists.get(key); ok {
			return e.blocks, e.totalSize, nil
		}
	}
	blocks, total, err := buildBlockList(ctx, m.cfg.Backend, root)
	if err != nil {
		return nil, 0, err
	}
	if m.blockLists != nil {
		m.blockLists.put(key, &blockListEntry{blocks: blocks, totalSize: total})
	}
	return blocks, total, nil
}

func buildBlockList(ctx context.Context, backend Backend, root mid.MID) ([]dagBlock, int64, error) {
	raw, err := backend.RawBlock(ctx, root)
	if err != nil {
		return nil, 0, err
	}

	var blocks []dagBlock
	var offset int64

	if root.Codec() == mid.CodecMemFS {
		var node membusspb.MemFSNode
		if err := proto.Unmarshal(raw, &node); err != nil {
			return nil, 0, err
		}
		if node.Type != membusspb.MemFSType_FILE {
			return nil, 0, fmt.Errorf("memfs node is not a file")
		}

		var walkMemFS func(n *membusspb.MemFSNode) error
		walkMemFS = func(n *membusspb.MemFSNode) error {
			for _, b := range n.Blocks {
				if b == nil || len(b.Mid) == 0 {
					continue
				}
				var codec uint64 = mid.CodecMemFS
				if b.Size > 0 {
					codec = mid.CodecRaw
				}
				childMID, err := mid.FromMultihash(codec, b.Mid)
				if err != nil {
					return err
				}

				if b.Size > 0 {
					blocks = append(blocks, dagBlock{
						mid:    childMID,
						size:   int64(b.Size),
						offset: offset,
					})
					offset += int64(b.Size)
				} else {
					childRaw, err := backend.RawBlock(ctx, childMID)
					if err != nil {
						return err
					}
					var childNode membusspb.MemFSNode
					if err := proto.Unmarshal(childRaw, &childNode); err != nil {
						return err
					}
					if err := walkMemFS(&childNode); err != nil {
						return err
					}
				}
			}
			return nil
		}

		if err := walkMemFS(&node); err != nil {
			return nil, 0, err
		}
		return blocks, offset, nil
	} else {
		var walkRawDAG func(curr mid.MID, rawBytes []byte) error
		walkRawDAG = func(curr mid.MID, rawBytes []byte) error {
			var node membusspb.DAGNode
			if err := proto.Unmarshal(rawBytes, &node); err == nil && len(node.Links) > 0 {
				for _, linkStr := range node.Links {
					child, err := mid.Parse(linkStr)
					if err != nil {
						return err
					}
					childRaw, err := backend.RawBlock(ctx, child)
					if err != nil {
						return err
					}
					if err := walkRawDAG(child, childRaw); err != nil {
						return err
					}
				}
				return nil
			}

			size := int64(len(rawBytes))
			blocks = append(blocks, dagBlock{
				mid:    curr,
				size:   size,
				offset: offset,
			})
			offset += size
			return nil
		}

		if err := walkRawDAG(root, raw); err != nil {
			return nil, 0, err
		}
		return blocks, offset, nil
	}
}

type dagReader struct {
	ctx         context.Context
	backend     Backend
	blocks      []dagBlock
	totalSize   int64
	pos         int64
	curBlockIdx int
	curBlockBuf []byte
}

func newDagReader(ctx context.Context, backend Backend, blocks []dagBlock, totalSize int64) *dagReader {
	return &dagReader{
		ctx:         ctx,
		backend:     backend,
		blocks:      blocks,
		totalSize:   totalSize,
		curBlockIdx: -1,
	}
}

func (r *dagReader) Read(p []byte) (int, error) {
	if r.pos >= r.totalSize {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	idx := r.findBlockIndex(r.pos)
	if idx < 0 || idx >= len(r.blocks) {
		return 0, io.EOF
	}

	if r.curBlockIdx != idx || r.curBlockBuf == nil {
		block := r.blocks[idx]
		data, err := r.backend.RawBlock(r.ctx, block.mid)
		if err != nil {
			return 0, fmt.Errorf("read block %s: %w", block.mid.String(), err)
		}
		r.curBlockIdx = idx
		r.curBlockBuf = data
	}

	block := r.blocks[idx]
	offsetInBlock := r.pos - block.offset
	if offsetInBlock >= int64(len(r.curBlockBuf)) {
		return 0, io.EOF
	}
	n := copy(p, r.curBlockBuf[offsetInBlock:])
	r.pos += int64(n)
	return n, nil
}

func (r *dagReader) Seek(offset int64, whence int) (int64, error) {
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = r.pos + offset
	case io.SeekEnd:
		target = r.totalSize + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}
	if target < 0 {
		return 0, fmt.Errorf("negative seek target: %d", target)
	}
	r.pos = target
	return r.pos, nil
}

func (r *dagReader) Close() error {
	r.curBlockBuf = nil
	return nil
}

// findBlockIndex returns the index of the block containing byte
// position pos, or -1 if no block covers it. Blocks are appended in
// strictly increasing offset order (offset += size), so they are
// sorted by offset and a binary search finds the covering block in
// O(log n) instead of scanning every block on every Read.
func (r *dagReader) findBlockIndex(pos int64) int {
	// Find the first block whose offset is strictly greater than pos;
	// the candidate covering block is the one immediately before it.
	i := sort.Search(len(r.blocks), func(i int) bool {
		return r.blocks[i].offset > pos
	})
	idx := i - 1
	if idx < 0 {
		return -1
	}
	b := r.blocks[idx]
	if pos >= b.offset && pos < b.offset+b.size {
		return idx
	}
	return -1
}
