package memfs

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/erasure"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"

	membusspb "github.com/nnlgsakib/membuss/proto"
)

// DefaultBlockSize is the default chunk size for new Builders.
// It matches core/chunk.DefaultBlockSize (256 KiB).
const DefaultBlockSize = chunk.DefaultBlockSize

// Builder constructs MemFS trees and writes them into a
// Blockstore. It reuses the existing chunker for raw blocks
// and the existing Blockstore Put path for everything else,
// so the dedup, walk, seal and GC machinery all just work.
type Builder struct {
	bs            store.Blockstore
	blk           int
	chunker       string
	enableErasure bool
}

// NewBuilder returns a Builder that writes into bs. The
// default block size is DefaultBlockSize.
func NewBuilder(bs store.Blockstore) *Builder {
	return &Builder{bs: bs, blk: DefaultBlockSize}
}

// WithBlockSize returns a copy of b with a different chunk
// size. Values outside [chunk.MinBlockSize, chunk.MaxBlockSize]
// are clamped to the nearest bound.
func (b *Builder) WithBlockSize(n int) *Builder {
	if n < chunk.MinBlockSize {
		n = chunk.MinBlockSize
	}
	if n > chunk.MaxBlockSize {
		n = chunk.MaxBlockSize
	}
	cp := *b
	cp.blk = n
	return &cp
}

// WithChunker returns a copy of b with a different chunker
// strategy (e.g. "fixed", "rabin", "fastcdc").
func (b *Builder) WithChunker(chunker string) *Builder {
	cp := *b
	cp.chunker = chunker
	return &cp
}

// WithErasure enables inline Reed-Solomon erasure coding during chunk ingestion.
func (b *Builder) WithErasure(enable bool) *Builder {
	cp := *b
	cp.enableErasure = enable
	return &cp
}

// AddResult is what AddFile / AddDir return on success.
type AddResult struct {
	MID   mid.MID
	Size  uint64
	Block uint64
}

// blockRef is the internal (MID, size) pair produced by the
// chunker pass before it gets folded into a MemFS FILE node.
type blockRef struct {
	mid  mid.MID
	size uint64
}

