package memgate_v2

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nnlgsakib/membuss/core/mid"
)

// MemNSResolver defines the interface for resolving MemNS names.
type MemNSResolver interface {
	Resolve(ctx context.Context, name string) (string, error)
}

// RefererTracker maps absolute asset request paths to their corresponding MIDs
// dynamically using the Referer chain, active MIDs list, and per-client state.
type RefererTracker struct {
	mu               sync.Mutex
	pathMIDs         map[string]string    // Key: clientIP + ":" + path, Value: midStr
	pathOrder        []string             // LRU at index 0, MRU at end
	recentMIDs       map[string][]string  // Key: clientIP, Value: MRU list of recently active MIDs (max 20)
	clientOrder      []string             // LRU at index 0, MRU at end
	clientLastActive map[string]time.Time // Key: clientIP, Value: last activity timestamp
	maxCacheSize     int                  // Maximum path mappings (default 2000)
	maxClients       int                  // Maximum client IP entries (default 2000)
	lastPrune        time.Time            // Timestamp of last auto-prune pass
}

// NewRefererTracker constructs a RefererTracker with default caps (2000 paths, 2000 clients).
func NewRefererTracker() *RefererTracker {
	return NewRefererTrackerWithOpts(2000, 2000)
}

// NewRefererTrackerWithOpts constructs a RefererTracker with custom caps.
func NewRefererTrackerWithOpts(maxCacheSize, maxClients int) *RefererTracker {
	if maxCacheSize < 1 {
		maxCacheSize = 2000
	}
	if maxClients < 1 {
		maxClients = 2000
	}
	return &RefererTracker{
		pathMIDs:         make(map[string]string),
		pathOrder:        make([]string, 0, maxCacheSize),
		recentMIDs:       make(map[string][]string),
		clientOrder:      make([]string, 0, maxClients),
		clientLastActive: make(map[string]time.Time),
		maxCacheSize:     maxCacheSize,
		maxClients:       maxClients,
		lastPrune:        time.Now(),
	}
}

// touchPathLocked promotes a path key to the end of pathOrder (MRU).
func (rt *RefererTracker) touchPathLocked(key string) {
	for i, k := range rt.pathOrder {
		if k == key {
			rt.pathOrder = append(rt.pathOrder[:i], rt.pathOrder[i+1:]...)
			break
		}
	}
	rt.pathOrder = append(rt.pathOrder, key)
}

// evictPathIfNeededLocked evicts LRU path mappings if pathMIDs exceeds maxCacheSize.
func (rt *RefererTracker) evictPathIfNeededLocked() {
	for len(rt.pathMIDs) > rt.maxCacheSize && len(rt.pathOrder) > 0 {
		oldest := rt.pathOrder[0]
		rt.pathOrder = rt.pathOrder[1:]
		delete(rt.pathMIDs, oldest)
	}
}

// touchClientLocked promotes a client IP to the end of clientOrder (MRU) and updates last active timestamp.
func (rt *RefererTracker) touchClientLocked(clientIP string) {
	if clientIP == "" {
		return
	}
	rt.clientLastActive[clientIP] = time.Now()
	for i, ip := range rt.clientOrder {
		if ip == clientIP {
			rt.clientOrder = append(rt.clientOrder[:i], rt.clientOrder[i+1:]...)
			break
		}
	}
	rt.clientOrder = append(rt.clientOrder, clientIP)
}

// evictClientIfNeededLocked evicts LRU client IPs if recentMIDs exceeds maxClients.
func (rt *RefererTracker) evictClientIfNeededLocked() {
	for len(rt.recentMIDs) > rt.maxClients && len(rt.clientOrder) > 0 {
		oldestIP := rt.clientOrder[0]
		rt.clientOrder = rt.clientOrder[1:]
		delete(rt.recentMIDs, oldestIP)
		delete(rt.clientLastActive, oldestIP)
		rt.purgePathMIDsForClientLocked(oldestIP)
	}
}

// purgePathMIDsForClientLocked removes all path mappings for a given client IP.
func (rt *RefererTracker) purgePathMIDsForClientLocked(clientIP string) {
	prefix := clientIP + ":"
	n := 0
	for _, k := range rt.pathOrder {
		if strings.HasPrefix(k, prefix) {
			delete(rt.pathMIDs, k)
		} else {
			rt.pathOrder[n] = k
			n++
		}
	}
	rt.pathOrder = rt.pathOrder[:n]
}

