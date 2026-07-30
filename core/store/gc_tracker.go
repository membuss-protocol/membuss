package store

import (
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

// DefaultGCGracePeriod is the window of time during which newly written
// unsealed blocks are immune from garbage collection sweeps.
const DefaultGCGracePeriod = 5 * time.Minute

// gcWriteTracker manages active write tracking during GC sweeps and
// enforces an ingestion grace period for recently written blocks.
type gcWriteTracker struct {
	mu          sync.RWMutex
	gcActive    bool
	activeBuffer map[string]struct{}
	gracePeriod time.Duration
}

func newGCWriteTracker(gracePeriod time.Duration) *gcWriteTracker {
	if gracePeriod <= 0 {
		gracePeriod = DefaultGCGracePeriod
	}
	return &gcWriteTracker{
		activeBuffer: make(map[string]struct{}),
		gracePeriod: gracePeriod,
	}
}

// StartGC marks that a GC sweep is currently running and clears the active buffer.
func (g *gcWriteTracker) StartGC() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gcActive = true
	g.activeBuffer = make(map[string]struct{})
}

// EndGC marks that the GC sweep has finished.
func (g *gcWriteTracker) EndGC() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gcActive = false
	g.activeBuffer = make(map[string]struct{})
}

// RecordWrite records a MID written to the store.
func (g *gcWriteTracker) RecordWrite(m mid.MID) {
	if m.IsZero() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gcActive {
		g.activeBuffer[m.String()] = struct{}{}
	}
}

// IsProtected checks if MID m is protected from GC deletion.
// A block is protected if:
// 1. It was written while the active GC sweep was running.
// 2. Its commit timestamp falls within the ingestion grace period.
func (g *gcWriteTracker) IsProtected(m mid.MID, writeTs uint64, minAge time.Duration) bool {
	if m.IsZero() {
		return true
	}
	g.mu.RLock()
	if g.gcActive {
		if _, ok := g.activeBuffer[m.String()]; ok {
			g.mu.RUnlock()
			return true
		}
	}
	grace := g.gracePeriod
	g.mu.RUnlock()

	if writeTs > 0 {
		effectiveGrace := grace
		if minAge == 0 {
			effectiveGrace = 0
		} else if minAge < grace {
			effectiveGrace = minAge
		}
		cutoff := uint64(time.Now().Add(-effectiveGrace).Unix())
		if writeTs >= cutoff && effectiveGrace > 0 {
			return true
		}
	}
	return false
}