// AddFile ingests a file from r, chunks it, stores every raw
// block in the Blockstore, and assembles a MemFS FILE node
// that references those blocks in order. The result is the
// root MID of the file.
//
//   - 1 block (the entire file fits in one chunk): the
//     MemFS FILE node carries the bytes inline in its data
//     field. The raw block is still stored under /b/ for
//     network-level fetch.
//
//   - ≤ fanout blocks (≤ 174 with the default 256 KiB
//     chunker, i.e. ≤ ~43 MiB): one MemFS FILE node with
//     a list of raw-block references.
//
//   - > fanout blocks: a balanced two-level tree of MemFS
//     FILE nodes, exactly like the dag.Builder's reduceLevel.
//
// AddFile also writes the FILE node itself to the Blockstore
// before returning, so callers can Seal the result immediately
// and peers can fetch it.
func (b *Builder) AddFile(name string, r io.Reader, mode fs.FileMode, mtime time.Time, mime string) (AddResult, error) {
	if b.bs == nil {
		return AddResult{}, errors.New("memfs: nil blockstore")
	}
	if r == nil {
		return AddResult{}, errors.New("memfs: nil reader")
	}

	var factory chunk.ChunkerFactory
	switch b.chunker {
	case "rabin":
		factory = chunk.NewRabin()
	case "fastcdc":
		factory = chunk.NewFastCDC()
	default:
		factory = chunk.NewFixed(b.blk)
	}

	r = bufio.NewReaderSize(r, 1<<20)
	chunker, err := factory(r)
	if err != nil {
		return AddResult{}, fmt.Errorf("memfs: chunker: %w", err)
	}
	type chunkWork struct {
		idx  uint64
		data []byte
	}
	type chunkResult struct {
		idx  uint64
		mid  mid.MID
		size uint64
		data []byte
		err  error
	}

	numWorkers := runtime.NumCPU()
	if numWorkers < 2 {
		numWorkers = 2
	}

	workCh := make(chan chunkWork, numWorkers*2)
	resCh := make(chan chunkResult, numWorkers*2)

	var workerWg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			var lastCfg erasure.Config
			var lastEnc *erasure.Encoder

			for work := range workCh {
				lm := mid.FromBytes(work.data)
				if err := b.bs.Put(lm, work.data); err != nil {
					resCh <- chunkResult{idx: work.idx, err: fmt.Errorf("memfs: store raw block: %w", err)}
					continue
				}
				if b.enableErasure {
					cfg := erasure.AdaptiveConfig(int64(len(work.data)))
					if lastEnc == nil || cfg != lastCfg {
						if enc, eerr := erasure.NewEncoder(cfg); eerr == nil {
							lastEnc = enc
							lastCfg = cfg
						}
					}
					if lastEnc != nil {
						if encoded, encErr := lastEnc.Encode(work.data); encErr == nil && encoded != nil {
							for _, shard := range encoded.Shards {
								_ = b.bs.Put(shard.ShardMID, shard.Data)
							}
							_ = erasure.SetManifest(b.bs, lm, encoded.Manifest)
						}
					}
				}
				resCh <- chunkResult{
					idx:  work.idx,
					mid:  lm,
					size: uint64(len(work.data)),
					data: work.data,
				}
			}
		}()
	}

	go func() {
		workerWg.Wait()
		close(resCh)
	}()

	go func() {
		var idx uint64
		for {
			blk, err := chunker.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				resCh <- chunkResult{err: fmt.Errorf("memfs: read chunk: %w", err)}
				break
			}
			dataCopy := make([]byte, len(blk.Data()))
			copy(dataCopy, blk.Data())
			workCh <- chunkWork{idx: idx, data: dataCopy}
			idx++
		}
		close(workCh)
	}()

	bld := &memfsLevelStack{bs: b.bs, name: name, mode: mode, mtime: mtime, mime: mime, maxBlk: b.blk}
	pending := make(map[uint64]chunkResult)
	var nextIdx uint64

	for res := range resCh {
		if res.err != nil {
			return AddResult{}, res.err
		}
		pending[res.idx] = res
		for {
			item, ok := pending[nextIdx]
			if !ok {
				break
			}
			delete(pending, nextIdx)
			if err := bld.pushLeaf(item.mid, item.size, item.data); err != nil {
				return AddResult{}, err
			}
			nextIdx++
		}
	}

	return bld.finalize()
}

type memfsBlockRef struct {
	mid  mid.MID
	size uint64
}

type memfsLevelStack struct {
	bs         store.Blockstore
	name       string
	mode       fs.FileMode
	mtime      time.Time
	mime       string
	maxBlk     int
	levels     [][]memfsBlockRef
	totalSize  uint64
	totalLeafs uint64
	singleData []byte
}

func (s *memfsLevelStack) pushLeaf(m mid.MID, size uint64, rawData []byte) error {
	s.totalSize += size
	s.totalLeafs++
	if s.totalLeafs == 1 {
		s.singleData = rawData
	} else {
		s.singleData = nil
	}
	return s.push(0, memfsBlockRef{mid: m, size: size})
}

func (s *memfsLevelStack) push(level int, ref memfsBlockRef) error {
	for len(s.levels) <= level {
		s.levels = append(s.levels, nil)
	}
	s.levels[level] = append(s.levels[level], ref)
	if len(s.levels[level]) < dag.Fanout {
		return nil
	}
	parentRef, err := s.collapse(level, s.levels[level])
	if err != nil {
		return err
	}
	s.levels[level] = s.levels[level][:0]
	return s.push(level+1, parentRef)
}

