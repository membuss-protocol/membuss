// Package memgate_v2 provides the public HTTP gateway and CDN layer
// for Membuss. It supports path-based gateway routes, subdomain-based
// resolution for SPAs, custom domain mapping via MemNS, and caching.
package memgate_v2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/mid"
)

// // Backend is the contract Mem-Gate depends on. The daemon
// supplies a real implementation; tests inject a memBackend.
type Backend interface {
	// Resolve returns a streaming reader of the content
	// addressed by m. The caller is responsible for closing
	// the reader. The size is the total content size in
	// bytes; it is used for Content-Length / Range math.
	Resolve(ctx context.Context, m mid.MID) (io.ReadCloser, ContentInfo, error)
	// ResolveWithProgress returns a streaming reader of the content while reporting progress updates.
	ResolveWithProgress(ctx context.Context, m mid.MID, progressFn func(ProgressUpdate)) (io.ReadCloser, ContentInfo, error)
	// RawBlock returns the bytes of a single block (no DAG
	// walk). Used by ?format=raw.
	RawBlock(ctx context.Context, m mid.MID) ([]byte, error)
	// DAGNodeJSON returns the DAG node at m serialized as
	// JSON: {"mid":..., "links":[...], "size":...}.
	DAGNodeJSON(ctx context.Context, m mid.MID) ([]byte, error)
	// Stat returns a quick metadata snapshot for HEAD.
	Stat(ctx context.Context, m mid.MID) (ContentInfo, error)
	// Ping returns nil if the backend is healthy.
	Ping(ctx context.Context) error

	// MemFSInfo describes a MemFS node. Type is one of
	// "file", "dir", "symlink", "metadata", "raw".
	MemFSInfo(ctx context.Context, m mid.MID) (MemFSInfo, error)
	// MemFSPathGet returns a streaming reader for the file
	// at m/path, with its size and MIME type.
	MemFSPathGet(ctx context.Context, m mid.MID, path string) (io.ReadSeekCloser, uint64, string, error)
	// MemFSList returns the entries of a MemFS directory,
	// sorted lexicographically by name.
	MemFSList(ctx context.Context, m mid.MID) ([]MemFSEntry, error)
	// MemFSPathInfo returns metadata about a path under the given root.
	MemFSPathInfo(ctx context.Context, m mid.MID, path string) (MemFSInfo, error)
	// MemFSPathList returns entries of a directory under root at path.
	MemFSPathList(ctx context.Context, m mid.MID, path string) ([]MemFSEntry, error)

	// --- Phase 21: Descriptor support ---

	// Descriptor returns the .mbuss descriptor bytes for the
	// given MID.
	Descriptor(ctx context.Context, m mid.MID) ([]byte, error)
}

// MemFSInfo is the metadata returned by Backend.MemFSInfo.
type MemFSInfo struct {
	MID   string
	Type  string
	Size  uint64
	Mode  uint32
	MTime int64
	Mime  string
}

// MemFSEntry is one row of a MemFS directory listing.
type MemFSEntry struct {
	Name string `json:"name"`
	MID  string `json:"mid"`
	Type string `json:"type"`
	Size uint64 `json:"size"`
}

// ContentInfo is the metadata returned by Backend.Resolve and
// Backend.Stat. It mirrors the X-Membuss-* response headers.
type ContentInfo struct {
	// MID is the canonical string form of the content's
	// root identifier.
	MID string
	// Size is the total content size in bytes.
	Size uint64
	// Blocks is the number of DAG nodes (or raw blocks)
	// the content consists of.
	Blocks uint64
	// ContentType, if non-empty, is the MIME type returned
	// in the Content-Type response header. Phase 19:
	// preferred over the extension-based sniff when the
	// uploader supplied an explicit MimeType.
	ContentType string
	// Sealed is true if the content is currently pinned in
	// the local store. Surfaced as X-Membuss-Sealed.
	Sealed bool
	// Name is the file name the uploader supplied (or the
	// basename of the source file when nothing was set).
	// Surfaced as the filename= parameter of
	// Content-Disposition and as X-Membuss-Name.
	Name string
	// MimeType is the explicit MIME type the uploader
	// supplied (or empty, in which case Mem-Gate falls
	// back to a filepath-extension sniff and then
	// application/octet-stream). Surfaced as
	// X-Membuss-MimeType.
	MimeType string
}

// Config configures a MemGate.
type Config struct {
	// Backend serves the actual content. Required.
	Backend Backend
	// MaxCacheBytes caps the in-memory LRU cache. Zero
	// disables caching. Defaults to 64 MiB.
	MaxCacheBytes uint64
	// MaxCacheItemBytes caps the size of a single cacheable
	// response. Responses larger than this are streamed to the
	// client instead of being buffered into memory and cached,
	// which bounds per-request heap use under concurrency. Zero
	// defaults to 8 MiB; it is clamped to MaxCacheBytes.
	MaxCacheItemBytes uint64
	// ReadTimeout is the per-request read timeout. Defaults
	// to 30s.
	ReadTimeout time.Duration
	// WriteTimeout is the per-request write timeout.
	// Defaults to 60s.
	WriteTimeout time.Duration
	// ExplorerHandler, if non-nil, is mounted under
	// /explorer/* on the same router. The Mem-Gate
	// caller is responsible for constructing the
	// explorer with the appropriate backend.
	ExplorerHandler http.Handler
	// RateLimitPerMin is the per-source-IP request budget
	// enforced on the public Mem-Gate server, evaluated
	// per minute. Zero disables rate limiting. Default 100.
	RateLimitPerMin int

	// Phase 18: MemNS resolver
	MemNSResolver *memns.Resolver
	LogLevel      string

	// EnableMetrics enables the Prometheus & OpenTelemetry edge metrics endpoint. Defaults to true.
	EnableMetrics bool
	// MetricsToken, if set, requires Authorization: Bearer <MetricsToken> or ?token=<MetricsToken>.
	// When empty, metrics endpoints are restricted to localhost/internal connections only.
	MetricsToken string
}

// MemGate is the public HTTP gateway.
type MemGate struct {
	cfg             Config
	router          chi.Router
	lru             *shardedLRU
	blockLists      *blockListCache
	ipLimiter       *ipLimiter
	refererTracker  *RefererTracker
	downloadManager *DownloadManager
	sfBlockList     singleflight.Group
	sfResolve       singleflight.Group
	metrics         *gatewayMetrics
}

// New returns a MemGate ready to be served. The returned
// router exposes /mem/{mid}/... and a /healthz endpoint.
func New(cfg Config) (*MemGate, error) {
	if cfg.Backend == nil {
		return nil, errors.New("memgate: nil backend")
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 60 * time.Second
	}
	if cfg.MaxCacheBytes == 0 {
		cfg.MaxCacheBytes = 64 * 1024 * 1024
	}
	if cfg.MaxCacheItemBytes == 0 {
		// Cap the size of any single cached item so one large
		// response cannot dominate the cache or blow up per-request
		// heap use. Items over this size are streamed, not buffered.
		cfg.MaxCacheItemBytes = 8 * 1024 * 1024
	}
	// A single item can never exceed the whole cache.
	if cfg.MaxCacheItemBytes > cfg.MaxCacheBytes {
		cfg.MaxCacheItemBytes = cfg.MaxCacheBytes
	}

	// Choose the shard count so every cacheable item can actually fit
	// in one shard. Otherwise an item larger than a shard's budget
	// would pass the eligibility check, get buffered into memory, then
	// be evicted immediately on put — paying the buffering cost for
	// nothing. Each shard must hold at least MaxCacheItemBytes.
	shardCount := defaultCacheShards
	if cfg.MaxCacheItemBytes > 0 {
		maxShards := int(cfg.MaxCacheBytes / cfg.MaxCacheItemBytes)
		if maxShards < 1 {
			maxShards = 1
		}
		if shardCount > maxShards {
			shardCount = maxShards
		}
	}

	mg := &MemGate{
		cfg:             cfg,
		lru:             newShardedLRU(cfg.MaxCacheBytes, shardCount),
		blockLists:      newBlockListCache(defaultBlockListCacheEntries),
		refererTracker:  NewRefererTracker(),
		downloadManager: NewDownloadManager(),
		metrics:         newGatewayMetrics(),
	}
	mg.ipLimiter = newIPLimiter(cfg.RateLimitPerMin, 10*time.Minute)
	mg.router = mg.buildRouter()
	return mg, nil
}

// Close gracefully shuts down background workers, timers, and active download tasks.
func (m *MemGate) Close() error {
	if m.ipLimiter != nil {
		m.ipLimiter.Stop()
	}
	if m.downloadManager != nil {
		m.downloadManager.Close()
	}
	return nil
}

// Shutdown gracefully shuts down MemGate before the context deadline expires.
func (m *MemGate) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		_ = m.Close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Router returns the chi router. Exposed so tests can drive
// the gateway via httptest.
func (m *MemGate) Router() http.Handler { return m.router }

// ChiRouter returns the underlying chi.Router for custom route mounting.
func (m *MemGate) ChiRouter() chi.Router { return m.router }

