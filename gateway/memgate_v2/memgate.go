// Package memgate_v2 provides the public HTTP gateway and CDN layer
// for Membuss. It supports path-based gateway routes, subdomain-based
// resolution for SPAs, custom domain mapping via MemNS, and caching.
package memgate_v2

import (
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
	"sync"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/nnlgsakib/membuss/core/memns"
	"github.com/nnlgsakib/membuss/core/mid"
	membusspb "github.com/nnlgsakib/membuss/proto"
	"google.golang.org/protobuf/proto"
)

// Backend is the contract Mem-Gate depends on. The daemon
// supplies a real implementation; tests inject a memBackend.
type Backend interface {
	// Resolve returns a streaming reader of the content
	// addressed by m. The caller is responsible for closing
	// the reader. The size is the total content size in
	// bytes; it is used for Content-Length / Range math.
	Resolve(ctx context.Context, m mid.MID) (io.ReadCloser, ContentInfo, error)
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
}

// MemGate is the public HTTP gateway.
type MemGate struct {
	cfg            Config
	router         chi.Router
	lru            *lru
	ipLimiter      *ipLimiter
	refererTracker *RefererTracker
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

	mg := &MemGate{
		cfg:            cfg,
		lru:            newLRU(cfg.MaxCacheBytes),
		refererTracker: NewRefererTracker(),
	}
	mg.ipLimiter = newIPLimiter(cfg.RateLimitPerMin, 10*time.Minute)
	mg.router = mg.buildRouter()
	return mg, nil
}

// Router returns the chi router. Exposed so tests can drive
// the gateway via httptest.
func (m *MemGate) Router() http.Handler { return m.router }

func (m *MemGate) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		_, _, isSubdomain := m.resolveSubdomain(r)
		systemPath := isSystemPath(path)

		// 1. Subdomain routing (skip for system paths)
		if !systemPath && isSubdomain {
			if midStr, innerPath, ok := m.resolveSubdomain(r); ok {
				m.refererTracker.RecordActiveMID(midStr)
				setMIDCookie(w, midStr)
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
					m.refererTracker.RecordActiveMID(parts[0])
					setMIDCookie(w, parts[0])
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
						m.refererTracker.RecordActiveMID(midStr)
						setMIDCookie(w, midStr)
					}
				}
			}
		}

		// 2. Referer-based path resolution for absolute assets on path-based gateways
		if !systemPath {
			if midStr, innerPath, ok := m.refererTracker.Resolve(r, m.cfg.Backend, m.cfg.MemNSResolver); ok {
				m.serveMemFSPath(w, r, midStr, innerPath)
				return
			}
		}

		m.router.ServeHTTP(w, r)
	})
}

// setMIDCookie sets a cookie with the active MID to assist in path-based asset resolution.
func setMIDCookie(w http.ResponseWriter, midStr string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "membuss_gateway_mid",
		Value:    midStr,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   3600 * 24, // 1 day
	})
}

// isSystemPath evaluates if a path is a dynamic node control route.
func isSystemPath(path string) bool {
	if strings.HasPrefix(path, "/mem/") ||
		strings.HasPrefix(path, "/healthz") ||
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

	// Subdomain routing only runs on .localhost (e.g. *.localhost or *.mem.localhost)
	if !strings.HasSuffix(host, ".localhost") {
		return "", "", false
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", "", false
	}

	firstLabel := labels[0]
	// 1. Check if first label is a valid MID
	if _, err := mid.Parse(firstLabel); err == nil {
		innerPath := strings.TrimPrefix(r.URL.Path, "/")
		return firstLabel, innerPath, true
	}

	// 2. Check if first label is a MemNS name
	if firstLabel != "localhost" && firstLabel != "127" && firstLabel != "www" {
		if m.cfg.MemNSResolver != nil {
			resolved, err := m.cfg.MemNSResolver.Resolve(r.Context(), firstLabel)
			if err == nil && resolved != "" {
				midStr := resolved
				if strings.HasPrefix(midStr, "/mem/") {
					midStr = midStr[5:]
				}
				innerPath := strings.TrimPrefix(r.URL.Path, "/")
				return midStr, innerPath, true
			}
		}
	}

	return "", "", false
}

