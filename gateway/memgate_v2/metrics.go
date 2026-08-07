package memgate_v2

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// gatewayMetrics encapsulates Prometheus collectors for Mem-Gate v2 edge metrics.
type gatewayMetrics struct {
	registry *prometheus.Registry

	requestsTotal    *prometheus.CounterVec
	cacheHitsTotal   prometheus.Counter
	cacheMissesTotal prometheus.Counter
	requestDuration  *prometheus.HistogramVec
	activeSSEStreams prometheus.Gauge
	rateLimitDrops   prometheus.Counter
}

func newGatewayMetrics() *gatewayMetrics {
	reg := prometheus.NewRegistry()
	m := &gatewayMetrics{
		registry: reg,
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "membuss_gateway_requests_total",
				Help: "Total HTTP requests processed by Mem-Gate, partitioned by status code and method.",
			},
			[]string{"code", "method"},
		),
		cacheHitsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "membuss_gateway_cache_hits_total",
			Help: "Total number of in-memory LRU cache hits.",
		}),
		cacheMissesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "membuss_gateway_cache_misses_total",
			Help: "Total number of in-memory LRU cache misses.",
		}),
		requestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "membuss_gateway_request_duration_seconds",
				Help:    "HTTP request latency histogram in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method"},
		),
		activeSSEStreams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "membuss_gateway_active_sse_streams",
			Help: "Number of currently active SSE status streams.",
		}),
		rateLimitDrops: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "membuss_gateway_rate_limit_drops_total",
			Help: "Total number of requests dropped due to rate limiting.",
		}),
	}

	reg.MustRegister(m.requestsTotal)
	reg.MustRegister(m.cacheHitsTotal)
	reg.MustRegister(m.cacheMissesTotal)
	reg.MustRegister(m.requestDuration)
	reg.MustRegister(m.activeSSEStreams)
	reg.MustRegister(m.rateLimitDrops)
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	return m
}

func (m *gatewayMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// statusResponseWriter wraps http.ResponseWriter to capture the HTTP status code.
type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		defer func() {
			_ = recover()
		}()
		f.Flush()
	}
}

func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijacker")
}

// middleware measures request latency and records HTTP status code distribution.
func (m *gatewayMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		duration := time.Since(start).Seconds()
		m.requestDuration.WithLabelValues(r.Method).Observe(duration)
		m.requestsTotal.WithLabelValues(strconv.Itoa(sw.status), r.Method).Inc()
	})
}

// authMiddleware enforces access security on the metrics endpoint.
// If token is configured, checks Authorization: Bearer <token> or ?token=<token>.
// If token is empty, restricts access to localhost/internal RemoteAddr connections only.
func (m *gatewayMetrics) authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			authHeader := r.Header.Get("Authorization")
			reqToken := strings.TrimPrefix(authHeader, "Bearer ")
			if reqToken == "" {
				reqToken = r.URL.Query().Get("token")
			}
			if reqToken != token {
				http.Error(w, "unauthorized metrics access", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if isLocalConnection(r) {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "metrics endpoint not accessible on public network", http.StatusForbidden)
	})
}

func isLocalConnection(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}