func (s *memfsLevelStack) collapse(level int, children []memfsBlockRef) (memfsBlockRef, error) {
	if len(children) == 0 {
		return memfsBlockRef{}, errors.New("memfs: collapse called on empty group")
	}
	var groupSize uint64
	blocks := make([]*membusspb.MemFSBlock, len(children))
	for i, c := range children {
		var sz uint64
		if level == 0 {
			sz = c.size
		}
		blocks[i] = &membusspb.MemFSBlock{
			Mid:  c.mid.Bytes(),
			Size: sz,
		}
		groupSize += c.size
	}
	node := &membusspb.MemFSNode{
		Type:     membusspb.MemFSType_FILE,
		FileSize: groupSize,
		Blocks:   blocks,
	}
	raw, err := proto.Marshal(node)
	if err != nil {
		return memfsBlockRef{}, fmt.Errorf("memfs: marshal intermediate: %w", err)
	}
	nodeMID := mid.FromBytesWithCodec(raw, mid.CodecMemFS)
	if err := s.bs.Put(nodeMID, raw); err != nil {
		return memfsBlockRef{}, fmt.Errorf("memfs: store intermediate: %w", err)
	}
	return memfsBlockRef{mid: nodeMID, size: groupSize}, nil
}

func (s *memfsLevelStack) topmost(i int) bool {
	for j := i + 1; j < len(s.levels); j++ {
		if len(s.levels[j]) > 0 {
			return false
		}
	}
	return true
}

func (s *memfsLevelStack) finalize() (AddResult, error) {
	if s.totalLeafs == 0 {
		// Empty file node
		pb := &membusspb.MemFSNode{
			Type:     membusspb.MemFSType_FILE,
			FileSize: 0,
			Mode:     uint32(s.mode),
		}
		if s.mtime.UnixNano() > 0 {
			pb.Mtime = s.mtime.UnixNano()
		}
		if s.mime != "" {
			pb.Meta = &membusspb.MemFSMeta{MimeType: s.mime}
		}
		raw, err := proto.Marshal(pb)
		if err != nil {
			return AddResult{}, fmt.Errorf("memfs: marshal empty file node: %w", err)
		}
		rootMID := mid.FromBytesWithCodec(raw, mid.CodecMemFS)
		if err := s.bs.Put(rootMID, raw); err != nil {
			return AddResult{}, fmt.Errorf("memfs: store empty file node: %w", err)
		}
		if s.name != "" {
			if err := store.SetObjectInfo(s.bs, rootMID, store.ObjectInfo{
				Name:     s.name,
				MimeType: s.mime,
				Size:     0,
			}); err != nil {
				return AddResult{}, fmt.Errorf("memfs: set objectinfo: %w", err)
			}
		}
		return AddResult{
			MID:   rootMID,
			Size:  0,
			Block: 0,
		}, nil
	}
	for i := 0; i < len(s.levels); i++ {
		if len(s.levels[i]) == 0 {
			continue
		}
		if s.topmost(i) {
			return s.buildRoot(i, s.levels[i])
		}
		parentRef, err := s.collapse(i, s.levels[i])
		if err != nil {
			return AddResult{}, err
		}
		s.levels[i] = s.levels[i][:0]
		if err := s.push(i+1, parentRef); err != nil {
			return AddResult{}, err
		}
	}
	return AddResult{}, errors.New("memfs: empty file")
}

func (s *memfsLevelStack) buildRoot(level int, children []memfsBlockRef) (AddResult, error) {
	pb := &membusspb.MemFSNode{
		Type:     membusspb.MemFSType_FILE,
		FileSize: s.totalSize,
		Mode:     uint32(s.mode),
	}
	if s.mtime.UnixNano() > 0 {
		pb.Mtime = s.mtime.UnixNano()
	}
	if s.mime != "" {
		pb.Meta = &membusspb.MemFSMeta{MimeType: s.mime}
	}

	if s.totalLeafs == 1 && len(s.singleData) > 0 && len(s.singleData) <= s.maxBlk {
		pb.Data = s.singleData
		pb.Blocks = []*membusspb.MemFSBlock{
			{Mid: children[0].mid.Bytes(), Size: children[0].size},
		}
	} else {
		pb.Blocks = make([]*membusspb.MemFSBlock, len(children))
		for i, c := range children {
			var sz uint64
			if level == 0 {
				sz = c.size
			}
			pb.Blocks[i] = &membusspb.MemFSBlock{Mid: c.mid.Bytes(), Size: sz}
		}
	}

	raw, err := proto.Marshal(pb)
	if err != nil {
		return AddResult{}, fmt.Errorf("memfs: marshal root file node: %w", err)
	}
	rootMID := mid.FromBytesWithCodec(raw, mid.CodecMemFS)
	if err := s.bs.Put(rootMID, raw); err != nil {
		return AddResult{}, fmt.Errorf("memfs: store root file node: %w", err)
	}
	if s.name != "" {
		if err := store.SetObjectInfo(s.bs, rootMID, store.ObjectInfo{
			Name:     s.name,
			MimeType: s.mime,
			Size:     s.totalSize,
		}); err != nil {
			return AddResult{}, fmt.Errorf("memfs: set objectinfo: %w", err)
		}
	}
	return AddResult{
		MID:   rootMID,
		Size:  s.totalSize,
		Block: s.totalLeafs,
	}, nil
}

