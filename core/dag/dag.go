// Package dag builds and resolves Merkle DAGs over content
// produced by core/chunk.
//
// A DAG has two kinds of nodes:
//
//   - Leaf nodes carry the raw bytes of a single chunk and have
//     no children.
//   - Internal nodes carry only MIDs of their children, serialized
//     as a DAGNode protobuf message.
//
// The MID of a leaf is the multihash of the raw chunk bytes (see
// core/mid). The MID of an internal node is the multihash of the
// canonical DAGNode form (protobuf-marshaled with the Mid field
// unset). The on-disk block is that same canonical form, so the
// Blockstore integrity check (which re-hashes on write) passes by
// construction, and the Resolver re-hashes again on read so that
// corrupted or substituted blocks are never served.
//
// Build is bottom-up and fully streaming: it consumes one chunk at
// a time and collapses each tree level into a parent as soon as it
// fills to Fanout children, so peak memory is O(Fanout · depth)
// rather than O(leafCount). The tree shape — and therefore the root
// MID — is identical to a level-complete batch reduction: N chunks
// collapse into ceil(N / Fanout) internal nodes, which collapse
// again until a single root remains. A single-chunk input collapses
// to a single leaf whose MID is the root.
package dag

import (
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"

	membusspb "github.com/nnlgsakib/membuss/proto"
)

// Fanout is the maximum number of children an internal node may
// hold.
const Fanout = 174

// Node is the runtime representation of a DAG node. The Mid
// field is set to the node's MID after construction.
type Node struct {
	membusspb.DAGNode
}

// Links returns the MIDs of the children of this node.
func (n *Node) Links() []mid.MID {
	out := make([]mid.MID, 0, len(n.DAGNode.Links))
	for _, s := range n.DAGNode.Links {
		out = append(out, mid.MustParse(s))
	}
	return out
}

// IsLeaf reports whether this node holds inline data and no links.
func (n *Node) IsLeaf() bool {
	return len(n.DAGNode.Links) == 0
}

// MID returns the MID of this node.
func (n *Node) MID() mid.MID {
	if n == nil || n.DAGNode.Mid == "" {
		return mid.MID{}
	}
	return mid.MustParse(n.DAGNode.Mid)
}

// Builder constructs a Merkle DAG over a sequence of chunks and
// writes the resulting nodes into the supplied Blockstore.
type Builder struct {
	bs store.Blockstore
}

// NewBuilder returns a Builder that writes into bs.
func NewBuilder(bs store.Blockstore) *Builder {
	return &Builder{bs: bs}
}

// Build consumes all blocks from c, writes every block (leaf and
// internal) into the Blockstore, and returns the MID of the root.
//
// Build is fully streaming: it reads one chunk at a time and never
// materialises the complete list of leaf MIDs. Each leaf is written
// immediately and pushed into a per-level buffer that flushes into a
// parent node as soon as it fills to Fanout children. Peak memory is
// therefore O(Fanout · treeDepth) — a few kilobytes even for a
// terabyte input — rather than O(leafCount).
//
// The tree produced is byte-identical to a level-complete bottom-up
// reduction: because leaves arrive in order and each level flushes
// at exactly Fanout, the grouping boundaries ([0,F), [F,2F), …) and
// the trailing partial group match the batch algorithm exactly, so
// the root MID is unchanged. A single-chunk input collapses to the
// leaf itself (no wrapping internal node).
func (b *Builder) Build(c chunk.Chunker) (mid.MID, error) {
	if b.bs == nil {
		return mid.MID{}, errors.New("dag: nil blockstore")
	}
	if c == nil {
		return mid.MID{}, errors.New("dag: nil chunker")
	}

	bld := &levelStack{bs: b.bs}
	sawLeaf := false
	for {
		blk, err := c.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return mid.MID{}, fmt.Errorf("dag: read chunk: %w", err)
		}
		if blk.Size() == 0 {
			return mid.MID{}, errors.New("dag: empty chunk")
		}
		leafMID := blk.MID()
		if leafMID.IsZero() {
			return mid.MID{}, errors.New("dag: chunk has zero MID")
		}
		if err := b.bs.Put(leafMID, blk.Data()); err != nil {
			return mid.MID{}, fmt.Errorf("dag: store leaf: %w", err)
		}
		if err := bld.push(0, leafMID); err != nil {
			return mid.MID{}, err
		}
		sawLeaf = true
	}
	if !sawLeaf {
		return mid.MID{}, errors.New("dag: empty input")
	}
	return bld.finalize()
}

// levelStack accumulates MIDs bottom-up, one buffer per tree level.
// Each buffer holds at most Fanout entries; on overflow it is
// collapsed into a single parent MID that is pushed one level up.
type levelStack struct {
	bs     store.Blockstore
	levels [][]mid.MID
}

// push appends m to the buffer for the given level. If that buffer
// reaches Fanout entries it is immediately collapsed into a parent
// node (written to the blockstore) that is pushed to level+1, which
// may cascade further.
func (s *levelStack) push(level int, m mid.MID) error {
	for len(s.levels) <= level {
		s.levels = append(s.levels, nil)
	}
	s.levels[level] = append(s.levels[level], m)
	if len(s.levels[level]) < Fanout {
		return nil
	}
	parent, err := s.collapse(s.levels[level])
	if err != nil {
		return err
	}
	s.levels[level] = s.levels[level][:0]
	return s.push(level+1, parent)
}

// finalize flushes the remaining partial buffers bottom-up and
// returns the single root MID. A lone surviving node is returned
// as-is (never wrapped), matching the batch algorithm's
// "reduce while len > 1" termination.
func (s *levelStack) finalize() (mid.MID, error) {
	for i := 0; i < len(s.levels); i++ {
		if len(s.levels[i]) == 0 {
			continue
		}
		// If no higher level holds anything and this level has a
		// single node, that node is the root — return it without
		// wrapping it in a redundant internal node.
		if len(s.levels[i]) == 1 && s.topmost(i) {
			return s.levels[i][0], nil
		}
		parent, err := s.collapse(s.levels[i])
		if err != nil {
			return mid.MID{}, err
		}
		s.levels[i] = s.levels[i][:0]
		if err := s.push(i+1, parent); err != nil {
			return mid.MID{}, err
		}
	}
	return mid.MID{}, errors.New("dag: empty input")
}

// topmost reports whether every level above i is empty.
func (s *levelStack) topmost(i int) bool {
	for j := i + 1; j < len(s.levels); j++ {
		if len(s.levels[j]) > 0 {
			return false
		}
	}
	return true
}

// collapse builds an internal node over the given children and
// writes it to the blockstore. The on-disk representation is the
// canonical DAGNode protobuf form (with the Mid field unset); the
// resolver recognises this form and re-attaches the MID at decode
// time.
func (s *levelStack) collapse(children []mid.MID) (mid.MID, error) {
	if len(children) == 0 {
		return mid.MID{}, errors.New("dag: collapse called on empty group")
	}
	links := make([]string, len(children))
	for i, c := range children {
		links[i] = c.String()
	}
	canonical := &membusspb.DAGNode{Links: links}
	raw, err := proto.Marshal(canonical)
	if err != nil {
		return mid.MID{}, fmt.Errorf("dag: marshal node: %w", err)
	}
	nodeMID := mid.FromBytes(raw)
	if err := s.bs.Put(nodeMID, raw); err != nil {
		return mid.MID{}, fmt.Errorf("dag: store internal: %w", err)
	}
	return nodeMID, nil
}
