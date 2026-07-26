package anchor

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// TelemetrySnapshot represents a thread-safe snapshot of anchor node operational metrics.
type TelemetrySnapshot struct {
	ActiveWorkers     int64   `json:"active_workers"`
	TotalFetches      int64   `json:"total_fetches"`
	SuccessFetches    int64   `json:"success_fetches"`
	FailedFetches     int64   `json:"failed_fetches"`
	SyncedBytes       int64   `json:"synced_bytes"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	StorageUsedBytes  uint64  `json:"storage_used_bytes"`
	StorageLimitBytes uint64  `json:"storage_limit_bytes"`
	StorageUsagePct   float64 `json:"storage_usage_pct"`
}

// TelemetryCollector records operational metrics for the AnchorEngine.
type TelemetryCollector struct {
	activeWorkers int64
	totalFetches  int64
	successFetch  int64
	failedFetch   int64
	syncedBytes   int64
	totalLatency  int64 // total duration in milliseconds
}

// NewTelemetryCollector creates a fresh TelemetryCollector.
func NewTelemetryCollector() *TelemetryCollector {
	return &TelemetryCollector{}
}

// IncWorkers increments the active worker gauge.
func (t *TelemetryCollector) IncWorkers() {
	if t != nil {
		atomic.AddInt64(&t.activeWorkers, 1)
	}
}

// DecWorkers decrements the active worker gauge.
func (t *TelemetryCollector) DecWorkers() {
	if t != nil {
		atomic.AddInt64(&t.activeWorkers, -1)
	}
}

// RecordFetch logs the outcome, byte size, and duration of a MID fetch operation.
func (t *TelemetryCollector) RecordFetch(success bool, bytes int64, duration time.Duration) {
	if t == nil {
		return
	}
	atomic.AddInt64(&t.totalFetches, 1)
	if success {
		atomic.AddInt64(&t.successFetch, 1)
	} else {
		atomic.AddInt64(&t.failedFetch, 1)
	}
	if bytes > 0 {
		atomic.AddInt64(&t.syncedBytes, bytes)
	}
	if duration > 0 {
		atomic.AddInt64(&t.totalLatency, duration.Milliseconds())
	}
}

// Snapshot captures current metrics and computes derived throughput/storage percentages.
func (t *TelemetryCollector) Snapshot(usedBytes, limitBytes uint64) TelemetrySnapshot {
	if t == nil {
		return TelemetrySnapshot{
			StorageUsedBytes:  usedBytes,
			StorageLimitBytes: limitBytes,
		}
	}

	active := atomic.LoadInt64(&t.activeWorkers)
	if active < 0 {
		active = 0
	}
	total := atomic.LoadInt64(&t.totalFetches)
	success := atomic.LoadInt64(&t.successFetch)
	failed := atomic.LoadInt64(&t.failedFetch)
	bytes := atomic.LoadInt64(&t.syncedBytes)
	totLat := atomic.LoadInt64(&t.totalLatency)

	var avgLat float64
	if total > 0 {
		avgLat = float64(totLat) / float64(total)
	}

	var usagePct float64
	if limitBytes > 0 {
		usagePct = (float64(usedBytes) / float64(limitBytes)) * 100.0
	}

	return TelemetrySnapshot{
		ActiveWorkers:     active,
		TotalFetches:      total,
		SuccessFetches:    success,
		FailedFetches:     failed,
		SyncedBytes:       bytes,
		AvgLatencyMs:      avgLat,
		StorageUsedBytes:  usedBytes,
		StorageLimitBytes: limitBytes,
		StorageUsagePct:   usagePct,
	}
}

// PrometheusFormat exports snapshot metrics in standard OpenMetrics / Prometheus text format.
func PrometheusFormat(snap TelemetrySnapshot) string {
	var sb strings.Builder
	sb.WriteString("# HELP membuss_anchor_active_workers Number of active worker pool goroutines.\n")
	sb.WriteString("# TYPE membuss_anchor_active_workers gauge\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_active_workers %d\n\n", snap.ActiveWorkers))

	sb.WriteString("# HELP membuss_anchor_fetches_total Total MID fetch attempts.\n")
	sb.WriteString("# TYPE membuss_anchor_fetches_total counter\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_fetches_total %d\n\n", snap.TotalFetches))

	sb.WriteString("# HELP membuss_anchor_fetches_success_total Successful MID fetch count.\n")
	sb.WriteString("# TYPE membuss_anchor_fetches_success_total counter\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_fetches_success_total %d\n\n", snap.SuccessFetches))

	sb.WriteString("# HELP membuss_anchor_fetches_failed_total Failed MID fetch count.\n")
	sb.WriteString("# TYPE membuss_anchor_fetches_failed_total counter\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_fetches_failed_total %d\n\n", snap.FailedFetches))

	sb.WriteString("# HELP membuss_anchor_synced_bytes_total Total bytes fetched over network.\n")
	sb.WriteString("# TYPE membuss_anchor_synced_bytes_total counter\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_synced_bytes_total %d\n\n", snap.SyncedBytes))

	sb.WriteString("# HELP membuss_anchor_fetch_latency_avg_ms Average MID fetch latency in milliseconds.\n")
	sb.WriteString("# TYPE membuss_anchor_fetch_latency_avg_ms gauge\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_fetch_latency_avg_ms %.2f\n\n", snap.AvgLatencyMs))

	sb.WriteString("# HELP membuss_anchor_storage_used_bytes Total storage bytes consumed.\n")
	sb.WriteString("# TYPE membuss_anchor_storage_used_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_storage_used_bytes %d\n\n", snap.StorageUsedBytes))

	sb.WriteString("# HELP membuss_anchor_storage_limit_bytes Maximum allowed storage quota in bytes.\n")
	sb.WriteString("# TYPE membuss_anchor_storage_limit_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("membuss_anchor_storage_limit_bytes %d\n", snap.StorageLimitBytes))

	return sb.String()
}
