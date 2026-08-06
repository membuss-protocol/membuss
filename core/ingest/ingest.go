package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/dag"
	"github.com/nnlgsakib/membuss/core/memfs"
	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
)

// Options specifies canonical ingestion configuration.
type Options struct {
	Name       string
	Mode       fs.FileMode
	ModTime    time.Time
	MimeType   string
	Chunker    string
	ChunkSize  int
	WrapDir    bool
	RawDAG     bool
	Seal       bool
	ProgressFn func(processed, total uint64)
}

// Result describes the outcome of an ingestion operation.
type Result struct {
	MID      mid.MID
	Size     uint64
	Blocks   uint64
	MimeType string
}

// IngestFile ingests a file stream into the store using the canonical MemFS format
// (or raw DAG if opts.RawDAG is explicitly set).
func IngestFile(ctx context.Context, s store.Store, r io.Reader, opts Options) (Result, error) {
	if s == nil {
		return Result{}, errors.New("ingest: nil store")
	}
	if r == nil {
		return Result{}, errors.New("ingest: nil reader")
	}

	// Apply canonical defaults
	if opts.Mode == 0 {
		opts.Mode = 0o644
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = chunk.DefaultBlockSize
	}
	if opts.MimeType == "" && opts.Name != "" {
		opts.MimeType = store.SniffMime(opts.Name)
	}

	var reader io.Reader = r
	if opts.ProgressFn != nil {
		reader = &progressReader{r: r, fn: opts.ProgressFn}
	}

	if opts.RawDAG {
		var factory chunk.ChunkerFactory
		switch opts.Chunker {
		case "rabin":
			factory = chunk.NewRabin()
		case "fastcdc":
			factory = chunk.NewFastCDC()
		default:
			factory = chunk.NewFixed(opts.ChunkSize)
		}
		c, err := factory(reader)
		if err != nil {
			return Result{}, fmt.Errorf("ingest: chunker: %w", err)
		}
		rootMID, err := dag.NewBuilder(s).Build(c)
		if err != nil {
			return Result{}, fmt.Errorf("ingest: dag: %w", err)
		}

		blocksCount, totalSize := uint64(1), uint64(0)
		_ = store.Walk(s, rootMID, func(m mid.MID, leaf bool) error {
			if leaf {
				blocksCount++
				if data, gerr := s.Get(m); gerr == nil {
					totalSize += uint64(len(data))
				}
			}
			return nil
		})

		if oi, oerr := store.GetObjectInfo(s, rootMID); oerr == nil {
			oi.IsRoot = true
			_ = store.SetObjectInfo(s, rootMID, oi)
		}
		if opts.Seal {
			_ = s.Seal(rootMID, true)
		}

		return Result{
			MID:      rootMID,
			Size:     totalSize,
			Blocks:   blocksCount,
			MimeType: opts.MimeType,
		}, nil
	}

	// Canonical MemFS Ingestion
	bld := memfs.NewBuilder(s).WithBlockSize(opts.ChunkSize)
	if opts.Chunker != "" {
		bld = bld.WithChunker(opts.Chunker)
	}

	res, err := bld.AddFile(opts.Name, reader, opts.Mode, opts.ModTime, opts.MimeType)
	if err != nil {
		return Result{}, fmt.Errorf("ingest: %w", err)
	}

	finalMID := res.MID
	finalSize := res.Size
	finalBlocks := res.Block

	if opts.WrapDir {
		dirRes, err := bld.AddDir(opts.Name, []memfs.DirEntry{
			{Name: opts.Name, Mid: res.MID, Type: memfs.TypeFile, Size: res.Size},
		}, 0o755, opts.ModTime)
		if err != nil {
			return Result{}, fmt.Errorf("ingest: wrap dir: %w", err)
		}
		finalMID = dirRes.MID
		finalSize = dirRes.Size
		finalBlocks = dirRes.Block
	}

	if oi, oerr := store.GetObjectInfo(s, finalMID); oerr == nil {
		oi.IsRoot = true
		_ = store.SetObjectInfo(s, finalMID, oi)
	}

	if opts.Seal {
		_ = s.Seal(finalMID, true)
	}

	return Result{
		MID:      finalMID,
		Size:     finalSize,
		Blocks:   finalBlocks,
		MimeType: opts.MimeType,
	}, nil
}

// IngestDirectoryStream ingests a directory stream into the store using MemFS.
func IngestDirectoryStream(ctx context.Context, s store.Store, entries []memfs.StreamEntry, opts Options) (Result, error) {
	if s == nil {
		return Result{}, errors.New("ingest: nil store")
	}
	if len(entries) == 0 {
		return Result{}, errors.New("ingest: no entries")
	}

	if opts.ChunkSize <= 0 {
		opts.ChunkSize = chunk.DefaultBlockSize
	}

	bld := memfs.NewBuilder(s).WithBlockSize(opts.ChunkSize)
	if opts.Chunker != "" {
		bld = bld.WithChunker(opts.Chunker)
	}

	res, err := bld.AddDirectoryStream(entries)
	if err != nil {
		return Result{}, fmt.Errorf("ingest: dir stream: %w", err)
	}

	if oi, oerr := store.GetObjectInfo(s, res.MID); oerr == nil {
		oi.IsRoot = true
		_ = store.SetObjectInfo(s, res.MID, oi)
	}

	if opts.Seal {
		_ = s.Seal(res.MID, true)
	}

	return Result{
		MID:    res.MID,
		Size:   res.Size,
		Blocks: res.Block,
	}, nil
}

type progressReader struct {
	r         io.Reader
	fn        func(processed, total uint64)
	processed uint64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.processed += uint64(n)
		if pr.fn != nil {
			pr.fn(pr.processed, 0)
		}
	}
	return n, err
}