// AddDir assembles a DIR node from a pre-built list of
// entries, stores it in the Blockstore, and returns its MID
// plus the cumulative size (sum of entry sizes). The entries
// slice is sorted lexicographically by name before being
// serialized, so callers can pass them in any order.
func (b *Builder) AddDir(name string, entries []DirEntry, mode fs.FileMode, mtime time.Time) (AddResult, error) {
	if b.bs == nil {
		return AddResult{}, errors.New("memfs: nil blockstore")
	}
	if entries == nil {
		entries = []DirEntry{}
	}
	// Defensive copy + sort so two callers producing the
	// same logical directory always get the same MID.
	sorted := make([]DirEntry, len(entries))
	copy(sorted, entries)
	sortDirEntries(sorted)

	pb := &membusspb.MemFSNode{
		Type: membusspb.MemFSType_DIR,
		Mode: uint32(mode),
	}
	if mtime.UnixNano() > 0 {
		pb.Mtime = mtime.UnixNano()
	}
	pb.Entries = make([]*membusspb.DirEntry, len(sorted))
	for i, e := range sorted {
		pb.Entries[i] = &membusspb.DirEntry{
			Name: e.Name,
			Mid:  e.Mid.Bytes(),
			Type: e.Type,
			Size: e.Size,
		}
	}

	var total uint64
	for _, e := range sorted {
		total += e.Size
	}

	raw, err := proto.Marshal(pb)
	if err != nil {
		return AddResult{}, fmt.Errorf("memfs: marshal dir: %w", err)
	}
	rootMID := mid.FromBytesWithCodec(raw, mid.CodecMemFS)
	if err := b.bs.Put(rootMID, raw); err != nil {
		return AddResult{}, fmt.Errorf("memfs: store dir: %w", err)
	}
	if name != "" {
		if err := store.SetObjectInfo(b.bs, rootMID, store.ObjectInfo{
			Name:     name,
			MimeType: "inode/directory",
			Size:     total,
		}); err != nil {
			return AddResult{}, fmt.Errorf("memfs: set objectinfo: %w", err)
		}
	}
	return AddResult{
		MID:   rootMID,
		Size:  total,
		Block: 1,
	}, nil
}

// AddSymlink stores a SYMLINK node pointing at target and
// returns its MID.
func (b *Builder) AddSymlink(name, target string, mode fs.FileMode, mtime time.Time) (AddResult, error) {
	if b.bs == nil {
		return AddResult{}, errors.New("memfs: nil blockstore")
	}
	pb := &membusspb.MemFSNode{
		Type:          membusspb.MemFSType_SYMLINK,
		SymlinkTarget: target,
		Mode:          uint32(mode),
	}
	if mtime.UnixNano() > 0 {
		pb.Mtime = mtime.UnixNano()
	}
	raw, err := proto.Marshal(pb)
	if err != nil {
		return AddResult{}, fmt.Errorf("memfs: marshal symlink: %w", err)
	}
	rootMID := mid.FromBytesWithCodec(raw, mid.CodecMemFS)
	if err := b.bs.Put(rootMID, raw); err != nil {
		return AddResult{}, fmt.Errorf("memfs: store symlink: %w", err)
	}
	return AddResult{MID: rootMID, Size: uint64(len(target)), Block: 1}, nil
}

