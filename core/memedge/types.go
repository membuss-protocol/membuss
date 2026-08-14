package memedge

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// RuntimeType identifies the execution runtime.
type RuntimeType string

const (
	RuntimeJS   RuntimeType = "js"
	RuntimeWasm RuntimeType = "wasm"
	RuntimeAuto RuntimeType = "auto"
)

// ExecutionTier indicates which tier executed the function.
type ExecutionTier int

const (
	TierUnknown   ExecutionTier = 0
	TierPublisher ExecutionTier = 1 // Executed by the original content publisher node
	TierPeer      ExecutionTier = 2 // Executed by a delegated random network peer
	TierGateway   ExecutionTier = 3 // Executed locally by the public gateway node
)

func (t ExecutionTier) String() string {
	switch t {
	case TierPublisher:
		return "publisher"
	case TierPeer:
		return "peer"
	case TierGateway:
		return "gateway"
	default:
		return "unknown"
	}
}

// Limits defines the security and resource bounds for an execution.
type Limits struct {
	// MaxExecutionTime is the hard CPU/wall-clock execution timeout.
	MaxExecutionTime time.Duration `json:"max_execution_time"`
	// MaxMemoryBytes is the memory limit for the execution sandbox.
	MaxMemoryBytes uint64 `json:"max_memory_bytes"`
	// MaxBodySizeBytes is the maximum allowed request/response body size.
	MaxBodySizeBytes int64 `json:"max_body_size_bytes"`
}

// DefaultLimits returns production-safe default limits.
func DefaultLimits() Limits {
	return Limits{
		MaxExecutionTime: 500 * time.Millisecond,
		MaxMemoryBytes:   32 << 20, // 32 MiB
		MaxBodySizeBytes: 10 << 20, // 10 MiB
	}
}

// Request represents an incoming HTTP request passed to the edge function.
type Request struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Query    map[string]string `json:"query"`
	Body     string            `json:"body"`
	ClientIP string            `json:"client_ip"`
	Params   map[string]string `json:"params,omitempty"`
}

// NewRequestFromHTTP creates a Request from a standard Go http.Request.
func NewRequestFromHTTP(r *http.Request, body []byte) *Request {
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	query := make(map[string]string)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	clientIP := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		clientIP = forwarded
	} else if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		clientIP = realIP
	}

	return &Request{
		Method:   r.Method,
		URL:      r.URL.String(),
		Path:     r.URL.Path,
		Headers:  headers,
		Query:    query,
		Body:     string(body),
		ClientIP: clientIP,
		Params:   make(map[string]string),
	}
}

// Response represents the output of an edge function execution.
type Response struct {
	Status      int               `json:"status"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	DurationMs  float64           `json:"duration_ms"`
	Runtime     RuntimeType       `json:"runtime"`
	Tier        ExecutionTier     `json:"tier"`
	Error       string            `json:"error,omitempty"`
	Logs        []string          `json:"logs,omitempty"`
}

// Config configures the MemEdge Engine.
type Config struct {
	Enabled            bool          `json:"enabled" yaml:"enabled"`
	Mode               string        `json:"mode" yaml:"mode"` // "community", "publisher_only", "off"
	DefaultLimits      Limits        `json:"default_limits" yaml:"default_limits"`
	MaxConcurrentTasks int           `json:"max_concurrent_tasks" yaml:"max_concurrent_tasks"`
	CacheCapacity      int           `json:"cache_capacity" yaml:"cache_capacity"`
}

// DefaultConfig returns sensible defaults for edge compute.
func DefaultConfig() Config {
	return Config{
		Enabled:            true,
		Mode:               "community",
		DefaultLimits:      DefaultLimits(),
		MaxConcurrentTasks: 16,
		CacheCapacity:      256,
	}
}

// Engine defines the interface for running serverless edge functions.
type Engine interface {
	// Execute runs code (either JavaScript or WebAssembly) with the provided request.
	Execute(ctx context.Context, code []byte, runtimeType RuntimeType, req *Request, limits *Limits) (*Response, error)
	// Close cleans up runtimes and cached modules.
	Close() error
}

// ErrExecutionTimeout is returned when function execution exceeds the time limit.
type ErrExecutionTimeout struct {
	Limit time.Duration
}

func (e ErrExecutionTimeout) Error() string {
	return fmt.Sprintf("edge execution exceeded timeout limit of %v", e.Limit)
}