// RecordActiveMID registers a MID in the recently active list for a client IP.
func (rt *RefererTracker) RecordActiveMID(clientIP, midStr string) {
	if midStr == "" || clientIP == "" {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.maybeAutoPruneLocked()
	rt.touchClientLocked(clientIP)

	list := rt.recentMIDs[clientIP]
	for i, m := range list {
		if m == midStr {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	list = append([]string{midStr}, list...)
	if len(list) > 20 {
		list = list[:20]
	}
	rt.recentMIDs[clientIP] = list
	rt.evictClientIfNeededLocked()
}

// RecordMapping stores the association of a request path to a MID for a client IP.
func (rt *RefererTracker) RecordMapping(clientIP, path, midStr string) {
	if path == "" || midStr == "" {
		return
	}
	cleanPath := "/" + strings.TrimPrefix(path, "/")

	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.maybeAutoPruneLocked()

	key := clientIP + ":" + cleanPath
	rt.pathMIDs[key] = midStr
	rt.touchPathLocked(key)
	rt.evictPathIfNeededLocked()

	rt.touchClientLocked(clientIP)
	rt.evictClientIfNeededLocked()
}

// getMapping retrieves a cached MID for a path and client IP.
func (rt *RefererTracker) getMapping(clientIP, path string) (string, bool) {
	cleanPath := "/" + strings.TrimPrefix(path, "/")
	key := clientIP + ":" + cleanPath

	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.maybeAutoPruneLocked()

	val, ok := rt.pathMIDs[key]
	if ok {
		rt.touchPathLocked(key)
		rt.touchClientLocked(clientIP)
	}
	return val, ok
}

// getRecentMIDs returns recently active MIDs for a client IP.
func (rt *RefererTracker) getRecentMIDs(clientIP string) []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.maybeAutoPruneLocked()

	list := rt.recentMIDs[clientIP]
	if len(list) > 0 {
		rt.touchClientLocked(clientIP)
	}
	out := make([]string, len(list))
	copy(out, list)
	return out
}

func (rt *RefererTracker) maybeAutoPruneLocked() {
	if time.Since(rt.lastPrune) > 10*time.Minute {
		rt.lastPrune = time.Now()
		rt.pruneInactiveClientsLocked(1 * time.Hour)
	}
}

func (rt *RefererTracker) pruneInactiveClientsLocked(maxAge time.Duration) int {
	now := time.Now()
	pruned := 0

	n := 0
	for _, clientIP := range rt.clientOrder {
		lastActive, ok := rt.clientLastActive[clientIP]
		if ok && now.Sub(lastActive) > maxAge {
			delete(rt.recentMIDs, clientIP)
			delete(rt.clientLastActive, clientIP)
			rt.purgePathMIDsForClientLocked(clientIP)
			pruned++
		} else {
			rt.clientOrder[n] = clientIP
			n++
		}
	}
	rt.clientOrder = rt.clientOrder[:n]
	return pruned
}

// PruneInactiveClients removes all client IP entries and path mappings inactive for longer than maxAge.
func (rt *RefererTracker) PruneInactiveClients(maxAge time.Duration) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.pruneInactiveClientsLocked(maxAge)
}

// getClientIP extracts the client IP address from the request.
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Resolve identifies which MID a path-based request belongs to.
// It returns the MID string, relative path within the MID, and true if found.
func (rt *RefererTracker) Resolve(r *http.Request, backend Backend, nsResolver MemNSResolver) (string, string, bool) {
	clientIP := getClientIP(r)
	reqPath := r.URL.Path
	trimmedInnerPath := strings.TrimPrefix(reqPath, "/")

	// Check if path itself is /mem/MID/...
	if strings.HasPrefix(reqPath, "/mem/") {
		trimmed := strings.TrimPrefix(reqPath, "/mem/")
		parts := strings.Split(trimmed, "/")
		if len(parts) > 0 && parts[0] != "" {
			if _, err := mid.Parse(parts[0]); err == nil {
				rt.RecordActiveMID(clientIP, parts[0])
				return parts[0], strings.Join(parts[1:], "/"), true
			}
		}
	}

	ref := r.Header.Get("Referer")
	var candidateMID string

	if ref != "" {
		refURL, err := url.Parse(ref)
		if err == nil {
			refPath := refURL.Path
			var prefix string
			if strings.HasPrefix(refPath, "/mem/") {
				prefix = "/mem/"
			} else if strings.HasPrefix(refPath, "/memns/") {
				prefix = "/memns/"
			} else if strings.HasPrefix(refPath, "/memlink/") {
				prefix = "/memlink/"
			}

			if prefix != "" {
				trimmed := strings.TrimPrefix(refPath, prefix)
				parts := strings.Split(trimmed, "/")
				if len(parts) > 0 && parts[0] != "" {
					identifier := parts[0]
					if prefix == "/mem/" {
						candidateMID = identifier
					} else if nsResolver != nil {
						resolved, err := nsResolver.Resolve(r.Context(), identifier)
						if err == nil && resolved != "" {
							candidateMID = resolved
							if strings.HasPrefix(candidateMID, "/mem/") {
								candidateMID = candidateMID[5:]
							}
						}
					}
				}
			} else {
				// Referer path is absolute. Trace the Referer chain using the path cache
				if val, ok := rt.getMapping(clientIP, refPath); ok {
					candidateMID = val
				}
			}
		}
	}

	// Try the candidate MID from Referer
	if candidateMID != "" {
		if _, err := mid.Parse(candidateMID); err == nil {
			rt.RecordMapping(clientIP, reqPath, candidateMID)
			rt.RecordActiveMID(clientIP, candidateMID)
			return candidateMID, trimmedInnerPath, true
		}
	}

	// Fallback 1: check recently active MIDs for this client
	recent := rt.getRecentMIDs(clientIP)
	for _, activeMID := range recent {
		if activeMID == candidateMID {
			continue // Already checked
		}
		if root, err := mid.Parse(activeMID); err == nil {
			// Purely local check first: do not trigger network lookups for missing active MIDs
			if _, statErr := backend.Stat(r.Context(), root); statErr == nil {
				if _, err := backend.MemFSPathInfo(r.Context(), root, trimmedInnerPath); err == nil {
					rt.RecordMapping(clientIP, reqPath, activeMID)
					return activeMID, trimmedInnerPath, true
				}
			}
		}
	}

	return "", "", false
}
