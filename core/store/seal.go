package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/db"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
)

// Seal marks root as a pinned root. If recursive is true, Seal
// also walks the DAG rooted at root to confirm the DAG is
// internally consistent.
func (s *MemStore) Seal(root mid.MID, recursive bool) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()
	if root.IsZero() {
		return errors.New("store: zero MID")
	}

	if err := s.writeSeal(root, false); err != nil {
		return err
	}

	if !recursive {
		return nil
	}
	werr := Walk(s, root, func(m mid.MID, _ bool) error {
		if m.Equal(root) {
			return nil
		}
		if m.Codec() == mid.CodecMemFS {
			_ = s.writeSeal(m, true)
		}
		return nil
	})
	if werr != nil {
		if errors.Is(werr, ErrNotFound) {
			return fmt.Errorf("%w: %v", ErrSealWalkIncomplete, werr)
		}
		return fmt.Errorf("store: seal walk: %w", werr)
	}
	return nil
}

// ErrSealWalkIncomplete signals that a Seal succeeded (the
// pin record is on disk) but the recursive DAG walk did not
// reach every reachable block.
var ErrSealWalkIncomplete = errors.New("store: seal walk incomplete")

// Unseal removes the seal record for the given MID and recursively
// unseals any child MemFS nodes.
func (s *MemStore) Unseal(root mid.MID) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.exit()
	if root.IsZero() {
		return errors.New("store: zero MID")
	}
	err := s.db.Delete(db.SealKey(root))
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}

	walkErr := Walk(s, root, func(m mid.MID, _ bool) error {
		if m.Codec() == mid.CodecMemFS {
			if err := s.db.Delete(db.SealKey(m)); err != nil && !errors.Is(err, db.ErrNotFound) {
				return fmt.Errorf("store: unseal child %s: %w", m.String(), err)
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, ErrNotFound) {
		return walkErr
	}
	return nil
}

// IsSealed reports whether a direct seal record exists for m.
func (s *MemStore) IsSealed(m mid.MID) (bool, error) {
	if err := s.enter(); err != nil {
		return false, err
	}
	defer s.exit()
	if m.IsZero() {
		return false, errors.New("store: zero MID")
	}
	return s.db.Has(db.SealKey(m))
}

// AllSealed returns every MID with a direct seal record.
func (s *MemStore) AllSealed() ([]mid.MID, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.exit()
	var out []mid.MID
	it, err := s.db.NewIter()
	if err != nil {
		return nil, err
	}
	defer it.Close()
	prefix := []byte(db.PrefixSeal)
	for it.SeekGE(prefix); it.Valid() && bytes.HasPrefix(it.Key(), prefix); it.Next() {
		raw := append([]byte(nil), it.Key()...)
		raw = raw[len(prefix):]

		codec := mid.CodecRaw
		isChild := false
		val := it.Value()
		if len(val) == 8 {
			v := binary.BigEndian.Uint64(val)
			isChild = (v & (1 << 63)) != 0
			codec = v &^ (1 << 63)
		}

		if isChild {
			continue
		}

		m, err := mid.FromMultihash(codec, raw)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// GC walks every sealed root, collects the reachable MID set,
// and deletes every key in the store that is NOT in that set
// AND is older than the minimum age.
func (s *MemStore) GC(ctx context.Context) (uint64, error) {
	return s.GCWithMinAge(ctx, 0)
}

// GCWithMinAge is like GC but only deletes blocks whose commit timestamp is older than minAge.
func (s *MemStore) GCWithMinAge(ctx context.Context, minAge time.Duration) (uint64, error) {
	if err := s.enter(); err != nil {
		return 0, err
	}
	defer s.exit()
	if ctx == nil {
		ctx = context.Background()
	}

	reachable := make(map[string]struct{})
	roots, err := s.AllSealed()
	if err != nil {
		return 0, err
	}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if root.IsZero() {
			continue
		}
		reachable[string(root.Bytes())] = struct{}{}
		_ = Walk(s, root, func(m mid.MID, _ bool) error {
			reachable[string(m.Bytes())] = struct{}{}
			return nil
		})
	}

	type pendingDelete struct {
		key   []byte
		bytes uint64
	}
	var toDelete []pendingDelete
	var minAgeTs uint64
	if minAge > 0 {
		minAgeTs = uint64(time.Now().Add(-minAge).Unix())
	}

	it, err := s.db.NewIter()
	if err != nil {
		return 0, err
	}
	defer it.Close()

	for it.First(); it.Valid(); it.Next() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		k := it.Key()
		ks := string(k)
		if len(ks) > len(db.PrefixMeta) && ks[:len(db.PrefixMeta)] == db.PrefixMeta {
			continue
		}
		if len(ks) > len(db.PrefixSeal) && ks[:len(db.PrefixSeal)] == db.PrefixSeal {
			continue
		}
		isBlock := len(ks) > len(db.PrefixBlock) && ks[:len(db.PrefixBlock)] == db.PrefixBlock
		isDAG := len(ks) > len(db.PrefixDAG) && ks[:len(db.PrefixDAG)] == db.PrefixDAG
		if !isBlock && !isDAG {
			continue
		}
		raw := k
		if isBlock {
			raw = k[len(db.PrefixBlock):]
		} else {
			raw = k[len(db.PrefixDAG):]
		}
		if _, ok := reachable[string(raw)]; ok {
			continue
		}
		if minAgeTs > 0 {
			m, merr := mid.FromMultihash(mid.CodecRaw, raw)
			if merr == nil {
				if ts, terr := db.ReadTimestamp(s.db, m); terr == nil && ts >= minAgeTs {
					continue
				}
			}
		}
		toDelete = append(toDelete, pendingDelete{
			key:   append([]byte(nil), k...),
			bytes: uint64(len(k) + len(it.Value())),
		})
	}
	_ = it.Close()

	var freed uint64
	const batchSize = 100
	for i := 0; i < len(toDelete); i += batchSize {
		end := i + batchSize
		if end > len(toDelete) {
			end = len(toDelete)
		}
		batch := s.db.NewBatch()
		for _, pd := range toDelete[i:end] {
			_ = batch.Delete(pd.key)
			freed += pd.bytes
		}
		if err := batch.Commit(); err != nil {
			batch.Close()
			return freed, err
		}
		batch.Close()
	}

	return freed, nil
}

// writeSeal writes a single seal record.
func (s *MemStore) writeSeal(m mid.MID, child bool) error {
	val := make([]byte, 8)
	codecVal := m.Codec()
	if child {
		codecVal |= 1 << 63
	}
	binary.BigEndian.PutUint64(val, codecVal)
	return s.db.Set(db.SealKey(m), val)
}

// DeleteRecursive removes the given root MID and all its reachable children from the store.
func (s *MemStore) DeleteRecursive(root mid.MID) (uint64, uint64, error) {
	if err := s.enter(); err != nil {
		return 0, 0, err
	}
	defer s.exit()
	if root.IsZero() {
		return 0, 0, errors.New("store: zero MID")
	}

	if err := s.Unseal(root); err != nil {
		// Ignore
	}

	reachable := make(map[string]mid.MID)
	var collect func(m mid.MID)
	collect = func(m mid.MID) {
		if m.IsZero() {
			return
		}
		ms := m.String()
		if _, seen := reachable[ms]; seen {
			return
		}
		reachable[ms] = m

		data, err := s.Get(m)
		if err != nil {
			return
		}

		var childMIDs []mid.MID
		if desc, uerr := tryParseDescriptor(data); uerr == nil && desc.RootMid != "" && len(desc.Blocks) > 0 {
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
				case membusspb.MemFSType_DIR:
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
				for _, s := range node.Links {
					child, err := mid.Parse(s)
					if err == nil {
						childMIDs = append(childMIDs, child)
					}
				}
			}
		}

		for _, child := range childMIDs {
			collect(child)
		}
	}

	collect(root)

	var blocksDeleted uint64
	var bytesFreed uint64

	mids := make([]mid.MID, 0, len(reachable))
	for _, m := range reachable {
		mids = append(mids, m)
	}

	const batchSize = 100
	for i := 0; i < len(mids); i += batchSize {
		end := i + batchSize
		if end > len(mids) {
			end = len(mids)
		}
		batchMids := mids[i:end]

		batch := s.db.NewBatch()
		for _, m := range batchMids {
			var size uint64
			deletedThisBlock := false
			for _, key := range [][]byte{db.BlockKey(m), db.DagKey(m)} {
				val, err := s.db.Get(key)
				if err == nil {
					size += uint64(len(val))
					_ = batch.Delete(key)
					deletedThisBlock = true
				}
			}
			if deletedThisBlock {
				blocksDeleted++
				bytesFreed += size
			}

			_ = batch.Delete(db.MetaKey("obj/" + m.String()))
			_ = batch.Delete(db.MetaKey("ts/" + m.String()))
		}
		if err := batch.Commit(); err != nil {
			batch.Close()
			return blocksDeleted, bytesFreed, fmt.Errorf("store: batch delete failed: %w", err)
		}
		batch.Close()
	}

	if s.bloom != nil {
		if rerr := s.bloom.rebuildFromDB(s.db); rerr != nil {
			return blocksDeleted, bytesFreed, fmt.Errorf("store: bloom rebuild after delete: %w", rerr)
		}
	}

	return blocksDeleted, bytesFreed, nil
}

// AllObjectMIDs returns every MID that has an ObjectInfo metadata record where IsRoot is true.
func (s *MemStore) AllObjectMIDs() ([]mid.MID, error) {
	if err := s.enter(); err != nil {
		return nil, err
	}
	defer s.exit()
	var out []mid.MID
	it, err := s.db.NewIter()
	if err != nil {
		return nil, err
	}
	defer it.Close()

	prefix := db.MetaKey("obj/")
	for it.SeekGE(prefix); it.Valid() && bytes.HasPrefix(it.Key(), prefix); it.Next() {
		var info ObjectInfo
		if err := json.Unmarshal(it.Value(), &info); err != nil {
			continue
		}
		if !info.IsRoot {
			continue
		}

		k := it.Key()
		midStr := string(k[len(prefix):])
		m, err := mid.Parse(midStr)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}