// AddDirectoryFromFS walks fsys starting at root and stores
// the entire subtree. The returned MID is the root DIR.
//
// The walk is a single bottom-up pass: leaves are added
// first, then each directory is added with references to
// its already-stored children. This keeps memory bounded to
// the size of one directory's entries, not the whole tree.
func (b *Builder) AddDirectoryFromFS(fsys fs.FS, root string) (AddResult, error) {
	if b.bs == nil {
		return AddResult{}, errors.New("memfs: nil blockstore")
	}
	if fsys == nil {
		return AddResult{}, errors.New("memfs: nil fs")
	}
	if root == "" {
		root = "."
	}

	// Collect entries as we walk. The post-order walk
	// visits children before their parent, so we can build
	// directories by appending to a parent bucket as soon
	// as each child finishes.
	type pending struct {
		relPath string
		isDir   bool
		entry   AddResult
	}
	var stack []pending

	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepathRel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mt := info.ModTime()
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			tgt, err := fs.ReadLink(fsys, p)
			if err != nil {
				return err
			}
			r, err := b.AddSymlink(path.Base(rel), tgt, info.Mode().Perm(), mt)
			if err != nil {
				return err
			}
			stack = append(stack, pending{relPath: rel, entry: r})
		case d.IsDir():
			stack = append(stack, pending{relPath: rel, isDir: true})
		default:
			f, err := fsys.Open(p)
			if err != nil {
				return err
			}
			mime := store.SniffMime(path.Base(rel))
			r, err := b.AddFile(path.Base(rel), f, info.Mode().Perm(), mt, mime)
			_ = f.Close()
			if err != nil {
				return err
			}
			stack = append(stack, pending{relPath: rel, entry: r})
		}
		return nil
	})
	if err != nil {
		return AddResult{}, err
	}

	// Bucket children by parent. Then walk stack in reverse
	// so the deepest directories are built first; their
	// children's MIDs are already in the bucket.
	byParent := make(map[string][]DirEntry)
	for _, p := range stack {
		if p.isDir {
			continue
		}
		parent := path.Dir(p.relPath)
		if parent == "." {
			parent = ""
		}
		byParent[parent] = append(byParent[parent], DirEntry{
			Name: path.Base(p.relPath),
			Mid:  p.entry.MID,
			Type: TypeFile,
			Size: p.entry.Size,
		})
	}

	// Sort stack so directories at the deepest paths are
	// processed first. The fs.WalkDir callback gave us
	// entries in path-sorted order, but reversing the slice
	// is not enough on its own because the depth is what
	// matters. Use a stable sort by depth (number of
	// slashes) so deeper paths come first.
	sort.SliceStable(stack, func(i, j int) bool {
		di := strings.Count(stack[i].relPath, "/")
		dj := strings.Count(stack[j].relPath, "/")
		if di != dj {
			return di > dj // deeper first
		}
		return stack[i].relPath > stack[j].relPath
	})

	for _, p := range stack {
		if !p.isDir {
			continue
		}
		entries := byParent[p.relPath]
		r, err := b.AddDir(path.Base(p.relPath), entries, 0o755, time.Time{})
		if err != nil {
			return AddResult{}, err
		}
		parent := path.Dir(p.relPath)
		if parent == "." {
			parent = ""
		}
		byParent[parent] = append(byParent[parent], DirEntry{
			Name: path.Base(p.relPath),
			Mid:  r.MID,
			Type: TypeDir,
			Size: r.Size,
		})
	}

	// The root directory's children are the top-level
	// byParent[""] entries.
	topEntries := byParent[""]
	r, err := b.AddDir(".", topEntries, 0o755, time.Time{})
	if err != nil {
		return AddResult{}, err
	}
	return r, nil
}

// sumBlockSizes returns the sum of a slice of blockRef sizes.
func sumBlockSizes(bs []blockRef) uint64 {
	var s uint64
	for _, b := range bs {
		s += b.size
	}
	return s
}

