package erasure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
	"github.com/nnlgsakib/membuss/core/store"
	"github.com/nnlgsakib/membuss/obs/metrics"
)

// RepairStats tracks background repair worker progress.
type RepairStats struct {
	AuditedMIDs    int
	DegradedMIDs   int
	RepairedShards int
	Unrecoverable  int
}

// RepairMID inspects the erasure shards of a MID and reconstructs missing shards.
// Requires at least DataShards (K) valid shards present to succeed.
func RepairMID(s store.Blockstore, m mid.MID) (int, error) {
	if s == nil {
		return 0, errors.New("erasure: nil store")
	}
	manifest, err := GetManifest(s, m)
	if err != nil {
		return 0, fmt.Errorf("erasure: get manifest for repair: %w", err)
	}
	if manifest == nil {
		return 0, nil // No erasure manifest associated
	}

	dataShards := int(manifest.DataShards)
	parityShards := int(manifest.ParityShards)
	totalShards := dataShards + parityShards

	if len(manifest.ShardMids) != totalShards {
		return 0, fmt.Errorf("erasure: manifest shard count mismatch: got %d want %d", len(manifest.ShardMids), totalShards)
	}

	cfg, err := NewConfig(dataShards, parityShards)
	if err != nil {
		return 0, err
	}
	encoder, err := NewEncoder(cfg)
	if err != nil {
		return 0, err
	}

	shards := make([][]byte, totalShards)
	presentCount := 0

	for i, smStr := range manifest.ShardMids {
		sm, perr := mid.Parse(smStr)
		if perr != nil {
			shards[i] = nil
			continue
		}
		data, gerr := s.Get(sm)
		if gerr == nil && len(data) > 0 {
			if VerifyShard(data, smStr) {
				shards[i] = data
				presentCount++
			} else {
				shards[i] = nil
			}
		} else {
			shards[i] = nil
		}
	}

	if presentCount == totalShards {
		return 0, nil // 100% healthy, no repair needed
	}

	if presentCount < dataShards {
		return 0, fmt.Errorf("erasure: unrecoverable MID %s (%d shards present, need %d)", m.String(), presentCount, dataShards)
	}

	// Reconstruct missing shards via Reed-Solomon matrix algebra
	if err := encoder.enc.Reconstruct(shards); err != nil {
		return 0, fmt.Errorf("erasure: reconstruct missing shards: %w", err)
	}

	repairedCount := 0
	for i, sData := range shards {
		if sData != nil && manifest.ShardMids[i] != "" {
			sm, perr := mid.Parse(manifest.ShardMids[i])
			if perr == nil {
				has, _ := s.Has(sm)
				if !has {
					if err := s.Put(sm, sData); err == nil {
						repairedCount++
					}
				}
			}
		}
	}

	return repairedCount, nil
}

// RepairWorker runs periodic background auditing and shard repair for degraded MIDs.
type RepairWorker struct {
	store    store.Blockstore
	interval time.Duration
	mu       sync.RWMutex
	stats    RepairStats
	metrics  *metrics.Metrics
}

// NewRepairWorker creates a new RepairWorker.
func NewRepairWorker(s store.Blockstore, interval time.Duration) *RepairWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &RepairWorker{
		store:    s,
		interval: interval,
	}
}

// WithMetrics attaches the Prometheus handle (XC-009). Returns the
// worker so daemon wiring can chain: NewRepairWorker(...).WithMetrics(m).
func (w *RepairWorker) WithMetrics(m *metrics.Metrics) *RepairWorker {
	w.metrics = m
	return w
}

// Run executes the background repair loop until ctx is canceled.
func (w *RepairWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.AuditAndRepair(ctx)
		}
	}
}

// AuditAndRepair performs a single audit and repair cycle over all sealed MIDs.
func (w *RepairWorker) AuditAndRepair(ctx context.Context) RepairStats {
	if w.store == nil {
		return RepairStats{}
	}

	var stats RepairStats
	var sealedMIDs []mid.MID
	if sl, ok := w.store.(interface {
		AllSealed() ([]mid.MID, error)
	}); ok {
		mids, serr := sl.AllSealed()
		if serr == nil {
			sealedMIDs = mids
		}
	} else if ab, ok := w.store.(interface {
		AllBlocks() ([]mid.MID, error)
	}); ok {
		mids, aerr := ab.AllBlocks()
		if aerr == nil {
			sealedMIDs = mids
		}
	}

	for _, m := range sealedMIDs {
		select {
		case <-ctx.Done():
			return stats
		default:
		}

		manifest, err := GetManifest(w.store, m)
		if err != nil || manifest == nil {
			continue
		}

		stats.AuditedMIDs++
		repaired, rerr := RepairMID(w.store, m)
		if rerr != nil {
			stats.Unrecoverable++
			slog.Warn("erasure repair: unrecoverable MID", "mid", m.String(), "err", rerr.Error())
		} else if repaired > 0 {
			stats.DegradedMIDs++
			stats.RepairedShards += repaired
			slog.Info("erasure repair: successfully repaired degraded MID", "mid", m.String(), "repaired_shards", repaired)
		}
	}

	w.mu.Lock()
	w.stats.AuditedMIDs += stats.AuditedMIDs
	w.stats.DegradedMIDs += stats.DegradedMIDs
	w.stats.RepairedShards += stats.RepairedShards
	w.stats.Unrecoverable += stats.Unrecoverable
	w.mu.Unlock()

	if w.metrics != nil {
		w.metrics.SetErasureRepairAuditedLastCycle(int64(stats.AuditedMIDs))
		w.metrics.AddErasureRepairShardsRepaired(stats.RepairedShards)
		w.metrics.AddErasureRepairUnrecoverable(stats.Unrecoverable)
	}

	return stats
}

// Stats returns cumulative repair statistics.
func (w *RepairWorker) Stats() RepairStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.stats
}