func (m *MemGate) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
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

	// Explorer is a local admin UI — block non-localhost access
	r.Use(m.localOnlyMiddleware)

	// Phase 18: custom domain serving middleware
	r.Use(m.customDomainMiddleware)

	r.Get("/healthz", m.handleHealth)
	r.Get("/mem/{mid}", m.handleGet)
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
	q := r.URL.Query()

	// Phase 19: resolve the content info FIRST so we can
	// set the right Content-Type and Content-Disposition
	// headers before the body handler writes a single byte.
	// The CDN-style behavior is: explicit ?download=1
	// forces attachment, ?view=1 (or absence of either)
	// defaults to inline so the browser renders images,
	// text, video, etc. directly.
	preInfo, preErr := m.cfg.Backend.Stat(r.Context(), root)
	if preErr == nil {
		if (preInfo.MimeType == "inode/directory" || preInfo.ContentType == "inode/directory") && q.Get("format") == "" {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		if preInfo.MimeType != "" && w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", preInfo.MimeType)
		}
		// Default filename for Content-Disposition. The
		// uploader-supplied name wins; falls back to
		// <mid>.bin. The disposition is set below.
		name := preInfo.Name
		if name == "" {
			name = midStr + ".bin"
		}
		disp := "inline"
		if q.Get("download") == "1" {
			disp = "attachment"
			if fn := q.Get("filename"); fn != "" {
				name = fn
			}
		}
		// Once-only: do not overwrite a header the
		// caller set explicitly (e.g. tests).
		if w.Header().Get("Content-Disposition") == "" {
			w.Header().Set("Content-Disposition",
				mime.FormatMediaType(disp, map[string]string{"filename": sanitizeFilename(name)}))
		}
		// X-Membuss-* mirrors for tooling that cannot
		// read Content-Disposition reliably.
		w.Header().Set("X-Membuss-Name", preInfo.Name)
		w.Header().Set("X-Membuss-MimeType", preInfo.MimeType)
	}

	switch q.Get("format") {
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
	fmt.Fprintf(w, `<!doctype html><html><head><title>%s</title>`+
		`<style>body{font-family:system-ui;max-width:60rem;margin:2rem auto;padding:0 1rem}`+
		`table{border-collapse:collapse;width:100%%}td,th{padding:.5rem;text-align:left;border-bottom:1px solid #eee}`+
		`a{color:#06c;text-decoration:none}a:hover{text-decoration:underline}`+
		`.muted{color:#888;font-size:.9em}</style></head><body>`+
		`<h1>%s</h1>`+
		`<table><tr><th>Name</th><th>Type</th><th>Size</th><th>MID</th></tr>`,
		html.EscapeString(title), html.EscapeString(title))
	for _, e := range entries {
		href := url.PathEscape(e.Name)
		if e.Type == "dir" {
			href += "/"
		}
		fmt.Fprintf(w, `<tr><td><a href="%s">%s</a></td>`+
			`<td>%s</td><td>%d</td>`+
			`<td class="muted"><a href="/mem/%s">%s…</a></td></tr>`,
			href, html.EscapeString(e.Name), html.EscapeString(e.Type), e.Size, url.PathEscape(e.MID), html.EscapeString(shortMID(e.MID)))
	}
	fmt.Fprintf(w, `</table></body></html>`)
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
// copy with optional range slicing.
func (m *MemGate) handleResolved(w http.ResponseWriter, r *http.Request, root mid.MID, midStr string) {
	// RFC 7234 conditional validation
	if checkETag(w, r, midStr) {
		return
	}

	// Cache lookup. LRU is keyed on the public MID string
	// so that a malicious or buggy caller cannot trigger
	// cache growth by spamming raw-block requests.
	cacheKey := "resolved:" + midStr
	if data, ok := m.lru.get(cacheKey); ok {
		m.writeBytes(w, r, midStr, data, DetectContentType(midStr, data, ""))
		return
	}

	rc, info, err := m.cfg.Backend.Resolve(r.Context(), root)
	if err != nil {
		http.Error(w, "resolve: "+err.Error(), http.StatusNotFound)
		return
	}
	defer rc.Close()

	// If we know the size and the reader is just a chunk
	// stream, we can stream the response without buffering
	// the entire content. Cache eligibility: the content
	// must fit under MaxCacheBytes.
	stream := true
	var buf []byte
	if info.Size > 0 && info.Size <= m.cfg.MaxCacheBytes {
		// Read fully and cache. The whole content fits in
		// the configured cache envelope.
		buf = make([]byte, info.Size)
		if _, err := io.ReadFull(rc, buf); err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			http.Error(w, "read: "+err.Error(), http.StatusBadGateway)
			return
		}
		m.lru.put(cacheKey, buf)
		stream = false
	} else {
		// Streamed path. Don't cache.
		w.Header().Set("Content-Type", chooseContentType(info.ContentType, "application/octet-stream"))
		w.Header().Set("X-Membuss-MID", info.MID)
		w.Header().Set("X-Membuss-Size", strconv.FormatUint(info.Size, 10))
		w.Header().Set("X-Membuss-Blocks", strconv.FormatUint(info.Blocks, 10))
		w.Header().Set("X-Membuss-Sealed", strconv.FormatBool(info.Sealed))
		w.Header().Set("ETag", `"`+info.MID+`"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")

		if m.handleStreamRange(w, r, root, int64(info.Size)) {
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
		return
	}

	_ = stream
	m.writeBytes(w, r, midStr, buf, chooseContentType(info.ContentType, DetectContentType(midStr, buf, "")))
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

// --- LRU ---

// lru is a small bounded-byte LRU. The data structure is
// hand-rolled so the package does not pull in a heavy
// dependency. For a CDN edge, a simple list + map is more
// than adequate.
type lru struct {
	mu       sync.Mutex
	maxBytes uint64
	curBytes uint64
	// items is ordered most-recent-first.
	items map[string]*listEntry
	head  *listEntry
	tail  *listEntry
}

type listEntry struct {
	key  string
	data []byte
	prev *listEntry
	next *listEntry
}

func newLRU(maxBytes uint64) *lru {
	return &lru{maxBytes: maxBytes, items: make(map[string]*listEntry)}
}

func (l *lru) get(key string) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.items[key]
	if !ok {
		return nil, false
	}
	l.moveToFront(e)
	return e.data, true
}

func (l *lru) put(key string, data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.items[key]; ok {
		l.curBytes -= uint64(len(e.data))
		e.data = data
		l.curBytes += uint64(len(data))
		l.moveToFront(e)
	} else {
		e := &listEntry{key: key, data: data}
		l.items[key] = e
		l.curBytes += uint64(len(data))
		l.pushFront(e)
	}
	for l.curBytes > l.maxBytes && l.tail != nil {
		old := l.tail
		l.remove(old)
		delete(l.items, old.key)
		l.curBytes -= uint64(len(old.data))
	}
}

func (l *lru) moveToFront(e *listEntry) {
	if l.head == e {
		return
	}
	l.remove(e)
	l.pushFront(e)
}

func (l *lru) pushFront(e *listEntry) {
	e.prev = nil
	e.next = l.head
	if l.head != nil {
		l.head.prev = e
	}
	l.head = e
	if l.tail == nil {
		l.tail = e
	}
}

func (l *lru) remove(e *listEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		l.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

// len returns the current number of entries.
func (l *lru) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

// Bytes returns the current cache size in bytes.
func (l *lru) bytes() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curBytes
}

// MaxBytes returns the configured cache cap.
func (l *lru) max() uint64 { return l.maxBytes }

// MarshalJSON renders the cache as a small JSON object.
func (l *lru) MarshalJSON() ([]byte, error) {
	type view struct {
		Entries int    `json:"entries"`
		Bytes   uint64 `json:"bytes"`
		Max     uint64 `json:"max_bytes"`
	}
	v := view{Entries: l.len(), Bytes: l.bytes(), Max: l.max()}
	return json.Marshal(v)
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
			strings.HasPrefix(path, "/explorer") ||
			strings.HasPrefix(path, "/memns/") ||
			strings.HasPrefix(path, "/memlink/") {
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
			http.Error(w, "Domain not found or resolve failed: "+err.Error(), http.StatusNotFound)
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

	midStr := resolved
	if strings.HasPrefix(midStr, "/mem/") {
		midStr = midStr[5:]
	}

	if strings.HasPrefix(resolved, "https://") || strings.HasPrefix(resolved, "/ipfs/") {
		http.Redirect(w, r, resolved, http.StatusTemporaryRedirect)
		return
	}

	m.serveMemFSPath(w, r, midStr, innerPath)
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
				w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": sanitizeFilename(filename)}))
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

	if size > 0 && size <= m.cfg.MaxCacheBytes {
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
			w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": sanitizeFilename(filename)}))
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
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": sanitizeFilename(filename)}))
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

type dagBlock struct {
	mid    mid.MID
	size   int64
	offset int64
}

func buildBlockList(ctx context.Context, backend Backend, root mid.MID) ([]dagBlock, int64, error) {
	raw, err := backend.RawBlock(ctx, root)
	if err != nil {
		return nil, 0, err
	}

	var blocks []dagBlock
	var offset int64

	if root.Codec() == mid.CodecMemFS {
		var node membusspb.MemFSNode
		if err := proto.Unmarshal(raw, &node); err != nil {
			return nil, 0, err
		}
		if node.Type != membusspb.MemFSType_FILE {
			return nil, 0, fmt.Errorf("memfs node is not a file")
		}

		var walkMemFS func(n *membusspb.MemFSNode) error
		walkMemFS = func(n *membusspb.MemFSNode) error {
			for _, b := range n.Blocks {
				if b == nil || len(b.Mid) == 0 {
					continue
				}
				var codec uint64 = mid.CodecMemFS
				if b.Size > 0 {
					codec = mid.CodecRaw
				}
				childMID, err := mid.FromMultihash(codec, b.Mid)
				if err != nil {
					return err
				}

				if b.Size > 0 {
					blocks = append(blocks, dagBlock{
						mid:    childMID,
						size:   int64(b.Size),
						offset: offset,
					})
					offset += int64(b.Size)
				} else {
					childRaw, err := backend.RawBlock(ctx, childMID)
					if err != nil {
						return err
					}
					var childNode membusspb.MemFSNode
					if err := proto.Unmarshal(childRaw, &childNode); err != nil {
						return err
					}
					if err := walkMemFS(&childNode); err != nil {
						return err
					}
				}
			}
			return nil
		}

		if err := walkMemFS(&node); err != nil {
			return nil, 0, err
		}
		return blocks, offset, nil
	} else {
		var walkRawDAG func(curr mid.MID, rawBytes []byte) error
		walkRawDAG = func(curr mid.MID, rawBytes []byte) error {
			var node membusspb.DAGNode
			if err := proto.Unmarshal(rawBytes, &node); err == nil && len(node.Links) > 0 {
				for _, linkStr := range node.Links {
					child, err := mid.Parse(linkStr)
					if err != nil {
						return err
					}
					childRaw, err := backend.RawBlock(ctx, child)
					if err != nil {
						return err
					}
					if err := walkRawDAG(child, childRaw); err != nil {
						return err
					}
				}
				return nil
			}

			size := int64(len(rawBytes))
			blocks = append(blocks, dagBlock{
				mid:    curr,
				size:   size,
				offset: offset,
			})
			offset += size
			return nil
		}

		if err := walkRawDAG(root, raw); err != nil {
			return nil, 0, err
		}
		return blocks, offset, nil
	}
}

type dagReader struct {
	ctx          context.Context
	backend      Backend
	blocks       []dagBlock
	totalSize    int64
	pos          int64
	curBlockIdx  int
	curBlockBuf  []byte
}

func newDagReader(ctx context.Context, backend Backend, blocks []dagBlock, totalSize int64) *dagReader {
	return &dagReader{
		ctx:         ctx,
		backend:     backend,
		blocks:      blocks,
		totalSize:   totalSize,
		curBlockIdx: -1,
	}
}

func (r *dagReader) Read(p []byte) (int, error) {
	if r.pos >= r.totalSize {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	idx := r.findBlockIndex(r.pos)
	if idx < 0 || idx >= len(r.blocks) {
		return 0, io.EOF
	}

	if r.curBlockIdx != idx || r.curBlockBuf == nil {
		block := r.blocks[idx]
		data, err := r.backend.RawBlock(r.ctx, block.mid)
		if err != nil {
			return 0, fmt.Errorf("read block %s: %w", block.mid.String(), err)
		}
		r.curBlockIdx = idx
		r.curBlockBuf = data
	}

	block := r.blocks[idx]
	offsetInBlock := r.pos - block.offset
	if offsetInBlock >= int64(len(r.curBlockBuf)) {
		return 0, io.EOF
	}
	n := copy(p, r.curBlockBuf[offsetInBlock:])
	r.pos += int64(n)
	return n, nil
}

func (r *dagReader) Seek(offset int64, whence int) (int64, error) {
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = r.pos + offset
	case io.SeekEnd:
		target = r.totalSize + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}
	if target < 0 {
		return 0, fmt.Errorf("negative seek target: %d", target)
	}
	r.pos = target
	return r.pos, nil
}

func (r *dagReader) Close() error {
	r.curBlockBuf = nil
	return nil
}

func (r *dagReader) findBlockIndex(pos int64) int {
	for i, b := range r.blocks {
		if pos >= b.offset && pos < b.offset+b.size {
			return i
		}
	}
	return -1
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

	blocks, _, err := buildBlockList(r.Context(), m.cfg.Backend, fileMID)
	if err != nil {
		http.Error(w, "build block list: "+err.Error(), http.StatusInternalServerError)
		return true
	}

	reader := newDagReader(r.Context(), m.cfg.Backend, blocks, totalSize)
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