// filepathRel returns the slash-separated path of p relative
// to base, both interpreted as fs.FS-style paths. If p ==
// base it returns ".".
func filepathRel(base, p string) (string, error) {
	if base == "" {
		base = "."
	}
	if p == base {
		return ".", nil
	}
	// When base is "." the path is already relative — return
	// it verbatim, stripping only a leading "./" if any.
	if base == "." {
		rel := p
		if len(rel) > 2 && rel[:2] == "./" {
			rel = rel[2:]
		}
		if rel == "" {
			return ".", nil
		}
		return rel, nil
	}
	if !hasPathPrefix(p, base) {
		return "", fmt.Errorf("memfs: %q is not under %q", p, base)
	}
	rel := p[len(base):]
	if len(rel) > 0 && rel[0] == '/' {
		rel = rel[1:]
	}
	if rel == "" {
		return ".", nil
	}
	return rel, nil
}

func hasPathPrefix(s, prefix string) bool {
	if prefix == "." {
		return true
	}
	if len(s) < len(prefix) {
		return false
	}
	if s[:len(prefix)] != prefix {
		return false
	}
	if len(s) == len(prefix) {
		return true
	}
	return s[len(prefix)] == '/'
}

// StreamEntry is one file to ingest during a streaming directory upload.
type StreamEntry struct {
	Path string
	Size int64
	R    io.Reader
}

// AddDirectoryStream ingests a directory tree directly from a stream of files.
// It constructs and stores all file nodes in real-time, eliminating the need to write
// files to a temporary directory on disk.
func (b *Builder) AddDirectoryStream(entries []StreamEntry) (AddResult, error) {
	if b.bs == nil {
		return AddResult{}, errors.New("memfs: nil blockstore")
	}
	if len(entries) == 0 {
		return AddResult{}, errors.New("memfs: no entries")
	}

	type treeNode struct {
		name     string
		isDir    bool
		mid      mid.MID
		size     uint64
		children map[string]*treeNode
	}

	root := &treeNode{
		name:     "",
		isDir:    true,
		children: make(map[string]*treeNode),
	}

	// 1. Process each file entry, chunk and store it, and insert it into the in-memory tree.
	for _, entry := range entries {
		rel := strings.ReplaceAll(entry.Path, "\\", "/")
		rel = path.Clean("/" + rel)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || rel == "." {
			continue
		}

		parts := strings.Split(rel, "/")
		if len(parts) == 0 {
			continue
		}

		mime := store.SniffMime(parts[len(parts)-1])
		res, err := b.AddFile(parts[len(parts)-1], entry.R, 0o644, time.Time{}, mime)
		if err != nil {
			return AddResult{}, fmt.Errorf("memfs: add file %q: %w", rel, err)
		}

		// Traverse and build tree nodes
		curr := root
		for i, part := range parts {
			isLast := i == len(parts)-1
			if isLast {
				curr.children[part] = &treeNode{
					name:  part,
					isDir: false,
					mid:   res.MID,
					size:  res.Size,
				}
			} else {
				child, ok := curr.children[part]
				if !ok {
					child = &treeNode{
						name:     part,
						isDir:    true,
						children: make(map[string]*treeNode),
					}
					curr.children[part] = child
				}
				curr = child
			}
		}
	}

	// 2. Bottom-up post-order traversal to build directory nodes.
	var buildDir func(n *treeNode) (mid.MID, uint64, uint64, error)
	buildDir = func(n *treeNode) (mid.MID, uint64, uint64, error) {
		if !n.isDir {
			return n.mid, n.size, 1, nil
		}

		dirEntries := make([]DirEntry, 0, len(n.children))
		for _, child := range n.children {
			childMID, childSize, _, err := buildDir(child)
			if err != nil {
				return mid.MID{}, 0, 0, err
			}
			t := TypeFile
			if child.isDir {
				t = TypeDir
			}
			dirEntries = append(dirEntries, DirEntry{
				Name: child.name,
				Mid:  childMID,
				Type: t,
				Size: childSize,
			})
		}

		res, err := b.AddDir(n.name, dirEntries, 0o755, time.Time{})
		if err != nil {
			return mid.MID{}, 0, 0, err
		}
		return res.MID, res.Size, res.Block, nil
	}

	// Build from root directory
	resMID, resSize, resBlock, err := buildDir(root)
	if err != nil {
		return AddResult{}, err
	}

	return AddResult{
		MID:   resMID,
		Size:  resSize,
		Block: resBlock,
	}, nil
}