func (m *MemGate) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		clientIP := getClientIP(r)
		_, _, isSubdomain := m.resolveSubdomain(r)
		systemPath := isSystemPath(path)

		// 1. Subdomain routing (skip for system paths)
		if !systemPath && isSubdomain {
			if midStr, innerPath, ok := m.resolveSubdomain(r); ok {
				if innerPath == "~status" {
					m.handleFetchStatusSSE(w, r)
					return
				}
				m.refererTracker.RecordActiveMID(clientIP, midStr)
				root, err := mid.Parse(midStr)
				if err == nil {
					info, err := m.cfg.Backend.MemFSInfo(r.Context(), root)
					if err == nil && info.Type == "dir" {
						m.serveMemFSPath(w, r, midStr, innerPath)
						return
					}
					if innerPath == "" {
						m.handleResolved(w, r, root, midStr)
						return
					}
				}
				m.serveMemFSPath(w, r, midStr, innerPath)
				return
			}
		}

		// Record active MID if direct path-based route /mem/{mid} is accessed
		if strings.HasPrefix(path, "/mem/") {
			trimmed := strings.TrimPrefix(path, "/mem/")
			parts := strings.Split(trimmed, "/")
			if len(parts) > 0 && parts[0] != "" {
				if _, err := mid.Parse(parts[0]); err == nil {
					m.refererTracker.RecordActiveMID(clientIP, parts[0])
				}
			}
		} else if strings.HasPrefix(path, "/memns/") {
			trimmed := strings.TrimPrefix(path, "/memns/")
			parts := strings.Split(trimmed, "/")
			if len(parts) > 0 && parts[0] != "" {
				if m.cfg.MemNSResolver != nil {
					resolved, err := m.cfg.MemNSResolver.Resolve(r.Context(), parts[0])
					if err == nil && resolved != "" {
						midStr := resolved
						if strings.HasPrefix(midStr, "/mem/") {
							midStr = midStr[5:]
						}
						m.refererTracker.RecordActiveMID(clientIP, midStr)
					}
				}
			}
		}

		// 2. Referer-based path resolution for absolute assets on path-based gateways.
		//    Skip for root "/" — it must always reach the router so that
		//    customDomainMiddleware can resolve the hostname properly.
		if !systemPath && path != "/" {
			if midStr, innerPath, ok := m.refererTracker.Resolve(r, m.cfg.Backend, m.cfg.MemNSResolver); ok {
				m.serveMemFSPath(w, r, midStr, innerPath)
				return
			}
		}

		m.router.ServeHTTP(w, r)
	})
}

// isSystemPath evaluates if a path is a dynamic node control route.
func isSystemPath(path string) bool {
	if strings.HasPrefix(path, "/mem/") ||
		strings.HasPrefix(path, "/healthz") ||
		strings.HasPrefix(path, "/metrics") ||
		strings.HasPrefix(path, "/memns/") ||
		strings.HasPrefix(path, "/memlink/") {
		return true
	}
	if path == "/explorer" || strings.HasPrefix(path, "/explorer/") {
		// Dynamic APIs, WebSockets, and metrics are system paths.
		// Others (like SvelteKit _app assets, images, etc.) are content paths.
		return strings.HasPrefix(path, "/explorer/api") ||
			strings.HasPrefix(path, "/explorer/ws") ||
			strings.HasPrefix(path, "/explorer/metrics")
	}
	return false
}

// resolveRefererMID checks if the request's Referer header points to a Membuss gateway path.
// If it does, it returns the MID and the inner path.
func (m *MemGate) resolveRefererMID(r *http.Request) (string, string, bool) {
	return m.refererTracker.Resolve(r, m.cfg.Backend, m.cfg.MemNSResolver)
}

// resolveSubdomain checks if the host uses a subdomain representing either a MID or a MemNS name.
// It returns the resolved MID string, the inner path, and true if matched.
func (m *MemGate) resolveSubdomain(r *http.Request) (string, string, bool) {
	host := r.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", "", false
	}

	firstLabel := labels[0]
	if firstLabel == "localhost" || firstLabel == "127" || firstLabel == "www" || firstLabel == "gateway" {
		return "", "", false
	}

	// 1. Check if first label is a valid MID
	if _, err := mid.Parse(firstLabel); err == nil {
		innerPath := strings.TrimPrefix(r.URL.Path, "/")
		return firstLabel, innerPath, true
	}

	// 2. Check if first label or full host resolves via MemNS / DNSLink
	if m.cfg.MemNSResolver != nil {
		resolved, err := m.cfg.MemNSResolver.Resolve(r.Context(), host)
		if err != nil {
			resolved, err = m.cfg.MemNSResolver.Resolve(r.Context(), firstLabel)
		}
		if err == nil && resolved != "" {
			midStr := resolved
			if strings.HasPrefix(midStr, "/mem/") {
				midStr = midStr[5:]
			}
			innerPath := strings.TrimPrefix(r.URL.Path, "/")
			return midStr, innerPath, true
		}
	}

	return "", "", false
}

