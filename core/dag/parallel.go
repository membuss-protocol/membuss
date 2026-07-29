package dag

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

	"github.com/nnlgsakib/membuss/core/chunk"
	"github.com/nnlgsakib/membuss/core/mid"
)

// chunkJob represents an indexed chunk read from the input stream.
type chunkJob struct {
	index uint64
	block chunk.Block
}

// processedChunk holds the processed block details ready for tree accumulation.
type processedChunk struct {
	index   uint64
	leafMID mid.MID
	data    []byte
}

// BuildParallel consumes all blocks from c, processes/hashes chunks concurrently
// across worker goroutines, writes every block into the Blockstore, and returns the
// MID of the root. The produced tree is 100% byte-identical to sequential Build.
func (b *Builder) BuildParallel(c chunk.Chunker, workers int) (mid.MID, error) {
	if b.bs == nil {
		return mid.MID{}, errors.New("dag: nil blockstore")
	}
	if c == nil {
		return mid.MID{}, errors.New("dag: nil chunker")
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers <= 1 {
		return b.Build(c)
	}

	jobs := make(chan chunkJob, workers*4)
	results := make(chan processedChunk, workers*4)
	errCh := make(chan error, 1)

	// Producer goroutine: read chunks sequentially from chunker
	go func() {
		defer close(jobs)
		var idx uint64
		for {
			blk, err := c.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				select {
				case errCh <- fmt.Errorf("dag: read chunk: %w", err):
				default:
				}
				return
			}
			if blk.Size() == 0 {
				select {
				case errCh <- errors.New("dag: empty chunk"):
				default:
				}
				return
			}
			jobs <- chunkJob{index: idx, block: blk}
			idx++
		}
	}()

	// Worker pool: compute MIDs concurrently across CPU cores
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				m := job.block.MID()
				if m.IsZero() {
					select {
					case errCh <- errors.New("dag: chunk has zero MID"):
					default:
					}
					return
				}
				results <- processedChunk{
					index:   job.index,
					leafMID: m,
					data:    job.block.RawData(),
				}
			}
		}()
	}

	// Close results channel once all workers finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Re-order results sequentially and accumulate into levelStack
	pending := make(map[uint64]processedChunk)
	bld := &levelStack{bs: b.bs}
	var nextIdx uint64
	sawLeaf := false

	for res := range results {
		pending[res.index] = res
		for {
			pc, ok := pending[nextIdx]
			if !ok {
				break
			}
			delete(pending, nextIdx)

			if err := b.bs.Put(pc.leafMID, pc.data); err != nil {
				return mid.MID{}, fmt.Errorf("dag: store leaf: %w", err)
			}
			if err := bld.push(0, pc.leafMID); err != nil {
				return mid.MID{}, err
			}
			sawLeaf = true
			nextIdx++
		}
	}

	// Check for any chunker / MID calculation errors
	select {
	case err := <-errCh:
		return mid.MID{}, err
	default:
	}

	if !sawLeaf {
		return mid.MID{}, errors.New("dag: empty input")
	}

	return bld.finalize()
}
