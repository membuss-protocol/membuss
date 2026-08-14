package store

import (
	"bytes"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/mid"

	membusspb "github.com/nnlgsakib/membuss/proto"
)

func tryParseDescriptor(data []byte) (*membusspb.DescriptorPayload, error) {
	var desc membusspb.DescriptorPayload
	if err := proto.Unmarshal(data, &desc); err == nil && desc.RootMid != "" {
		return &desc, nil
	}
	if len(data) < 5+32 {
		return nil, errors.New("descriptor: too short")
	}
	if !bytes.Equal(data[:4], []byte{'M', 'E', 'M', 'B'}) {
		return nil, errors.New("descriptor: invalid magic")
	}
	if data[4] != 1 {
		return nil, errors.New("descriptor: unsupported version")
	}
	payload := data[5 : len(data)-32]
	var descWrapped membusspb.DescriptorPayload
	if err := proto.Unmarshal(payload, &descWrapped); err == nil && descWrapped.RootMid != "" {
		return &descWrapped, nil
	}
	return nil, errors.New("descriptor: failed to parse wrapped")
}

// Walk visits every MID reachable from root in depth-first order
// (root first, then children in link order). For each visited
// node the visit callback is invoked with the MID and a flag
// indicating whether the node is a leaf (true) or an internal
// node (false). If visit returns an error, Walk stops and returns
// the same error.
//
// Walk is the building block for Seal/GC: callers accumulate the
// visited MIDs into a set and then operate on the set.
//
// Walk lives in the store package (not core/dag) to avoid an
// import cycle, since the store depends on a DAG-walking helper
// for GC. The walker itself only depends on Blockstore, which is
// the minimum interface any DAG-aware code needs.
// BlockGetter is the subset of Blockstore needed to walk a DAG.
type BlockGetter interface {
	Get(m mid.MID) ([]byte, error)
}

func Walk(bs BlockGetter, root mid.MID, visit func(m mid.MID, leaf bool) error) error {
	return WalkOptions(bs, root, false, visit)
}

func WalkOptions(bs BlockGetter, root mid.MID, ignoreMissing bool, visit func(m mid.MID, leaf bool) error) error {
	if bs == nil {
		return errors.New("store: nil blockstore")
	}
	if root.IsZero() {
		return errors.New("store: zero root MID")
	}

	visited := make(map[string]struct{})

	var walk func(m mid.MID) error
	walk = func(m mid.MID) error {
		key := m.String()
		if _, ok := visited[key]; ok {
			return nil
		}
		visited[key] = struct{}{}

		var size int64 = -1
		if checker, ok := bs.(interface {
			GetSize(mid.MID) (int64, error)
		}); ok {
			var err error
			size, err = checker.GetSize(m)
			if err != nil {
				if ignoreMissing && (errors.Is(err, ErrNotFound) || (err != nil && (err.Error() == "store: block not found" || errors.Is(errors.Unwrap(err), ErrNotFound)))) {
					return nil
				}
				return fmt.Errorf("store: walk size %s: %w", m.String(), err)
			}
		}

		if m.Codec() == mid.CodecRaw && size > 16384 {
			// Large raw data block is a leaf node; skip loading payload
			return visit(m, true)
		}

		data, err := bs.Get(m)
		if err != nil {
			if ignoreMissing && (errors.Is(err, ErrNotFound) || (err != nil && (err.Error() == "store: block not found" || errors.Is(errors.Unwrap(err), ErrNotFound)))) {
				return nil
			}
			return fmt.Errorf("store: walk get %s: %w", m.String(), err)
		}

		if m.Codec() == mid.CodecRaw && int64(len(data)) > 16384 {
			// Fallback case: large raw block loaded, skip parsing, it is a leaf
			return visit(m, true)
		}

		var childMIDs []mid.MID
		var isInternal bool

		if desc, uerr := tryParseDescriptor(data); uerr == nil && desc.RootMid != "" && len(desc.Blocks) > 0 {
			isInternal = true
			if rMID, err := mid.Parse(desc.RootMid); err == nil {
				childMIDs = append(childMIDs, rMID)
			}
			for _, b := range desc.Blocks {
				if m, err := mid.Parse(b.Mid); err == nil {
					childMIDs = append(childMIDs, m)
				}
			}
		} else if m.Codec() == mid.CodecMemFS {
			var node membusspb.MemFSNode
			if uerr := proto.Unmarshal(data, &node); uerr == nil {
				switch node.Type {
				case membusspb.MemFSType_FILE:
					if len(node.Data) > 0 && len(node.Blocks) <= 1 {
						// Single inline block file is self-contained leaf
						isInternal = false
					} else {
						isInternal = len(node.Blocks) > 0
						for _, b := range node.Blocks {
							if b == nil || len(b.Mid) == 0 {
								continue
							}
							var codec uint64 = mid.CodecMemFS
							if b.Size > 0 {
								codec = mid.CodecRaw
							}
							child, err := mid.FromMultihash(codec, b.Mid)
							if err == nil {
								childMIDs = append(childMIDs, child)
							}
						}
					}
				case membusspb.MemFSType_DIR:
					isInternal = len(node.Entries) > 0
					for _, e := range node.Entries {
						if e == nil || len(e.Mid) == 0 {
							continue
						}
						var codec uint64 = mid.CodecMemFS
						if e.Type == membusspb.MemFSType_RAW {
							codec = mid.CodecRaw
						}
						child, err := mid.FromMultihash(codec, e.Mid)
						if err == nil {
							childMIDs = append(childMIDs, child)
						}
					}
				}
			}
		} else {
			var node membusspb.DAGNode
			if uerr := proto.Unmarshal(data, &node); uerr == nil && len(node.Links) > 0 {
				isInternal = true
				for _, s := range node.Links {
					child, err := mid.Parse(s)
					if err == nil {
						childMIDs = append(childMIDs, child)
					}
				}
			}
		}

		if isInternal {
			if err := visit(m, false); err != nil {
				return err
			}
			for _, child := range childMIDs {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}

		return visit(m, true)
	}

	return walk(root)
}