func (m *MemGate) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Compress(5,
		"text/html",
		"text/css",
		"text/plain",
		"text/xml",
		"text/javascript",
		"application/javascript",
		"application/x-javascript",
		"application/json",
		"application/xml",
		"image/svg+xml",
		"application/wasm",
	))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/explorer") {
				next.ServeHTTP(w, r)
				return
			}
			if strings.ToLower(m.cfg.LogLevel) == "debug" {
				middleware.Logger(next).ServeHTTP(w, r)
			} else {
				next.ServeHTTP(w, r)
			}
		})
	})
	r.Use(middleware.Recoverer)
	// Rate limit BEFORE route dispatch so a flood cannot pin
	// a single request handler. /healthz is intentionally not
	// exempted; operators who want it open can disable the
	// limiter by setting RateLimitPerMin=0 in config.
	r.Use(m.ipLimiter.Middleware)

	if m.metrics != nil {
		r.Use(m.metrics.middleware)
	}

	// Explorer is a local admin UI — block non-localhost access
	r.Use(m.localOnlyMiddleware)

	// Phase 18: custom domain serving middleware
	r.Use(m.customDomainMiddleware)

	r.Get("/healthz", m.handleHealth)
	if m.metrics != nil {
		metricsHandler := m.metrics.authMiddleware(m.cfg.MetricsToken, m.metrics.handler())
		r.Handle("/metrics", metricsHandler)
		r.Handle("/explorer/metrics", metricsHandler)
	}
	r.Get("/mem/{mid}", m.handleGet)
	r.Get("/mem/{mid}/~status", m.handleFetchStatusSSE)
	r.Head("/mem/{mid}", m.handleHead)
	// Directory listing (HTML or JSON) when the path
	// component is empty.
	r.Get("/mem/{mid}/", m.handleDirList)
	r.Get("/mem/{mid}/*", m.handlePathGet)

	// Phase 18: MemNS and MemLink routes
	r.Get("/memns/{name}", m.handleMemNSResolve)
	r.Get("/memns/{name}/*", m.handleMemNSResolve)
	r.Get("/memlink/{domain}", m.handleMemLinkGet)
	r.Get("/memlink/{domain}/*", m.handleMemLinkGet)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if m.cfg.ExplorerHandler != nil {
			http.Redirect(w, r, "/explorer/", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Membuss Gateway</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0d1117; color: #c9d1d9; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
    .card { background: #161b22; padding: 2.5rem; border-radius: 12px; border: 1px solid #30363d; max-width: 500px; text-align: center; box-shadow: 0 8px 24px rgba(0,0,0,0.5); }
    h1 { color: #58a6ff; margin-top: 0; font-size: 1.8rem; }
    p { color: #8b949e; line-height: 1.6; }
    a.button { display: inline-block; background: #238636; color: #ffffff; padding: 0.6rem 1.2rem; border-radius: 6px; text-decoration: none; font-weight: 600; margin-top: 1rem; }
    a.button:hover { background: #2ea043; }
  </style>
</head>
<body>
  <div class="card">
    <h1>⚡ Membuss Gateway</h1>
    <p>Public Content-Addressed Storage & Gateway Layer</p>
    <a href="/explorer/" class="button">Launch Web Explorer</a>
  </div>
</body>
</html>`))
	})

	if m.cfg.ExplorerHandler != nil {
		r.Mount("/explorer", m.cfg.ExplorerHandler)
	}
	return r
}

// --- handlers ---

func (m *MemGate) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := m.cfg.Backend.Ping(r.Context()); err != nil {
		http.Error(w, "backend not ready: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *MemGate) handleHead(w http.ResponseWriter, r *http.Request) {
	midStr := chi.URLParam(r, "mid")
	root, err := mid.Parse(midStr)
	if err != nil {
		http.Error(w, "bad mid: "+err.Error(), http.StatusBadRequest)
		return
	}
	info, err := m.cfg.Backend.Stat(r.Context(), root)
	if err != nil {
		http.Error(w, "stat: "+err.Error(), http.StatusNotFound)
		return
	}
	h := w.Header()
	h.Set("X-Membuss-MID", info.MID)
	h.Set("X-Membuss-Size", strconv.FormatUint(info.Size, 10))
	h.Set("X-Membuss-Blocks", strconv.FormatUint(info.Blocks, 10))
	h.Set("X-Membuss-Sealed", strconv.FormatBool(info.Sealed))
	h.Set("ETag", `"`+info.MID+`"`)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatUint(info.Size, 10))
	// Content-Type: prefer the uploader-supplied MimeType,
	// fall back to the path-based ContentType the backend
	// filled in. Both are surfaced so the test suite and
	// direct API consumers can tell them apart.
	ct := info.MimeType
	if ct == "" {
		ct = info.ContentType
	}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("X-Membuss-Name", info.Name)
	h.Set("X-Membuss-MimeType", info.MimeType)
	h.Set("Cache-Control", "public, immutable, max-age=31536000")
	w.WriteHeader(http.StatusOK)
}

func (m *MemGate) handleGet(w http.ResponseWriter, r *http.Request) {
	midStr := chi.URLParam(r, "mid")
	root, err := mid.Parse(midStr)
	if err != nil {
		http.Error(w, "bad mid: "+err.Error(), http.StatusBadRequest)
		return
	}
	// All metadata and header decisions happen in handleResolved,
	// which resolves the content (including a network fetch on
	// first request) BEFORE writing the body. This guarantees the
	// gateway serves identical metadata to the explorer whether or
	// not the content was already local.
	switch r.URL.Query().Get("format") {
	case "dag-json":
		m.handleDAGJSON(w, r, root)
	case "raw":
		m.handleRawBlock(w, r, root)
	case "descriptor":
		m.handleDescriptor(w, r, root)
	default:
		m.handleResolved(w, r, root, midStr)
	}
}

// handleDirList renders a directory listing as HTML or JSON,
// depending on the ?format query parameter. The handler is
// mounted at /mem/{mid}/ (trailing slash, no path component).
func (m *MemGate) handleDirList(w http.ResponseWriter, r *http.Request) {
	midStr := chi.URLParam(r, "mid")
	root, err := mid.Parse(midStr)
	if err != nil {
		http.Error(w, "bad mid: "+err.Error(), http.StatusBadRequest)
		return
	}
	m.handleDirListForMID(w, r, root, midStr)
}

func (m *MemGate) handleDirListForMID(w http.ResponseWriter, r *http.Request, root mid.MID, midStr string) {
	info, err := m.cfg.Backend.MemFSInfo(r.Context(), root)
	if err != nil {
		http.Error(w, "memfs: "+err.Error(), http.StatusNotFound)
		return
	}
	if info.Type != "dir" {
		// Non-directory MIDs at the bare /mem/{mid}/ URL
		// fall back to the legacy resolved-content handler.
		m.handleResolved(w, r, root, midStr)
		return
	}
	entries, err := m.cfg.Backend.MemFSList(r.Context(), root)
	if err != nil {
		http.Error(w, "memfs: "+err.Error(), http.StatusNotFound)
		return
	}
	// Auto-serve index.html if it exists and ?format is not set
	if r.URL.Query().Get("format") == "" {
		hasIndex := false
		for _, e := range entries {
			if e.Name == "index.html" {
				hasIndex = true
				break
			}
		}
		if hasIndex {
			m.serveMemFSPath(w, r, midStr, "index.html")
			return
		} else {
			// Check commonDirs for index.html
			commonDirs := []string{"dist", "build", "public", "out"}
			for _, d := range commonDirs {
				if _, checkErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, path.Join(d, "index.html")); checkErr == nil {
					m.serveMemFSPath(w, r, midStr, path.Join(d, "index.html"))
					return
				}
			}
		}
	}
	m.renderDirectoryList(w, r, "Directory "+root.String(), root.String(), info.Size, entries)
}

// renderDirectoryList renders a directory listing to the client.
func (m *MemGate) renderDirectoryList(w http.ResponseWriter, r *http.Request, title string, midStr string, size uint64, entries []MemFSEntry) {
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mid":     midStr,
			"type":    "dir",
			"size":    size,
			"entries": entries,
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>%s | Membuss Gateway</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Archivo:wdth,wght@62..125,100..900&family=IBM+Plex+Mono:wght@300;400;500;600;700&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-color: #0c1416;
            --panel-bg: #111d20;
            --text-primary: #e9e2d2;
            --text-secondary: #8c887a;
            --accent-ochre: #e8a33d;
            --accent-verdigris: #57b79e;
            --border-color: rgba(233, 226, 210, 0.08);
            --row-hover: rgba(233, 226, 210, 0.03);
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: 'Archivo', -apple-system, BlinkMacSystemFont, sans-serif;
            background-color: var(--bg-color);
            color: var(--text-primary);
            min-height: 100vh;
            padding: 2rem 1rem;
            display: flex;
            justify-content: center;
        }
        .container {
            width: 100%%;
            max-width: 68rem;
        }
        .card {
            background-color: var(--panel-bg);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 2rem;
            box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
        }
        .header {
            margin-bottom: 2rem;
            border-bottom: 1px solid var(--border-color);
            padding-bottom: 1rem;
            display: flex;
            align-items: center;
            justify-content: space-between;
        }
        .title {
            font-size: 1.5rem;
            font-weight: 600;
            color: var(--text-primary);
            letter-spacing: -0.02em;
        }
        .brand {
            font-family: 'Archivo', sans-serif;
            font-weight: 800;
            font-size: 0.9rem;
            letter-spacing: 0.1em;
            color: var(--accent-ochre);
            display: flex;
            align-items: center;
            gap: 0.5rem;
        }
        .brand-icon {
            background: var(--accent-ochre);
            color: var(--bg-color);
            width: 1.5rem;
            height: 1.5rem;
            border-radius: 4px;
            display: flex;
            align-items: center;
            justify-content: center;
            font-weight: 900;
            font-size: 0.8rem;
        }
        table {
            width: 100%%;
            border-collapse: collapse;
            font-size: 0.9rem;
        }
        th {
            font-family: 'Archivo', sans-serif;
            text-transform: uppercase;
            font-size: 0.75rem;
            letter-spacing: 0.05em;
            color: var(--text-secondary);
            font-weight: 600;
            padding: 0.75rem 1rem;
            text-align: left;
            border-bottom: 2px solid var(--border-color);
        }
        td {
            padding: 1rem;
            border-bottom: 1px solid var(--border-color);
            vertical-align: middle;
        }
        tr:hover td {
            background-color: var(--row-hover);
        }
        a {
            color: var(--accent-ochre);
            text-decoration: none;
            transition: color 0.15s ease;
        }
        a:hover {
            color: #f4cd8a;
            text-decoration: underline;
        }
        .type-badge {
            display: inline-flex;
            align-items: center;
            padding: 0.25rem 0.5rem;
            border-radius: 4px;
            font-size: 0.75rem;
            font-weight: 500;
            text-transform: uppercase;
        }
        .type-dir {
            background: rgba(87, 183, 158, 0.1);
            color: var(--accent-verdigris);
            border: 1px solid rgba(87, 183, 158, 0.2);
        }
        .type-file {
            background: rgba(232, 163, 61, 0.08);
            color: var(--accent-ochre);
            border: 1px solid rgba(232, 163, 61, 0.15);
        }
        .mono {
            font-family: 'IBM Plex Mono', ui-monospace, monospace;
        }
        .muted-link {
            color: var(--text-secondary);
        }
        .muted-link:hover {
            color: var(--text-primary);
        }
        .footer {
            margin-top: 2rem;
            padding-top: 1rem;
            border-top: 1px solid var(--border-color);
            font-size: 0.75rem;
            color: var(--text-secondary);
            display: flex;
            justify-content: space-between;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="card">
            <div class="header">
                <div class="title">%s</div>
                <div class="brand">
                    <div class="brand-icon">M</div>
                    <span>MEMBUSS GATEWAY</span>
                </div>
            </div>
            <table>
                <thead>
                    <tr>
                        <th>Name</th>
                        <th>Type</th>
                        <th>Size</th>
                        <th>MID</th>
                    </tr>
                </thead>
                <tbody>`,
		html.EscapeString(title), html.EscapeString(title))
	for _, e := range entries {
		href := url.PathEscape(e.Name)
		typeClass := "type-file"
		if e.Type == "dir" {
			href += "/"
			typeClass = "type-dir"
		}
		fmt.Fprintf(w, `<tr><td><a href="%s">%s</a></td>`+
			`<td><span class="type-badge %s">%s</span></td>`+
			`<td class="mono">%d B</td>`+
			`<td class="mono"><a href="/mem/%s" class="muted-link">%s…</a></td></tr>`,
			href, html.EscapeString(e.Name), typeClass, html.EscapeString(e.Type), e.Size, url.PathEscape(e.MID), html.EscapeString(shortMID(e.MID)))
	}
	fmt.Fprintf(w, `</tbody>
            </table>
            <div class="footer">
                <span>Mem-Gate • Content-Addressable CDN</span>
                <span>v1.9.1</span>
            </div>
        </div>
    </div>
</body>
</html>`)
}

// shortMID returns the first 16 characters of a public MID,
// suitable for display in a table. The remainder is hidden
// so a directory listing does not become a wall of text.
func shortMID(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}

func (m *MemGate) handlePathGet(w http.ResponseWriter, r *http.Request) {
	midStr := chi.URLParam(r, "mid")
	innerPath := chi.URLParam(r, "*")
	m.serveMemFSPath(w, r, midStr, innerPath)
}

func (m *MemGate) handleDAGJSON(w http.ResponseWriter, r *http.Request, root mid.MID) {
	etagVal := root.String()
	if checkETag(w, r, etagVal) {
		return
	}

	cacheKey := "dagjson:" + root.String()
	if data, ok := m.lru.get(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Membuss-MID", root.String())
		w.Header().Set("ETag", `"`+etagVal+`"`)
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	body, err := m.cfg.Backend.DAGNodeJSON(r.Context(), root)
	if err != nil {
		http.Error(w, "dag-json: "+err.Error(), http.StatusNotFound)
		return
	}
	m.lru.put(cacheKey, body)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Membuss-MID", root.String())
	w.Header().Set("ETag", `"`+etagVal+`"`)
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (m *MemGate) handleRawBlock(w http.ResponseWriter, r *http.Request, root mid.MID) {
	etagVal := root.String()
	if checkETag(w, r, etagVal) {
		return
	}

	cacheKey := "raw:" + root.String()
	if data, ok := m.lru.get(cacheKey); ok {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("X-Membuss-MID", root.String())
		w.Header().Set("ETag", `"`+etagVal+`"`)
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	data, err := m.cfg.Backend.RawBlock(r.Context(), root)
	if err != nil {
		http.Error(w, "raw: "+err.Error(), http.StatusNotFound)
		return
	}
	m.lru.put(cacheKey, data)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Membuss-MID", root.String())
	w.Header().Set("ETag", `"`+etagVal+`"`)
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleDescriptor serves the .mbuss descriptor file for a MID.
func (m *MemGate) handleDescriptor(w http.ResponseWriter, r *http.Request, root mid.MID) {
	data, err := m.cfg.Backend.Descriptor(r.Context(), root)
	if err != nil {
		http.Error(w, "descriptor: "+err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.mbuss", root.String()))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleResolved serves the resolved content. The hot path
// is: cache hit -> write directly. Miss -> Backend.Resolve ->
// copy with optional range slicing. The content is resolved
// FIRST so its metadata (name, MIME type, size, directory
// flag) is authoritative even when the content had to be
// fetched from the network on this very request — the case
// the gateway used to get wrong.
func (m *MemGate) handleResolved(w http.ResponseWriter, r *http.Request, root mid.MID, midStr string) {
	// Check if content is local. If not, and it's a browser request, trigger background fetch and show status page
	_, statErr := m.cfg.Backend.Stat(r.Context(), root)
	if statErr != nil {
		if strings.Contains(r.Header.Get("Accept"), "text/html") &&
			r.URL.Query().Get("format") == "" &&
			r.URL.Query().Get("download") != "1" {

			_, _ = m.downloadManager.GetOrCreateJob(root, m.cfg.Backend)

			_, _, isSubdomain := m.resolveSubdomain(r)
			var ssePath string
			if isSubdomain {
				ssePath = "/~status"
			} else {
				ssePath = fmt.Sprintf("/mem/%s/~status", midStr)
			}
			m.renderResolvePage(w, r, midStr, ssePath)
			return
		}
	}

	// RFC 7234 conditional validation
	if checkETag(w, r, midStr) {
		return
	}

	rc, info, err := m.cfg.Backend.Resolve(r.Context(), root)
	if err != nil {
		http.Error(w, "resolve: "+err.Error(), http.StatusNotFound)
		return
	}
	defer rc.Close()

	// Directory / website root: mirror the explorer by serving the
	// directory listing or index.html instead of dumping the raw
	// MemFS node bytes. Redirect to the trailing-slash route so the
	// directory handler (which auto-serves index.html) takes over.
	if info.MimeType == "inode/directory" || info.ContentType == "inode/directory" {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		m.handleDirListForMID(w, r, root, midStr)
		return
	}

	// Filename + disposition. Inline by default so the browser
	// renders renderable types; ?download=1 forces attachment.
	name := info.Name
	if name == "" {
		name = defaultFilename(midStr, info.ContentType)
	}

	// Content-Type: prefer the backend's resolved MIME; fall back
	// to a content sniff so renderable types (HTML, images, text)
	// still render correctly when no metadata was supplied.
	ct := chooseContentType(info.ContentType, DetectContentType(name, nil, info.MimeType))
	disp := contentDisposition(ct, info.Size)
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
		if fn := r.URL.Query().Get("filename"); fn != "" {
			name = fn
		}
	}

	cacheKey := "resolved:" + midStr
	if data, ok := m.lru.get(cacheKey); ok {
		if m.metrics != nil {
			m.metrics.cacheHitsTotal.Inc()
		}
		m.setResolvedHeaders(w, info, ct, disp, name)
		m.writeBytes(w, r, midStr, data, ct)
		return
	}
	if m.metrics != nil {
		m.metrics.cacheMissesTotal.Inc()
	}

	// Cache eligibility: for non-range requests under MaxCacheItemBytes, stream
	// directly to the response writer while capturing data into a buffer for LRU caching.
	// This eliminates TTFB latency and pre-buffering memory amplification under concurrency.
	if info.Size > 0 && info.Size <= m.cfg.MaxCacheItemBytes && r.Header.Get("Range") == "" {
		m.setResolvedHeaders(w, info, ct, disp, name)
		w.Header().Set("Content-Length", strconv.FormatUint(info.Size, 10))
		w.WriteHeader(http.StatusOK)

		var buf bytes.Buffer
		buf.Grow(int(info.Size))
		mw := io.MultiWriter(w, &buf)

		n, err := io.Copy(mw, rc)
		if err == nil && uint64(n) == info.Size {
			m.lru.put(cacheKey, buf.Bytes())
		}
		return
	}

	// Streamed path (too large to cache).
	m.setResolvedHeaders(w, info, ct, disp, name)

	if m.handleStreamRange(w, r, root, int64(info.Size)) {
		return
	}

	if info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatUint(info.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// setResolvedHeaders applies the common gateway response headers
// for a resolved content response, including the uploader-supplied
// name as a renderable (inline) Content-Disposition.
func (m *MemGate) setResolvedHeaders(w http.ResponseWriter, info ContentInfo, ct, disp, name string) {
	h := w.Header()
	h.Set("Content-Type", ct)
	cleanMID := strings.TrimSuffix(info.MID, "/")
	h.Set("X-Membuss-MID", cleanMID)
	h.Set("X-Membuss-Size", strconv.FormatUint(info.Size, 10))
	h.Set("X-Membuss-Blocks", strconv.FormatUint(info.Blocks, 10))
	h.Set("X-Membuss-Sealed", strconv.FormatBool(info.Sealed))
	h.Set("X-Membuss-Name", sanitizeFilename(info.Name))
	h.Set("X-Membuss-MimeType", info.MimeType)
	h.Set("ETag", `"`+cleanMID+`"`)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "public, immutable, max-age=31536000")
	// Once-only: don't overwrite a header the caller set explicitly.
	if h.Get("Content-Disposition") == "" {
		fn := sanitizeFilename(name)
		if fn == "" || fn == "download" || fn == "." {
			fn = defaultFilename(cleanMID, ct)
		}
		formatted := mime.FormatMediaType(disp, map[string]string{"filename": fn})
		if formatted == "" {
			formatted = disp + `; filename="` + sanitizeFilename(fn) + `"`
		}
		h.Set("Content-Disposition", formatted)
	}
}

// defaultFilename derives a download filename from the MID and
// content type when the uploader did not supply a name.
// Caps the MID portion at 16 characters so browsers (Chrome/Firefox)
// do not discard the name or fall back to "download".
func defaultFilename(midStr, ct string) string {
	ext := ".bin"
	base := ct
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		base = ct[:idx]
	}
	if exts, err := mime.ExtensionsByType(strings.TrimSpace(base)); err == nil && len(exts) > 0 {
		ext = exts[0]
	}
	shortMID := midStr
	if len(shortMID) > 16 {
		shortMID = shortMID[:16]
	}
	return shortMID + ext
}

// writeBytes writes data to w honoring an optional Range
// header. It sets the X-Membuss-* and caching headers
// expected by clients.
func (m *MemGate) writeBytes(w http.ResponseWriter, r *http.Request, midStr string, data []byte, contentType string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Membuss-MID", midStr)
	h.Set("X-Membuss-Size", strconv.Itoa(len(data)))
	if h.Get("ETag") == "" {
		h.Set("ETag", `"`+midStr+`"`)
	}
	h.Set("Accept-Ranges", "bytes")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "public, immutable, max-age=31536000")
	}

	if rng := r.Header.Get("Range"); rng != "" {
		start, end, err := parseRange(rng, int64(len(data)))
		if err != nil {
			http.Error(w, "bad range: "+err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, len(data)))
		h.Set("Content-Length", strconv.FormatInt(end-start, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start:end])
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// parseRange parses a single-range "bytes=start-end" header
// and returns the half-open [start,end) bounds. Multi-range
// requests are not supported (yet) and return an error.
func parseRange(s string, size int64) (int64, int64, error) {
	const prefix = "bytes="
	if !strings.HasPrefix(s, prefix) {
		return 0, 0, fmt.Errorf("range unit must be bytes")
	}
	spec := strings.TrimPrefix(s, prefix)
	if strings.Contains(spec, ",") {
		return 0, 0, fmt.Errorf("multi-range not supported")
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, fmt.Errorf("range missing dash")
	}
	startStr := strings.TrimSpace(spec[:dash])
	endStr := strings.TrimSpace(spec[dash+1:])

	if size < 0 {
		return 0, 0, fmt.Errorf("negative size")
	}
	var start, end uint64
	var err error
	if startStr == "" {
		// Suffix range: last N bytes.
		n, perr := strconv.ParseUint(endStr, 10, 64)
		if perr != nil || n == 0 {
			return 0, 0, fmt.Errorf("bad suffix length")
		}
		if n > uint64(size) {
			n = uint64(size)
		}
		start = uint64(size) - n
		end = uint64(size)
		return int64(start), int64(end), nil
	}
	start, err = strconv.ParseUint(startStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad start: %w", err)
	}
	if endStr == "" {
		end = uint64(size)
	} else {
		end, err = strconv.ParseUint(endStr, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("bad end: %w", err)
		}
		if size > 0 && end >= uint64(size-1) {
			end = uint64(size)
		} else {
			end++ // bytes=N-M is inclusive of M
		}
	}
	if start >= uint64(size) || end <= start || end > uint64(size) {
		return 0, 0, fmt.Errorf("range out of bounds")
	}
	return int64(start), int64(end), nil
}

func init() {
	// Seed common web MIME types to prevent discrepancies or missing records in standard OS registries (e.g. on Windows).
	_ = mime.AddExtensionType(".html", "text/html; charset=utf-8")
	_ = mime.AddExtensionType(".htm", "text/html; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".json", "application/json")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".txt", "text/plain; charset=utf-8")
	_ = mime.AddExtensionType(".png", "image/png")
	_ = mime.AddExtensionType(".jpg", "image/jpeg")
	_ = mime.AddExtensionType(".jpeg", "image/jpeg")
	_ = mime.AddExtensionType(".gif", "image/gif")
	_ = mime.AddExtensionType(".pdf", "application/pdf")
	_ = mime.AddExtensionType(".xml", "application/xml")
	_ = mime.AddExtensionType(".mp3", "audio/mpeg")
	_ = mime.AddExtensionType(".mp4", "video/mp4")
	_ = mime.AddExtensionType(".webm", "video/webm")
	_ = mime.AddExtensionType(".webp", "image/webp")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
	_ = mime.AddExtensionType(".otf", "font/otf")
	_ = mime.AddExtensionType(".ico", "image/x-icon")
}

// DetectContentType picks a MIME type using (in order):
//   1. The override/hint if provided (ignoring generic application/octet-stream).
//   2. Extension-based lookup via the standard library's registry (mime.TypeByExtension).
//   3. Content-based sniffing using the gabriel-vasile/mimetype library.
//   4. application/octet-stream as a fallback.
func DetectContentType(midStr string, data []byte, override string) string {
	if override != "" && override != "application/octet-stream" {
		if strings.HasPrefix(override, "application/javascript") || strings.HasPrefix(override, "text/javascript") {
			return "text/javascript; charset=utf-8"
		}
		if strings.HasPrefix(override, "text/css") {
			return "text/css; charset=utf-8"
		}
		return override
	}
	if ext := filepath.Ext(midStr); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	if len(data) > 0 {
		return mimetype.Detect(data).String()
	}
	return "application/octet-stream"
}

func chooseContentType(info, fallback string) string {
	if info != "" {
		return info
	}
	return fallback
}

// sanitizeFilename strips control characters, quotes, and invalid
// characters to prevent header and HTML injection.
func sanitizeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		switch r {
		case '"', '\\', '/', '<', '>', '|', ':', '*', '?':
			return '_'
		}
		return r
	}, s)
}

func checkETag(w http.ResponseWriter, r *http.Request, etagValue string) bool {
	ifNoneMatch := r.Header.Get("If-None-Match")
	if ifNoneMatch == "" {
		return false
	}
	cleanETag := strings.TrimPrefix(ifNoneMatch, "W/")
	cleanETag = strings.Trim(cleanETag, `"`)
	if cleanETag == etagValue {
		w.Header().Set("ETag", `"`+etagValue+`"`)
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// Phase 18: custom domain serving middleware.
func (m *MemGate) customDomainMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if idx := strings.Index(host, ":"); idx != -1 {
			host = host[:idx]
		}

		// Don't intercept localhost or local IPs
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path
		// Don't intercept system paths
		if strings.HasPrefix(path, "/mem/") ||
			strings.HasPrefix(path, "/healthz") ||
			strings.HasPrefix(path, "/metrics") ||
			strings.HasPrefix(path, "/explorer") ||
			strings.HasPrefix(path, "/memns/") ||
			strings.HasPrefix(path, "/memlink/") {
			next.ServeHTTP(w, r)
			return
		}

		if _, _, ok := m.resolveSubdomain(r); ok {
			next.ServeHTTP(w, r)
			return
		}

		if m.cfg.MemNSResolver == nil {
			http.Error(w, "MemNS resolver not configured", http.StatusInternalServerError)
			return
		}

		// Resolve domain to MID
		resolved, err := m.cfg.MemNSResolver.Resolve(r.Context(), host)
		if err != nil {
			if path == "/" {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, fmt.Sprintf("No DNSLink or MemLink record found for domain '%s'", host), http.StatusNotFound)
			return
		}

		midStr := resolved
		if strings.HasPrefix(midStr, "/mem/") {
			midStr = midStr[5:]
		}

		innerPath := strings.TrimPrefix(path, "/")
		m.serveMemFSPath(w, r, midStr, innerPath)
	})
}

// localOnlyMiddleware blocks /explorer access from non-localhost sources.
// The explorer is a local admin UI and must not be exposed to the public internet.
func (m *MemGate) localOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/explorer") {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if idx := strings.Index(host, ":"); idx != -1 {
			host = host[:idx]
		}
		if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "explorer not available on public gateway", http.StatusNotFound)
	})
}

func (m *MemGate) handleMemNSResolve(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	name = strings.Trim(name, "/")
	innerPath := chi.URLParam(r, "*")

	if m.cfg.MemNSResolver == nil {
		http.Error(w, "MemNS resolver not configured", http.StatusInternalServerError)
		return
	}

	resolved, err := m.cfg.MemNSResolver.Resolve(r.Context(), name)
	if err != nil {
		http.Error(w, "memns resolve failed: "+err.Error(), http.StatusNotFound)
		return
	}

	if strings.HasPrefix(resolved, "https://") || strings.HasPrefix(resolved, "/ipfs/") {
		http.Redirect(w, r, resolved, http.StatusTemporaryRedirect)
		return
	}

	midStr := resolved
	if strings.HasPrefix(midStr, "/mem/") {
		midStr = midStr[5:]
	} else if strings.HasPrefix(midStr, "mem/") {
		midStr = midStr[4:]
	}
	midStr = strings.TrimPrefix(midStr, "/")

	rootStr := midStr
	subpath := ""
	if idx := strings.Index(midStr, "/"); idx != -1 {
		rootStr = midStr[:idx]
		subpath = midStr[idx+1:]
	}

	if !strings.HasPrefix(rootStr, "mem") && strings.HasPrefix(rootStr, "b") {
		rootStr = "mem" + rootStr
	}

	rootMID, err := mid.Parse(rootStr)
	if err != nil {
		http.Error(w, "bad mid: "+err.Error(), http.StatusBadRequest)
		return
	}

	targetPath := strings.TrimPrefix(innerPath, "/")
	if targetPath == "" {
		targetPath = subpath
	}

	if targetPath == "" {
		m.handleResolved(w, r, rootMID, rootMID.String())
		return
	}

	m.serveMemFSPath(w, r, rootMID.String(), targetPath)
}

func (m *MemGate) handleMemLinkGet(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	innerPath := chi.URLParam(r, "*")

	if m.cfg.MemNSResolver == nil {
		http.Error(w, "MemNS resolver not configured", http.StatusInternalServerError)
		return
	}

	resolved, err := m.cfg.MemNSResolver.Resolve(r.Context(), domain)
	if err != nil {
		http.Error(w, "memlink resolve failed: "+err.Error(), http.StatusNotFound)
		return
	}

	midStr := resolved
	if strings.HasPrefix(midStr, "/mem/") {
		midStr = midStr[5:]
	}

	m.serveMemFSPath(w, r, midStr, innerPath)
}

func (m *MemGate) serveMemFSPath(w http.ResponseWriter, r *http.Request, midStr, innerPath string) {
	root, err := mid.Parse(midStr)
	if err != nil {
		http.Error(w, "bad mid: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check if root MID is local. If not, and it's a browser request, trigger background fetch and show status page
	_, statErr := m.cfg.Backend.Stat(r.Context(), root)
	if statErr != nil {
		if strings.Contains(r.Header.Get("Accept"), "text/html") &&
			r.URL.Query().Get("format") == "" &&
			r.URL.Query().Get("download") != "1" {

			_, _ = m.downloadManager.GetOrCreateJob(root, m.cfg.Backend)

			_, _, isSubdomain := m.resolveSubdomain(r)
			var ssePath string
			if isSubdomain {
				ssePath = "/~status"
			} else {
				ssePath = fmt.Sprintf("/mem/%s/~status", midStr)
			}
			m.renderResolvePage(w, r, midStr, ssePath)
			return
		}
	}

	// Dynamic base path redirect for SvelteKit / framework apps compiled with custom base paths
	if redirectURL, ok := m.checkBaseRedirect(r, root, innerPath); ok {
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	resolvedPath := innerPath
	pathInfo, err := m.cfg.Backend.MemFSPathInfo(r.Context(), root, resolvedPath)
	if err != nil && strings.Contains(err.Error(), "not found") {
		// Generic self-healing path resolution:
		// If the path is not found, try stripping the base segment and look for direct/referer/common build folder mappings
		var baseStrippedPath string
		parts := strings.SplitN(innerPath, "/", 2)
		if len(parts) == 2 {
			if parts[1] != "" {
				baseStrippedPath = parts[1]
			} else {
				baseStrippedPath = "index.html"
			}
		} else if len(parts) == 1 && parts[0] != "" {
			baseStrippedPath = "index.html"
		}

		if baseStrippedPath != "" {
			// 1. Check if base-stripped path exists directly (flat upload)
			if _, checkErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, baseStrippedPath); checkErr == nil {
				if baseStrippedPath == "index.html" && len(parts) == 1 {
					resolvedPath = ""
				} else {
					resolvedPath = baseStrippedPath
				}
				pathInfo, err = m.cfg.Backend.MemFSPathInfo(r.Context(), root, resolvedPath)
			} else {
				// 2. Check if it exists nested in a directory matched by the Referer (nested upload like dist/)
				refDir := m.getRefererDir(r, root)
				if refDir != "" {
					nestedPath := path.Join(refDir, baseStrippedPath)
					if _, checkErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, nestedPath); checkErr == nil {
						resolvedPath = nestedPath
						pathInfo, err = m.cfg.Backend.MemFSPathInfo(r.Context(), root, resolvedPath)
					}
				}
				// 3. Fallback: Auto-detect common build directories (dist, build, public, out)
				if err != nil && strings.Contains(err.Error(), "not found") {
					commonDirs := []string{"dist", "build", "public", "out"}
					for _, d := range commonDirs {
						nestedPath := path.Join(d, baseStrippedPath)
						if _, checkErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, nestedPath); checkErr == nil {
							resolvedPath = nestedPath
							pathInfo, err = m.cfg.Backend.MemFSPathInfo(r.Context(), root, resolvedPath)
							break
						}
					}
				}
			}
		}
	}

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// SPA fallback: Serve index.html if the requested path is not found,
			// the browser accepts HTML (navigation), and index.html exists at the root or common build dirs.
			if strings.Contains(r.Header.Get("Accept"), "text/html") && innerPath != "index.html" {
				if _, indexErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, "index.html"); indexErr == nil {
					m.serveMemFSPath(w, r, midStr, "index.html")
					return
				}
				commonDirs := []string{"dist", "build", "public", "out"}
				for _, d := range commonDirs {
					if _, indexErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, path.Join(d, "index.html")); indexErr == nil {
						m.serveMemFSPath(w, r, midStr, path.Join(d, "index.html"))
						return
					}
				}
			}
			http.Error(w, "not found: "+err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "memfs: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if pathInfo.Type == "dir" {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		entries, err := m.cfg.Backend.MemFSPathList(r.Context(), root, resolvedPath)
		if err != nil {
			http.Error(w, "memfs list: "+err.Error(), http.StatusNotFound)
			return
		}
		// Auto-serve index.html if it exists and ?format is not set
		if r.URL.Query().Get("format") == "" {
			hasIndex := false
			for _, e := range entries {
				if e.Name == "index.html" {
					hasIndex = true
					break
				}
			}
			if hasIndex {
				m.serveMemFSPath(w, r, midStr, path.Join(resolvedPath, "index.html"))
				return
			} else if resolvedPath == "" {
				// Check commonDirs for index.html if we are at the root
				commonDirs := []string{"dist", "build", "public", "out"}
				for _, d := range commonDirs {
					if _, checkErr := m.cfg.Backend.MemFSPathInfo(r.Context(), root, path.Join(d, "index.html")); checkErr == nil {
						m.serveMemFSPath(w, r, midStr, path.Join(d, "index.html"))
						return
					}
				}
			}
		}
		m.renderDirectoryList(w, r, "Directory "+midStr+"/"+resolvedPath, pathInfo.MID, pathInfo.Size, entries)
		return
	}

	etagVal := midStr + "/" + resolvedPath
	if checkETag(w, r, etagVal) {
		return
	}

	cacheKey := "path:" + midStr + ":" + resolvedPath
	if cachedBytes, ok := m.lru.get(cacheKey); ok {
		var cp struct {
			Data []byte `json:"data"`
			Mime string `json:"mime"`
		}
		if json.Unmarshal(cachedBytes, &cp) == nil {
			filename := filepath.Base(resolvedPath)
			w.Header().Set("Content-Type", cp.Mime)
			w.Header().Set("X-Membuss-MID", pathInfo.MID)
			w.Header().Set("X-Membuss-Path", "/"+resolvedPath)
			w.Header().Set("X-Membuss-Name", filename)
			w.Header().Set("X-Membuss-MimeType", cp.Mime)
			if w.Header().Get("Content-Disposition") == "" {
				disp := contentDisposition(cp.Mime, pathInfo.Size)
				w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": sanitizeFilename(filename)}))
			}
			w.Header().Set("ETag", `"`+etagVal+`"`)
			if cp.Mime == "text/html" || strings.HasSuffix(filename, ".html") {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			} else {
				w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
			}
			m.writeBytes(w, r, pathInfo.MID, cp.Data, cp.Mime)
			return
		}
	}

	rc, size, mimeType, err := m.cfg.Backend.MemFSPathGet(r.Context(), root, resolvedPath)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "not found: "+err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "memfs: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	defer rc.Close()

	ct := DetectContentType(resolvedPath, nil, mimeType)
	filename := filepath.Base(resolvedPath)

	// Only buffer+cache items small enough to fit under
	// MaxCacheItemBytes. Larger files fall through to the streaming
	// path below (which also serves Range requests), so a single
	// response never allocates more than MaxCacheItemBytes of heap.
	if size > 0 && size <= m.cfg.MaxCacheItemBytes {
		buf := make([]byte, size)
		if _, err := io.ReadFull(rc, buf); err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			http.Error(w, "read: "+err.Error(), http.StatusBadGateway)
			return
		}
		ct = DetectContentType(resolvedPath, buf, mimeType)
		cp := struct {
			Data []byte `json:"data"`
			Mime string `json:"mime"`
		}{
			Data: buf,
			Mime: ct,
		}
		if cpBytes, err := json.Marshal(cp); err == nil {
			m.lru.put(cacheKey, cpBytes)
		}

		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Membuss-MID", pathInfo.MID)
		w.Header().Set("X-Membuss-Path", "/"+resolvedPath)
		w.Header().Set("X-Membuss-Name", filename)
		w.Header().Set("X-Membuss-MimeType", ct)
		if w.Header().Get("Content-Disposition") == "" {
			disp := contentDisposition(ct, size)
			w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": sanitizeFilename(filename)}))
		}
		w.Header().Set("ETag", `"`+etagVal+`"`)
		if ct == "text/html" || strings.HasSuffix(filename, ".html") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		} else {
			w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		}
		m.writeBytes(w, r, pathInfo.MID, buf, ct)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Membuss-MID", pathInfo.MID)
	w.Header().Set("X-Membuss-Path", "/"+resolvedPath)
	w.Header().Set("X-Membuss-Name", filename)
	w.Header().Set("X-Membuss-MimeType", ct)
	if w.Header().Get("Content-Disposition") == "" {
		disp := contentDisposition(ct, size)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": sanitizeFilename(filename)}))
	}
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatUint(size, 10))
	}
	w.Header().Set("ETag", `"`+etagVal+`"`)
	w.Header().Set("Accept-Ranges", "bytes")
	if ct == "text/html" || strings.HasSuffix(filename, ".html") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	}

	fileMID, err := mid.Parse(pathInfo.MID)
	if err == nil {
		if m.handleStreamRange(w, r, fileMID, int64(size)) {
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (m *MemGate) handleStreamRange(w http.ResponseWriter, r *http.Request, fileMID mid.MID, totalSize int64) bool {
	rng := r.Header.Get("Range")
	if rng == "" {
		return false
	}

	start, end, err := parseRange(rng, totalSize)
	if err != nil {
		http.Error(w, "bad range: "+err.Error(), http.StatusRequestedRangeNotSatisfiable)
		return true
	}

	blocks, _, err := m.cachedBlockList(r.Context(), fileMID)
	if err != nil {
		http.Error(w, "build block list: "+err.Error(), http.StatusInternalServerError)
		return true
	}

	reader := newDagReader(r.Context(), m.cfg.Backend, m.lru, m.metrics, blocks, totalSize)
	defer reader.Close()

	if _, err := reader.Seek(start, io.SeekStart); err != nil {
		http.Error(w, "seek error: "+err.Error(), http.StatusInternalServerError)
		return true
	}

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, totalSize))
	w.Header().Set("Content-Length", strconv.FormatInt(end-start, 10))
	w.WriteHeader(http.StatusPartialContent)

	_, _ = io.CopyN(w, reader, end-start)
	return true
}

