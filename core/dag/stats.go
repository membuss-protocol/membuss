package dag

import (
	"errors"
	"fmt"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// Stats summarises the shape and size of a resolved DAG.
type Stats struct {
	// Nodes is the total number of blocks in the DAG (leaves
	// plus internal nodes), counting each distinct MID once.
	Nodes uint64
	// Leaves is the number of leaf (raw content) blocks.
	Leaves uint64
	// Internal is the number of internal (link-only) nodes.
	Internal uint64
	// Depth is the number of node levels from the root to the
	// deepest leaf, inclusive. A single-leaf DAG has depth 1;
	// a root over leaves has depth 2.
	Depth uint64
	// Width is the largest number of children held by any single
	// internal node. Zero for a single-leaf DAG.
	Width uint64
	// Bytes is the sum of the on-disk size of every block,
	// including the internal-node envelopes.
	Bytes uint64
}

// Stat walks the DAG rooted at root and reports its shape and size.
// It reads every block through bs, verifying each block's integrity
// against its claimed MID, exactly as the resolver does. Distinct
// MIDs are counted once even if referenced from multiple parents, so
// deduplicated content is not double-counted.
func Stat(bs store.Blockstore, root mid.MID) (Stats, error) {
	if bs == nil {
		return Stats{}, errors.New("dag: nil blockstore")
	}
	if root.IsZero() {
		return Stats{}, errors.New("dag: zero root MID")
	}

	var st Stats
	seen := make(map[string]struct{})

	// walk returns the depth of the subtree rooted at m (number of
	// node levels from m down to its deepest leaf, inclusive).
	var walk func(m mid.MID) (uint64, error)
	walk = func(m mid.MID) (uint64, error) {
		key := m.String()
		if _, dup := seen[key]; dup {
			// Already counted this block; still need its depth to
			// report the true tree height, but avoid re-counting
			// its bytes/nodes. Re-reading is cheap relative to the
			// correctness of not double-counting dedup'd content.
			return depthOnly(bs, m)
		}
		seen[key] = struct{}{}

		data, err := bs.Get(m)
		if err != nil {
			return 0, fmt.Errorf("dag: get %s: %w", m.String(), err)
		}
		if err := verifyBlock(m, data); err != nil {
			return 0, err
		}

		st.Nodes++
		st.Bytes += uint64(len(data))

		links, internal := isInternalNode(data)
		if !internal {
			st.Leaves++
			return 1, nil
		}
		st.Internal++
		if uint64(len(links)) > st.Width {
			st.Width = uint64(len(links))
		}

		var maxChild uint64
		for _, s := range links {
			child, err := mid.Parse(s)
			if err != nil {
				return 0, fmt.Errorf("dag: parse link %q: %w", s, err)
			}
			d, err := walk(child)
			if err != nil {
				return 0, err
			}
			if d > maxChild {
				maxChild = d
			}
		}
		return maxChild + 1, nil
	}

	depth, err := walk(root)
	if err != nil {
		return Stats{}, err
	}
	st.Depth = depth
	return st, nil
}

// depthOnly returns the subtree depth of an already-counted node
// without incrementing any of the running totals in Stat.
func depthOnly(bs store.Blockstore, m mid.MID) (uint64, error) {
	data, err := bs.Get(m)
	if err != nil {
		return 0, fmt.Errorf("dag: get %s: %w", m.String(), err)
	}
	links, internal := isInternalNode(data)
	if !internal {
		return 1, nil
	}
	var maxChild uint64
	for _, s := range links {
		child, err := mid.Parse(s)
		if err != nil {
			return 0, fmt.Errorf("dag: parse link %q: %w", s, err)
		}
		d, err := depthOnly(bs, child)
		if err != nil {
			return 0, err
		}
		if d > maxChild {
			maxChild = d
		}
	}
	return maxChild + 1, nil
}
