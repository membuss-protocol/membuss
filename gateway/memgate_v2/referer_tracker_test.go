package memgate_v2

import (
	"fmt"
	"testing"
	"time"
)

func TestRefererTracker_PathLRUEviction(t *testing.T) {
	rt := NewRefererTrackerWithOpts(3, 10)

	rt.RecordMapping("10.0.0.1", "/path1", "mid1")
	rt.RecordMapping("10.0.0.1", "/path2", "mid2")
	rt.RecordMapping("10.0.0.1", "/path3", "mid3")

	// Access path1 so path2 becomes LRU
	m1, ok := rt.getMapping("10.0.0.1", "/path1")
	if !ok || m1 != "mid1" {
		t.Fatalf("expected mid1 for /path1, got %s, ok=%v", m1, ok)
	}

	// Add path4, exceeding maxCacheSize (3). /path2 should be evicted.
	rt.RecordMapping("10.0.0.1", "/path4", "mid4")

	if _, ok := rt.getMapping("10.0.0.1", "/path2"); ok {
		t.Errorf("expected /path2 to be evicted as LRU, but it was found")
	}

	if val, ok := rt.getMapping("10.0.0.1", "/path1"); !ok || val != "mid1" {
		t.Errorf("expected /path1 to survive, got val=%s ok=%v", val, ok)
	}
	if val, ok := rt.getMapping("10.0.0.1", "/path4"); !ok || val != "mid4" {
		t.Errorf("expected /path4 to exist, got val=%s ok=%v", val, ok)
	}
}

func TestRefererTracker_ClientIPLRUEviction(t *testing.T) {
	rt := NewRefererTrackerWithOpts(10, 2)

	rt.RecordActiveMID("ip1", "mid1")
	rt.RecordMapping("ip1", "/style.css", "mid1")

	rt.RecordActiveMID("ip2", "mid2")
	rt.RecordMapping("ip2", "/app.js", "mid2")

	// Access ip1 so ip2 becomes LRU
	recent1 := rt.getRecentMIDs("ip1")
	if len(recent1) == 0 || recent1[0] != "mid1" {
		t.Fatalf("expected mid1 for ip1, got %v", recent1)
	}

	// Add ip3, exceeding maxClients (2). ip2 should be evicted.
	rt.RecordActiveMID("ip3", "mid3")

	if recent2 := rt.getRecentMIDs("ip2"); len(recent2) > 0 {
		t.Errorf("expected ip2 to be evicted as LRU, but found recent MIDs: %v", recent2)
	}

	if _, ok := rt.getMapping("ip2", "/app.js"); ok {
		t.Errorf("expected ip2 path mappings to be purged upon client eviction")
	}

	if recent1 := rt.getRecentMIDs("ip1"); len(recent1) == 0 {
		t.Errorf("expected ip1 to survive client eviction")
	}
}

func TestRefererTracker_PruneInactiveClients(t *testing.T) {
	rt := NewRefererTrackerWithOpts(10, 10)

	rt.RecordActiveMID("ip-old", "mid-old")
	rt.RecordMapping("ip-old", "/old.png", "mid-old")

	// Artificially backdate last active timestamp
	rt.mu.Lock()
	rt.clientLastActive["ip-old"] = time.Now().Add(-2 * time.Hour)
	rt.mu.Unlock()

	rt.RecordActiveMID("ip-fresh", "mid-fresh")

	pruned := rt.PruneInactiveClients(1 * time.Hour)
	if pruned != 1 {
		t.Fatalf("expected 1 client pruned, got %d", pruned)
	}

	if recent := rt.getRecentMIDs("ip-old"); len(recent) > 0 {
		t.Errorf("expected ip-old to be pruned")
	}

	if _, ok := rt.getMapping("ip-old", "/old.png"); ok {
		t.Errorf("expected ip-old path mapping to be purged")
	}

	if recent := rt.getRecentMIDs("ip-fresh"); len(recent) == 0 {
		t.Errorf("expected ip-fresh to remain")
	}
}

func TestRefererTracker_StressCap(t *testing.T) {
	rt := NewRefererTrackerWithOpts(100, 50)

	for i := 0; i < 500; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i%100)
		path := fmt.Sprintf("/asset_%d.png", i)
		midStr := fmt.Sprintf("mid_%d", i)
		rt.RecordMapping(ip, path, midStr)
		rt.RecordActiveMID(ip, midStr)
	}

	rt.mu.Lock()
	pathCount := len(rt.pathMIDs)
	clientCount := len(rt.recentMIDs)
	rt.mu.Unlock()

	if pathCount > 100 {
		t.Errorf("pathMIDs count %d exceeds cap 100", pathCount)
	}
	if clientCount > 50 {
		t.Errorf("recentMIDs count %d exceeds cap 50", clientCount)
	}
}
