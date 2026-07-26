package memex_v2

import (
	"context"
	"sync"
	"time"
)

// DefaultIdleTimeout is the maximum duration a session will wait for new block activity
// before considering the stream stalled. As long as blocks are arriving, transfers
// continue indefinitely regardless of total payload size (1 GB to 100 TB).
const DefaultIdleTimeout = 60 * time.Second

// MaxActiveWantlistWindow caps the number of concurrent in-flight block requests
// in memory, preventing RAM bloat on terabyte-scale DAGs with millions of blocks.
const MaxActiveWantlistWindow = 2048

// ActivityContext returns a context that cancels only if touchFn is not called
// within idleTimeout. Calling touchFn resets the idle countdown timer.
func ActivityContext(parent context.Context, idleTimeout time.Duration) (ctx context.Context, cancel context.CancelFunc, touchFn func()) {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}

	actCtx, actCancel := context.WithCancel(parent)

	var mu sync.Mutex
	timer := time.AfterFunc(idleTimeout, func() {
		actCancel()
	})

	touch := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Reset(idleTimeout)
		}
	}

	cleanupCancel := func() {
		mu.Lock()
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		mu.Unlock()
		actCancel()
	}

	return actCtx, cleanupCancel, touch
}
