package anchor

import (
	"strings"
	"testing"
	"time"
)

func TestTelemetry_RecordAndSnapshot(t *testing.T) {
	tc := NewTelemetryCollector()

	tc.IncWorkers()
	tc.IncWorkers()
	tc.DecWorkers() // Net active workers = 1

	tc.RecordFetch(true, 1024, 10*time.Millisecond)
	tc.RecordFetch(false, 0, 20*time.Millisecond)

	snap := tc.Snapshot(5000, 10000)

	if snap.ActiveWorkers != 1 {
		t.Errorf("active workers: got %d, want 1", snap.ActiveWorkers)
	}
	if snap.TotalFetches != 2 {
		t.Errorf("total fetches: got %d, want 2", snap.TotalFetches)
	}
	if snap.SuccessFetches != 1 {
		t.Errorf("success fetches: got %d, want 1", snap.SuccessFetches)
	}
	if snap.FailedFetches != 1 {
		t.Errorf("failed fetches: got %d, want 1", snap.FailedFetches)
	}
	if snap.SyncedBytes != 1024 {
		t.Errorf("synced bytes: got %d, want 1024", snap.SyncedBytes)
	}
	if snap.AvgLatencyMs != 15.0 {
		t.Errorf("avg latency ms: got %.2f, want 15.00", snap.AvgLatencyMs)
	}
	if snap.StorageUsagePct != 50.0 {
		t.Errorf("storage usage pct: got %.2f, want 50.00", snap.StorageUsagePct)
	}
}

func TestTelemetry_PrometheusExport(t *testing.T) {
	tc := NewTelemetryCollector()
	tc.RecordFetch(true, 2048, 5*time.Millisecond)

	snap := tc.Snapshot(2048, 10000)
	output := PrometheusFormat(snap)

	expectedSubstrings := []string{
		"membuss_anchor_active_workers 0",
		"membuss_anchor_fetches_total 1",
		"membuss_anchor_fetches_success_total 1",
		"membuss_anchor_synced_bytes_total 2048",
		"membuss_anchor_storage_used_bytes 2048",
		"membuss_anchor_storage_limit_bytes 10000",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(output, sub) {
			t.Errorf("prometheus format missing expected line %q. Output:\n%s", sub, output)
		}
	}
}
