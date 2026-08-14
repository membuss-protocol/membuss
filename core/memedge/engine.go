package memedge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// WasmMagicHeader is the standard 4-byte prefix for WebAssembly binaries (\x00asm).
var WasmMagicHeader = []byte{0x00, 0x61, 0x73, 0x6d}

// DefaultEngine implements the Engine interface.
type DefaultEngine struct {
	cfg        Config
	cache      *CodeCache
	jsRunner   *JSRunner
	wasmRunner *WasmRunner
	sem        chan struct{}

	// Metrics
	totalExecutions atomic.Uint64
	totalErrors     atomic.Uint64
	totalDurationUs atomic.Uint64

	mu sync.RWMutex
}

// NewEngine initializes a new MemEdge engine.
func NewEngine(ctx context.Context, cfg Config) (*DefaultEngine, error) {
	if cfg.MaxConcurrentTasks <= 0 {
		cfg.MaxConcurrentTasks = 16
	}
	if cfg.CacheCapacity <= 0 {
		cfg.CacheCapacity = 256
	}

	cache := NewCodeCache(cfg.CacheCapacity)
	js := NewJSRunner(cache)
	wasm, err := NewWasmRunner(ctx, cache)
	if err != nil {
		return nil, fmt.Errorf("init wasm runner: %w", err)
	}

	return &DefaultEngine{
		cfg:        cfg,
		cache:      cache,
		jsRunner:   js,
		wasmRunner: wasm,
		sem:        make(chan struct{}, cfg.MaxConcurrentTasks),
	}, nil
}

// DetectRuntime inspects the filename and code content to determine if it is Wasm or JS.
func DetectRuntime(path string, code []byte) RuntimeType {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".wasm" {
		return RuntimeWasm
	}
	if ext == ".js" || ext == ".mjs" || ext == ".ts" {
		return RuntimeJS
	}

	// Sniff magic bytes
	if len(code) >= 4 && bytes.Equal(code[:4], WasmMagicHeader) {
		return RuntimeWasm
	}

	// Default to JavaScript
	return RuntimeJS
}

// Execute dispatches the function to the appropriate runtime engine with concurrency and resource limits.
func (e *DefaultEngine) Execute(ctx context.Context, code []byte, runtimeType RuntimeType, req *Request, limits *Limits) (*Response, error) {
	if !e.cfg.Enabled || e.cfg.Mode == "off" {
		return nil, errors.New("edge compute is disabled on this node")
	}

	if len(code) == 0 {
		return nil, errors.New("cannot execute empty code payload")
	}

	activeLimits := e.cfg.DefaultLimits
	if limits != nil {
		if limits.MaxExecutionTime > 0 {
			activeLimits.MaxExecutionTime = limits.MaxExecutionTime
		}
		if limits.MaxMemoryBytes > 0 {
			activeLimits.MaxMemoryBytes = limits.MaxMemoryBytes
		}
		if limits.MaxBodySizeBytes > 0 {
			activeLimits.MaxBodySizeBytes = limits.MaxBodySizeBytes
		}
	}

	if req == nil {
		req = &Request{
			Method:  "GET",
			Headers: make(map[string]string),
			Query:   make(map[string]string),
		}
	}

	// Auto-detect runtime if not explicitly set
	if runtimeType == "" || runtimeType == RuntimeAuto {
		runtimeType = DetectRuntime(req.Path, code)
	}

	// Acquire concurrency slot
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(activeLimits.MaxExecutionTime):
		return &Response{
			Status:  503,
			Runtime: runtimeType,
			Error:   "Server busy: max concurrent edge tasks reached",
		}, errors.New("max concurrency limit reached")
	}

	start := time.Now()
	var resp *Response
	var execErr error

	switch runtimeType {
	case RuntimeWasm:
		resp, execErr = e.wasmRunner.Execute(ctx, code, req, activeLimits)
	case RuntimeJS:
		resp, execErr = e.jsRunner.Execute(ctx, code, req, activeLimits)
	default:
		resp, execErr = e.jsRunner.Execute(ctx, code, req, activeLimits)
	}

	durUs := uint64(time.Since(start).Microseconds())
	e.totalExecutions.Add(1)
	e.totalDurationUs.Add(durUs)

	if execErr != nil {
		e.totalErrors.Add(1)
	}

	return resp, execErr
}

// Stats returns the real-time execution statistics of the engine.
type Stats struct {
	Enabled          bool    `json:"enabled"`
	Mode             string  `json:"mode"`
	TotalExecutions  uint64  `json:"total_executions"`
	TotalErrors      uint64  `json:"total_errors"`
	AvgDurationMs    float64 `json:"avg_duration_ms"`
	ActiveGoroutines int     `json:"active_tasks"`
	MaxConcurrency   int     `json:"max_concurrency"`
}

// Stats returns the current runtime performance metrics.
func (e *DefaultEngine) Stats() Stats {
	total := e.totalExecutions.Load()
	totalUs := e.totalDurationUs.Load()
	var avgMs float64
	if total > 0 {
		avgMs = float64(totalUs) / float64(total) / 1000.0
	}

	return Stats{
		Enabled:          e.cfg.Enabled,
		Mode:             e.cfg.Mode,
		TotalExecutions:  total,
		TotalErrors:      e.totalErrors.Load(),
		AvgDurationMs:    avgMs,
		ActiveGoroutines: len(e.sem),
		MaxConcurrency:   e.cfg.MaxConcurrentTasks,
	}
}

// Close gracefully terminates all runtimes and clears caches.
func (e *DefaultEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.cache.Clear()
	if e.wasmRunner != nil {
		_ = e.wasmRunner.Close(context.Background())
	}
	return nil
}
