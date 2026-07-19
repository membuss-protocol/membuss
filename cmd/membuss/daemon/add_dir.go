// Directory ingest for the daemon. AddDirWithProgress walks a
// local directory tree and builds a single MemFS DIR root,
// reporting aggregate byte progress as it reads the files. It
// is the directory counterpart to AddWithProgress and backs the
// AddDirStream gRPC used by `membuss-cli add <dir>`.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	serverpkg "github.com/nnlgsakib/membuss/rpc/server"
)

// dirFile is one regular file discovered while walking a
// directory: its slash-separated path relative to the walk
// root, its absolute path on disk, and its size.
type dirFile struct {
	rel  string
	abs  string
	size int64
}

// AddDirWithProgress ingests the directory at path as a single
// MemFS DIR tree. It mirrors AddWithProgress: the daemon reads
// the tree from its own filesystem, invoking progressFn with the
// running byte count against the total tree size as files are
// read. name optionally overrides the root directory name
// (defaulting to the directory's basename).
func (b *daemonBackend) AddDirWithProgress(ctx context.Context, path, chunker string, chunkSize uint32, sealRoot bool, name string, progressFn func(processed, total uint64)) (serverpkg.AddResult, error) {
	if path == "" {
		return serverpkg.AddResult{}, errors.New("add-dir: empty path")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return serverpkg.AddResult{}, err
		}
		path = abs
	}
	fi, err := os.Stat(path)
	if err != nil {
		return serverpkg.AddResult{}, err
	}
	if !fi.IsDir() {
		return serverpkg.AddResult{}, fmt.Errorf("add-dir: %q is not a directory", path)
	}

	// First pass: enumerate the regular files and total their
	// size. This gives the progress denominator up front and
	// keeps only lightweight descriptors in memory (not open
	// handles or file bytes).
	var (
		files      []dirFile
		totalBytes uint64
	)
	walkErr := filepath.Walk(path, func(p string, info os.FileInfo, we error) error {
		if we != nil {
			return we
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(path, p)
		if rerr != nil {
			return rerr
		}
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		files = append(files, dirFile{rel: rel, abs: p, size: info.Size()})
		if info.Size() > 0 {
			totalBytes += uint64(info.Size())
		}
		return nil
	})
	if walkErr != nil {
		return serverpkg.AddResult{}, walkErr
	}
	if len(files) == 0 {
		return serverpkg.AddResult{}, errors.New("add-dir: no files in directory")
	}

	// Deterministic order so the same tree yields the same
	// stream regardless of the OS walk order.
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	// Second pass: feed lazily-opened, progress-counting
	// readers into the MemFS stream builder. AddDirectoryStream
	// consumes entries one at a time, so only one file handle is
	// open at any moment and memory stays bounded to a single
	// file's chunk window.
	agg := &aggCounter{total: totalBytes, fn: progressFn}
	entries := make([]memfs.StreamEntry, len(files))
	for i, f := range files {
		entries[i] = memfs.StreamEntry{
			Path: f.rel,
			Size: f.size,
			R:    &lazyFileReader{abs: f.abs, agg: agg},
		}
	}

	builder := memfs.NewBuilder(b.store)
	if chunker != "" {
		builder = builder.WithChunker(chunker)
	}
	if chunkSize > 0 {
		builder = builder.WithBlockSize(int(chunkSize))
	}

	res, err := builder.AddDirectoryStream(entries)
	if err != nil {
		return serverpkg.AddResult{}, fmt.Errorf("add-dir: %w", err)
	}

	// Land the bar on 100% for a fast ingest that never tripped
	// the progress throttle.
	if progressFn != nil {
		final := totalBytes
		if final == 0 {
			final = res.Size
		}
		progressFn(final, final)
	}

	// Root name defaults to the directory basename.
	dirName := filepath.Clean(name)
	if dirName == "." || dirName == "/" || dirName == "\\" || dirName == "" {
		dirName = filepath.Base(path)
	}
	if dirName == "." || dirName == "/" || dirName == "\\" || dirName == "" {
		dirName = "upload"
	}

	if err := store.SetObjectInfo(b.store, res.MID, store.ObjectInfo{
		Name:     dirName,
		MimeType: "inode/directory",
		Size:     res.Size,
		IsRoot:   true,
	}); err != nil {
		return serverpkg.AddResult{}, fmt.Errorf("add-dir: objectinfo: %w", err)
	}

	if sealRoot {
		if err := b.store.Seal(res.MID, true); err != nil {
			return serverpkg.AddResult{}, fmt.Errorf("add-dir: seal: %w", err)
		}
		if b.dht != nil {
			go func(r mid.MID) {
				announceCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				provideRecursive(announceCtx, b.dht, b.store, r)
			}(res.MID)
		}
	}

	return serverpkg.AddResult{
		MID:      res.MID.String(),
		Size:     res.Size,
		Blocks:   res.Block,
		Sealed:   sealRoot,
		Name:     dirName,
		MimeType: "inode/directory",
	}, nil
}

// aggCounter accumulates bytes read across all files of a
// directory ingest and reports the running total against the
// tree's total size. It is shared by every lazyFileReader of a
// single AddDirWithProgress call.
type aggCounter struct {
	total uint64
	read  uint64
	fn    func(processed, total uint64)
}

func (a *aggCounter) add(n int) {
	if a == nil || a.fn == nil || n <= 0 {
		return
	}
	a.read += uint64(n)
	a.fn(a.read, a.total)
}

// lazyFileReader opens its backing file on the first Read and
// closes it on EOF (or the first error), so a directory with
// thousands of entries never holds more than one open handle at
// a time. Bytes read drive the shared aggregate progress
// counter.
type lazyFileReader struct {
	abs    string
	agg    *aggCounter
	f      *os.File
	opened bool
	closed bool
}

func (l *lazyFileReader) Read(p []byte) (int, error) {
	if l.closed {
		return 0, io.EOF
	}
	if !l.opened {
		f, err := os.Open(l.abs)
		if err != nil {
			l.closed = true
			return 0, err
		}
		l.f = f
		l.opened = true
	}
	n, err := l.f.Read(p)
	if n > 0 {
		l.agg.add(n)
	}
	if err != nil {
		_ = l.f.Close()
		l.closed = true
	}
	return n, err
}
