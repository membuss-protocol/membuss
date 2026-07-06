package memgate_v2

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/nnlgsakib/membuss/core/mid"
)

// MemNSResolver defines the interface for resolving MemNS names.
type MemNSResolver interface {
	Resolve(ctx context.Context, name string) (string, error)
}

// RefererTracker maps absolute asset request paths to their corresponding MIDs
// dynamically using the Referer chain, active MIDs list, and cookies.
type RefererTracker struct {
	mu           sync.RWMutex
	pathMIDs     map[string]string // Key: clientIP + ":" + path, Value: midStr
	recentMIDs   []string          // MRU list of recently active MIDs
	maxCacheSize int
}

// NewRefererTracker constructs a RefererTracker.
func NewRefererTracker() *RefererTracker {
	return &RefererTracker{
		pathMIDs:     make(map[string]string),
		recentMIDs:   make([]string, 0, 20),
		maxCacheSize: 2000,
	}
}

// RecordActiveMID registers a MID in the recently active list.
func (rt *RefererTracker) RecordActiveMID(midStr string) {
	if midStr == "" {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Move to front if already exists
	for i, m := range rt.recentMIDs {
		if m == midStr {
			rt.recentMIDs = append(rt.recentMIDs[:i], rt.recentMIDs[i+1:]...)
			break
		}
	}

	rt.recentMIDs = append([]string{midStr}, rt.recentMIDs...)
	if len(rt.recentMIDs) > 20 {
		rt.recentMIDs = rt.recentMIDs[:20]
	}
}

// RecordMapping stores the association of a request path to a MID for a client IP.
func (rt *RefererTracker) RecordMapping(clientIP, path, midStr string) {
	if path == "" || midStr == "" {
		return
	}
	cleanPath := "/" + strings.TrimPrefix(path, "/")

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if len(rt.pathMIDs) >= rt.maxCacheSize {
		rt.pathMIDs = make(map[string]string) // evict all if limit reached
	}

	key := clientIP + ":" + cleanPath
	rt.pathMIDs[key] = midStr
}

// getMapping retrieves a cached MID for a path and client IP.
func (rt *RefererTracker) getMapping(clientIP, path string) (string, bool) {
	cleanPath := "/" + strings.TrimPrefix(path, "/")
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	key := clientIP + ":" + cleanPath
	val, ok := rt.pathMIDs[key]
	return val, ok
}

// getRecentMIDs returns recently active MIDs.
func (rt *RefererTracker) getRecentMIDs() []string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]string, len(rt.recentMIDs))
	copy(out, rt.recentMIDs)
	return out
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
				rt.RecordActiveMID(parts[0])
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
			rt.RecordActiveMID(candidateMID)
			return candidateMID, trimmedInnerPath, true
		}
	}

	// Fallback 1: check recently active MIDs
	recent := rt.getRecentMIDs()
	for _, activeMID := range recent {
		if activeMID == candidateMID {
			continue // Already checked
		}
		if root, err := mid.Parse(activeMID); err == nil {
			if _, err := backend.MemFSPathInfo(r.Context(), root, trimmedInnerPath); err == nil {
				rt.RecordMapping(clientIP, reqPath, activeMID)
				return activeMID, trimmedInnerPath, true
			}
		}
	}

	// Fallback 2: check Cookie
	if cookie, err := r.Cookie("membuss_gateway_mid"); err == nil && cookie.Value != "" {
		cookieMID := cookie.Value
		if cookieMID != candidateMID {
			if root, err := mid.Parse(cookieMID); err == nil {
				if _, err := backend.MemFSPathInfo(r.Context(), root, trimmedInnerPath); err == nil {
					rt.RecordMapping(clientIP, reqPath, cookieMID)
					rt.RecordActiveMID(cookieMID)
					return cookieMID, trimmedInnerPath, true
				}
			}
		}
	}

	return "", "", false
}
