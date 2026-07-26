package anchor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

func TestWorkerPool_ConcurrentlyProcessesItems(t *testing.T) {
	ctx := context.Background()
	numItems := 50
	items := make([]enqueuedMID, numItems)
	for i := 0; i < numItems; i++ {
		items[i] = enqueuedMID{
			mid: mid.FromBytes([]byte{byte(i)}),
		}
	}

	var processed int64
	var activeGauge int64
	var maxActive int64

	processBacklogConcurrently(ctx, items, 4, func(_ context.Context, _ enqueuedMID) {
		current := atomic.AddInt64(&activeGauge, 1)
		for {
			max := atomic.LoadInt64(&maxActive)
			if current <= max || atomic.CompareAndSwapInt64(&maxActive, max, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&processed, 1)
		atomic.AddInt64(&activeGauge, -1)
	})

	if processed != int64(numItems) {
		t.Fatalf("processed: got %d, want %d", processed, numItems)
	}
	if maxActive > 4 {
		t.Fatalf("maxActive workers: got %d, want <= 4", maxActive)
	}
}

func TestWorkerPool_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	items := []enqueuedMID{
		{mid: mid.FromBytes([]byte("item1"))},
		{mid: mid.FromBytes([]byte("item2"))},
	}

	var processed int64
	processBacklogConcurrently(ctx, items, 2, func(_ context.Context, _ enqueuedMID) {
		atomic.AddInt64(&processed, 1)
	})

	if processed != 0 {
		t.Fatalf("expected 0 items processed when context is cancelled, got %d", processed)
	}
}
