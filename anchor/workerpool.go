package anchor

import (
	"context"
	"sync"
)

// DefaultFetchConcurrency is the default number of concurrent worker
// goroutines used to fetch missing backlog items in parallel.
const DefaultFetchConcurrency = 8

// processBacklogConcurrently processes items using a bounded worker pool.
// It stops dispatching new items immediately if ctx is cancelled.
func processBacklogConcurrently(ctx context.Context, items []enqueuedMID, concurrency int, fetchFn func(context.Context, enqueuedMID), telemetry ...*TelemetryCollector) {
	if len(items) == 0 || ctx.Err() != nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultFetchConcurrency
	}

	var tc *TelemetryCollector
	if len(telemetry) > 0 {
		tc = telemetry[0]
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			goto Done
		case sem <- struct{}{}:
			if ctx.Err() != nil {
				<-sem
				goto Done
			}
			wg.Add(1)
			if tc != nil {
				tc.IncWorkers()
			}
			go func(itm enqueuedMID) {
				defer func() {
					if tc != nil {
						tc.DecWorkers()
					}
					<-sem
					wg.Done()
				}()
				fetchFn(ctx, itm)
			}(item)
		}
	}

Done:
	wg.Wait()
}