func (m *MemGate) checkBaseRedirect(r *http.Request, root mid.MID, innerPath string) (string, bool) {
	targetFile := innerPath
	if targetFile == "" {
		targetFile = "index.html"
	}
	if !strings.HasSuffix(targetFile, "index.html") {
		return "", false
	}

	// Try reading the file at targetFile
	rc, _, _, err := m.cfg.Backend.MemFSPathGet(r.Context(), root, targetFile)
	if err != nil && innerPath == "" {
		// If innerPath was empty and index.html wasn't at the root, check common build folders
		commonDirs := []string{"dist", "build", "public", "out"}
		for _, d := range commonDirs {
			rc, _, _, err = m.cfg.Backend.MemFSPathGet(r.Context(), root, path.Join(d, "index.html"))
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return "", false
	}
	defer rc.Close()

	buf := make([]byte, 8192)
	n, _ := io.ReadFull(rc, buf)
	content := string(buf[:n])

	base := detectFrameworkBase(content)
	if base != "" {
		relBase := strings.TrimPrefix(base, "/")
		redirectPath := r.URL.Path
		if strings.HasSuffix(redirectPath, "index.html") {
			redirectPath = strings.TrimSuffix(redirectPath, "index.html")
		}
		
		// Clean and compare path to see if base path prefix is already at the end
		cleanedPath := strings.TrimSuffix(redirectPath, "/")
		if strings.HasSuffix(cleanedPath, "/"+relBase) || cleanedPath == relBase || strings.HasSuffix(cleanedPath, "/"+base) {
			return "", false
		}

		// Also strip any /dist/ or /build/ prefix from the redirect path if we are redirecting to the base route
		commonDirs := []string{"dist", "build", "public", "out"}
		for _, d := range commonDirs {
			if strings.HasSuffix(cleanedPath, "/"+d) || cleanedPath == d {
				// Strip the prefix
				redirectPath = strings.TrimSuffix(cleanedPath, d)
				break
			}
		}

		if strings.HasSuffix(redirectPath, "/") {
			redirectPath = redirectPath + relBase + "/"
		} else {
			redirectPath = redirectPath + "/" + relBase + "/"
		}
		redirectPath = strings.ReplaceAll(redirectPath, "//", "/")
		return redirectPath, true
	}
	return "", false
}

// detectFrameworkBase inspects index.html content to find common compiled asset path prefix patterns.
func detectFrameworkBase(content string) string {
	suffixes := []string{"_app/", "assets/", "_nuxt/", "_next/", "static/"}
	for _, suffix := range suffixes {
		idx := strings.Index(content, suffix)
		if idx == -1 {
			continue
		}
		startQuote := -1
		for j := idx - 1; j >= 0; j-- {
			if content[j] == '"' || content[j] == '\'' || content[j] == '`' {
				startQuote = j
				break
			}
		}
		if startQuote == -1 {
			continue
		}
		pathSegment := content[startQuote+1 : idx]
		if strings.HasPrefix(pathSegment, "/") {
			base := strings.TrimSuffix(pathSegment, "/")
			if base != "" {
				return base
			}
		}
	}
	return ""
}

// getRefererDir parses the Referer header to extract the directory path inside the MID.
func (m *MemGate) getRefererDir(r *http.Request, root mid.MID) string {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return ""
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	refPath := refURL.Path

	// Strip the prefix (/mem/{mid}/, /memns/{name}/, /memlink/{domain}/, or /)
	inner := refPath
	if strings.HasPrefix(inner, "/mem/") {
		trimmed := strings.TrimPrefix(inner, "/mem/")
		parts := strings.Split(trimmed, "/")
		if len(parts) > 1 {
			inner = strings.Join(parts[1:], "/")
		}
	} else if strings.HasPrefix(inner, "/memns/") {
		trimmed := strings.TrimPrefix(inner, "/memns/")
		parts := strings.Split(trimmed, "/")
		if len(parts) > 1 {
			inner = strings.Join(parts[1:], "/")
		}
	} else if strings.HasPrefix(inner, "/memlink/") {
		trimmed := strings.TrimPrefix(inner, "/memlink/")
		parts := strings.Split(trimmed, "/")
		if len(parts) > 1 {
			inner = strings.Join(parts[1:], "/")
		}
	} else {
		inner = strings.TrimPrefix(inner, "/")
	}

	// Remove explorer prefix if present
	if strings.HasPrefix(inner, "explorer/") {
		inner = strings.TrimPrefix(inner, "explorer/")
	}

	// Now get the directory portion
	dir := path.Dir(inner)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

const MaxRenderSize = 50 * 1024 * 1024 // 50MB

// contentDisposition decides between "inline" and "attachment"
// for a resolved response.
//
// The gateway serves every response with Accept-Ranges and a
// working Range handler, so streamable media (video, audio,
// images, PDF) plays inline in the browser at any size — a
// 200 MB video is streamed in Range chunks, never buffered
// whole. Forcing "attachment" purely on size would make the
// browser download such files instead of playing them, which
// is exactly the "video won't play in the browser" symptom.
//
// The MaxRenderSize ceiling therefore only applies to opaque
// types (application/octet-stream and the like): those have no
// inline renderer, so a large one is better offered as a
// download than streamed into a tab that cannot display it.
func contentDisposition(ct string, size uint64) string {
	if isInlineStreamable(ct) {
		return "inline"
	}
	if size > MaxRenderSize {
		return "attachment"
	}
	return "inline"
}

// isInlineStreamable reports whether a content type has a
// native browser renderer that consumes the body via Range
// requests (so size is irrelevant to whether it should be
// inline). The check is on the media type only; parameters
// such as "; charset=utf-8" are ignored.
func isInlineStreamable(ct string) bool {
	base := ct
	if idx := strings.IndexByte(base, ';'); idx >= 0 {
		base = base[:idx]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	switch {
	case strings.HasPrefix(base, "video/"),
		strings.HasPrefix(base, "audio/"),
		strings.HasPrefix(base, "image/"):
		return true
	}
	switch base {
	case "application/pdf", "text/plain", "text/html":
		return true
	}
	return false
}

func (m *MemGate) handleFetchStatusSSE(w http.ResponseWriter, r *http.Request) {
	midStr := chi.URLParam(r, "mid")
	if midStr == "" {
		if resolvedMID, _, ok := m.resolveSubdomain(r); ok {
			midStr = resolvedMID
		}
	}
	root, err := mid.Parse(midStr)
	if err != nil {
		http.Error(w, "bad mid: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if m.metrics != nil {
		m.metrics.activeSSEStreams.Inc()
		defer m.metrics.activeSSEStreams.Dec()
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	job, _ := m.downloadManager.GetOrCreateJob(root, m.cfg.Backend)

	listenerID, ch := job.AddListenerWithContext(r.Context())
	defer job.RemoveListener(listenerID)

	state, errMsg, blocksResolved, blocksTotal, bytesDelivered, bytesTotal, throughput, eta := job.GetStatus()
	fmt.Fprintf(w, "data: {\"mid\":\"%s\",\"state\":\"%s\",\"error\":\"%s\",\"blocks_resolved\":%d,\"blocks_total\":%d,\"bytes_delivered\":%d,\"bytes_total\":%d,\"throughput\":%f,\"eta\":%f}\n\n",
		midStr, state, errMsg, blocksResolved, blocksTotal, bytesDelivered, bytesTotal, throughput, eta)
	flusher.Flush()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case _, ok := <-ch:
			state, errMsg, blocksResolved, blocksTotal, bytesDelivered, bytesTotal, throughput, eta = job.GetStatus()
			fmt.Fprintf(w, "data: {\"mid\":\"%s\",\"state\":\"%s\",\"error\":\"%s\",\"blocks_resolved\":%d,\"blocks_total\":%d,\"bytes_delivered\":%d,\"bytes_total\":%d,\"throughput\":%f,\"eta\":%f}\n\n",
				midStr, state, errMsg, blocksResolved, blocksTotal, bytesDelivered, bytesTotal, throughput, eta)
			flusher.Flush()
			if !ok || state == "completed" || state == "failed" {
				return
			}
		}
	}
}

func (m *MemGate) renderResolvePage(w http.ResponseWriter, r *http.Request, midStr string, ssePath string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)

	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Resolving Content | Membuss</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Archivo:wdth,wght@62..125,100..900&family=IBM+Plex+Mono:wght@300;400;500;600;700&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-color: #0c1416;
            --card-bg: #111d20;
            --accent-color: #e8a33d;
            --accent-glow: rgba(232, 163, 61, 0.12);
            --text-primary: #e9e2d2;
            --text-secondary: #8c887a;
            --success-color: #57b79e;
            --error-color: #e0654c;
            --border-color: rgba(233, 226, 210, 0.08);
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        body {
            font-family: 'Archivo', -apple-system, BlinkMacSystemFont, sans-serif;
            background-color: var(--bg-color);
            color: var(--text-primary);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            overflow: hidden;
            position: relative;
        }

        body::before {
            content: '';
            position: absolute;
            width: 400px;
            height: 400px;
            border-radius: 50%%;
            background: radial-gradient(circle, var(--accent-glow) 0%%, transparent 70%%);
            top: 20%%;
            left: 20%%;
            z-index: 0;
            filter: blur(50px);
            animation: float 20s ease-in-out infinite alternate;
        }

        body::after {
            content: '';
            position: absolute;
            width: 450px;
            height: 450px;
            border-radius: 50%%;
            background: radial-gradient(circle, rgba(87, 183, 158, 0.1) 0%%, transparent 70%%);
            bottom: 10%%;
            right: 15%%;
            z-index: 0;
            filter: blur(60px);
            animation: float 25s ease-in-out infinite alternate-reverse;
        }

        @keyframes float {
            0%% { transform: translate(0, 0) scale(1); }
            100%% { transform: translate(40px, 40px) scale(1.1); }
        }

        .container {
            width: 100%%;
            max-width: 580px;
            padding: 2rem;
            z-index: 10;
        }

        .card {
            background: var(--card-bg);
            border: 1px solid var(--border-color);
            border-radius: 12px;
            padding: 3rem 2.5rem;
            backdrop-filter: blur(20px);
            -webkit-backdrop-filter: blur(20px);
            box-shadow: 0 20px 50px rgba(0, 0, 0, 0.4);
            text-align: center;
            transition: border-color 0.5s ease;
        }

        .logo {
            display: inline-flex;
            align-items: center;
            gap: 0.5rem;
            margin-bottom: 2rem;
        }

        .logo-icon {
            width: 36px;
            height: 36px;
            background: var(--accent-color);
            border-radius: 6px;
            display: flex;
            align-items: center;
            justify-content: center;
            box-shadow: 0 0 12px var(--accent-glow);
            font-weight: 900;
            font-size: 1.25rem;
            color: var(--bg-color);
        }

        .logo-text {
            font-weight: 800;
            font-size: 1.5rem;
            letter-spacing: 0.05em;
            color: var(--text-primary);
        }

        h1 {
            font-size: 1.75rem;
            font-weight: 600;
            margin-bottom: 0.75rem;
            letter-spacing: -0.02em;
        }

        .mid-box {
            font-family: 'IBM Plex Mono', monospace;
            font-size: 0.85rem;
            background: rgba(0, 0, 0, 0.15);
            padding: 0.65rem 1rem;
            border-radius: 8px;
            border: 1px solid var(--border-color);
            color: var(--text-secondary);
            margin-bottom: 2.5rem;
            word-break: break-all;
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 0.5rem;
        }

        .status-badge {
            display: inline-flex;
            align-items: center;
            gap: 0.5rem;
            padding: 0.5rem 1.25rem;
            border-radius: 4px;
            font-size: 0.875rem;
            font-weight: 500;
            background: rgba(233, 226, 210, 0.03);
            color: var(--text-secondary);
            margin-bottom: 2rem;
            border: 1px solid var(--border-color);
        }

        .status-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%%;
            background-color: var(--text-secondary);
        }

        .status-discovering { color: var(--accent-color); border-color: rgba(232, 163, 61, 0.2); background: rgba(232, 163, 61, 0.05); }
        .status-discovering .status-dot { background-color: var(--accent-color); animation: pulse 1.5s infinite; }
        
        .status-fetching { color: var(--accent-color); border-color: rgba(232, 163, 61, 0.2); background: rgba(232, 163, 61, 0.05); }
        .status-fetching .status-dot { background-color: var(--accent-color); animation: pulse 1.5s infinite; }
        
        .status-completed { color: var(--success-color); border-color: rgba(87, 183, 158, 0.2); background: rgba(87, 183, 158, 0.05); }
        .status-completed .status-dot { background-color: var(--success-color); }
        
        .status-failed { color: var(--error-color); border-color: rgba(224, 101, 76, 0.2); background: rgba(224, 101, 76, 0.05); }
        .status-failed .status-dot { background-color: var(--error-color); }

        @keyframes pulse {
            0%% { transform: scale(0.8); opacity: 0.5; }
            50%% { transform: scale(1.2); opacity: 1; }
            100%% { transform: scale(0.8); opacity: 0.5; }
        }

        .progress-container {
            margin-bottom: 2.5rem;
            text-align: left;
        }

        .progress-track {
            height: 12px;
            background-color: rgba(0, 0, 0, 0.3);
            border-radius: 6px;
            overflow: hidden;
            border: 1px solid var(--border-color);
            margin-bottom: 0.75rem;
        }

        .progress-bar {
            height: 100%%;
            width: 0%%;
            background: linear-gradient(90deg, var(--accent-color), var(--success-color));
            border-radius: 6px;
            transition: width 0.3s cubic-bezier(0.4, 0, 0.2, 1);
        }

        .progress-labels {
            display: flex;
            justify-content: space-between;
            font-size: 0.875rem;
            color: var(--text-secondary);
        }

        .progress-percent {
            font-weight: 600;
            color: var(--text-primary);
        }

        .stats-grid {
            display: grid;
            grid-template-columns: repeat(2, 1fr);
            gap: 1.25rem;
            margin-bottom: 2.5rem;
        }

        .stat-card {
            background: rgba(0, 0, 0, 0.15);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 1.25rem;
            text-align: left;
        }

        .stat-label {
            font-size: 0.75rem;
            color: var(--text-secondary);
            text-transform: uppercase;
            letter-spacing: 0.05em;
            margin-bottom: 0.35rem;
        }

        .stat-value {
            font-size: 1.125rem;
            font-weight: 600;
            font-family: 'IBM Plex Mono', monospace;
        }

        .notice-box {
            font-size: 0.875rem;
            background: rgba(232, 163, 61, 0.05);
            border: 1px solid rgba(232, 163, 61, 0.2);
            color: var(--accent-color);
            padding: 1rem;
            border-radius: 8px;
            display: none;
            margin-bottom: 2rem;
            text-align: left;
            line-height: 1.5;
        }

        .error-message {
            font-size: 0.875rem;
            background: rgba(224, 101, 76, 0.05);
            border: 1px solid rgba(224, 101, 76, 0.2);
            color: var(--error-color);
            padding: 1.25rem;
            border-radius: 8px;
            display: none;
            margin-bottom: 2rem;
            text-align: left;
            word-break: break-word;
        }

        .footer {
            font-size: 0.75rem;
            color: var(--text-secondary);
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="card" id="main-card">
            <div class="logo">
                <div class="logo-icon">M</div>
                <div class="logo-text">MEMBUSS</div>
            </div>
            
            <h1 id="title-text">Retrieving Content</h1>
            <div class="mid-box">
                <span id="mid-display">%s</span>
            </div>

            <div class="status-badge status-discovering" id="status-badge">
                <div class="status-dot"></div>
                <span id="status-text">Discovering peers...</span>
            </div>

            <div class="notice-box" id="notice-box">
                <strong>Direct Download:</strong> This content is larger than 50MB. To preserve memory and client responsiveness, it will be downloaded directly as an attachment once fully assembled.
            </div>

            <div class="error-message" id="error-box">
                <strong>Error:</strong> <span id="error-text"></span>
            </div>

            <div class="progress-container" id="progress-container">
                <div class="progress-track">
                    <div class="progress-bar" id="progress-bar"></div>
                </div>
                <div class="progress-labels">
                    <span id="progress-detail">Initializing request...</span>
                    <span class="progress-percent" id="progress-percent">0%%</span>
                </div>
            </div>

            <div class="stats-grid">
                <div class="stat-card">
                    <div class="stat-label">Transfer Speed</div>
                    <div class="stat-value" id="speed-value">0 B/s</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Time Remaining</div>
                    <div class="stat-value" id="eta-value">Calculating...</div>
                </div>
            </div>

            <div class="footer">
                Mem-Gate • Content-Addressable CDN
            </div>
        </div>
    </div>

    <script>
        const mid = "%s";
        const sseUrl = "%s";
        let isLarge = false;

        function formatBytes(bytes, decimals = 2) {
            if (bytes === 0) return '0 Bytes';
            const k = 1024;
            const dm = decimals < 0 ? 0 : decimals;
            const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
        }

        function formatDuration(seconds) {
            if (isNaN(seconds) || seconds === Infinity || seconds <= 0) return 'Calculating...';
            if (seconds < 60) return Math.round(seconds) + 's';
            const mins = Math.floor(seconds / 60);
            const secs = Math.round(seconds %% 60);
            return mins + 'm ' + secs + 's';
        }

        function initSSE() {
            const eventSource = new EventSource(sseUrl);

            eventSource.onmessage = function(event) {
                const data = JSON.parse(event.data);
                console.log("Status update:", data);

                const badge = document.getElementById("status-badge");
                const badgeText = document.getElementById("status-text");
                const bar = document.getElementById("progress-bar");
                const percent = document.getElementById("progress-percent");
                const detail = document.getElementById("progress-detail");
                const speed = document.getElementById("speed-value");
                const eta = document.getElementById("eta-value");
                const notice = document.getElementById("notice-box");
                const errorBox = document.getElementById("error-box");
                const errorText = document.getElementById("error-text");

                if (data.state === "discovering") {
                    badge.className = "status-badge status-discovering";
                    badgeText.innerText = "Discovering providers...";
                    detail.innerText = "Searching Mem-DHT network...";
                    bar.style.width = "0%%";
                    percent.innerText = "0%%";
                    speed.innerText = "0 B/s";
                    eta.innerText = "Calculating...";
                } else if (data.state === "fetching") {
                    badge.className = "status-badge status-fetching";
                    badgeText.innerText = "Fetching blocks...";
                    
                    let pct = 0;
                    if (data.bytes_total > 0) {
                        pct = Math.round((data.bytes_delivered / data.bytes_total) * 100);
                        detail.innerText = formatBytes(data.bytes_delivered) + " / " + formatBytes(data.bytes_total) + " (" + data.blocks_resolved + " / " + data.blocks_total + " blks)";
                        
                        if (data.bytes_total > 50 * 1024 * 1024 && !isLarge) {
                            isLarge = true;
                            notice.style.display = "block";
                        }
                    } else if (data.blocks_total > 0) {
                        pct = Math.round((data.blocks_resolved / data.blocks_total) * 100);
                        detail.innerText = data.blocks_resolved + " / " + data.blocks_total + " blocks";
                    } else {
                        detail.innerText = formatBytes(data.bytes_delivered) + " fetched";
                    }

                    bar.style.width = pct + "%%";
                    percent.innerText = pct + "%%";

                    speed.innerText = formatBytes(data.throughput) + "/s";
                    eta.innerText = formatDuration(data.eta);
                } else if (data.state === "completed") {
                    badge.className = "status-badge status-completed";
                    badgeText.innerText = "Assembled successfully";
                    bar.style.width = "100%%";
                    percent.innerText = "100%%";
                    detail.innerText = "Complete! Redirecting...";
                    speed.innerText = "Done";
                    eta.innerText = "Completed";

                    eventSource.close();
                    
                    setTimeout(() => {
                        const destUrl = window.location.pathname + window.location.search;
                        window.location.href = destUrl;
                    }, 1200);
                } else if (data.state === "failed") {
                    badge.className = "status-badge status-failed";
                    badgeText.innerText = "Not found on network";
                    detail.innerText = "Failed to resolve content.";
                    eventSource.close();

                    errorBox.style.display = "block";
                    errorText.innerText = data.error || "No providers found on the Kademlia DHT or connections timed out.";
                }
            };

            eventSource.onerror = function(err) {
                console.error("SSE connection error:", err);
            };
        }

        window.onload = initSSE;
    </script>
</body>
</html>`, midStr, midStr, ssePath)
}

