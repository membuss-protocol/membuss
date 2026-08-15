package memedge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// WasmRunner executes WebAssembly / WASI serverless binaries (Go, Rust, TinyGo, C).
type WasmRunner struct {
	mu      sync.Mutex
	cache   *CodeCache
	runtime wazero.Runtime
}

// NewWasmRunner creates a new WasmRunner instance.
func NewWasmRunner(ctx context.Context, cache *CodeCache) (*WasmRunner, error) {
	if cache == nil {
		cache = NewCodeCache(128)
	}

	// Create Wazero JIT runtime with 32 MiB default memory limit (512 pages of 64 KiB)
	runtimeCfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(512)
	r := wazero.NewRuntimeWithConfig(ctx, runtimeCfg)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	return &WasmRunner{
		cache:   cache,
		runtime: r,
	}, nil
}

// Execute runs the WebAssembly binary against the provided Request and Limits.
func (w *WasmRunner) Execute(ctx context.Context, code []byte, req *Request, limits Limits) (resp *Response, err error) {
	start := time.Now()

	// Panic safety watchdog
	defer func() {
		if rec := recover(); rec != nil {
			resp = &Response{
				Status:     500,
				DurationMs: float64(time.Since(start).Microseconds()) / 1000.0,
				Runtime:    RuntimeWasm,
				Error:      fmt.Sprintf("WebAssembly runtime panic: %v", rec),
			}
			err = fmt.Errorf("wasm panic: %v", rec)
		}
	}()

	execCtx, cancel := context.WithTimeout(ctx, limits.MaxExecutionTime)
	defer cancel()

	cacheKey := "wasm:" + KeyForCode(code)

	w.mu.Lock()
	var compiled wazero.CompiledModule
	if cached, found := w.cache.Get(cacheKey); found {
		if m, ok := cached.(wazero.CompiledModule); ok {
			compiled = m
		}
	}

	if compiled == nil {
		var err error
		compiled, err = w.runtime.CompileModule(ctx, code)
		if err != nil {
			w.mu.Unlock()
			return &Response{
				Status:     500,
				DurationMs: float64(time.Since(start).Microseconds()) / 1000.0,
				Runtime:    RuntimeWasm,
				Error:      "Wasm compilation error: " + err.Error(),
			}, fmt.Errorf("compile wasm: %w", err)
		}
		w.cache.Set(cacheKey, compiled)
	}
	w.mu.Unlock()

	// Prepare stdin payload with JSON request
	reqJSON, _ := json.Marshal(req)
	stdin := bytes.NewReader(reqJSON)
	var stdout, stderr bytes.Buffer

	// Build environment variables for standard CGI/WASI apps
	modConfig := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithEnv("MEMEDGE", "1").
		WithEnv("REQUEST_METHOD", req.Method).
		WithEnv("REQUEST_PATH", req.Path).
		WithEnv("REQUEST_URI", req.URL).
		WithEnv("REMOTE_ADDR", req.ClientIP)

	for k, v := range req.Headers {
		envKey := "HTTP_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
		modConfig = modConfig.WithEnv(envKey, v)
	}

	mod, err := w.runtime.InstantiateModule(execCtx, compiled, modConfig)
	if err != nil {
		var sysErr *sys.ExitError
		// In WASI, exit code 0 is normal exit
		if errors.As(err, &sysErr) && sysErr.ExitCode() == 0 {
			err = nil
		}
	}

	if mod != nil {
		_ = mod.Close(execCtx)
	}

	duration := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return &Response{
				Status:     504,
				DurationMs: duration,
				Runtime:    RuntimeWasm,
				Error:      fmt.Sprintf("Wasm execution timed out after %v", limits.MaxExecutionTime),
			}, ErrExecutionTimeout{Limit: limits.MaxExecutionTime}
		}

		return &Response{
			Status:     500,
			DurationMs: duration,
			Runtime:    RuntimeWasm,
			Error:      fmt.Sprintf("Wasm execution error: %v (stderr: %s)", err, stderr.String()),
		}, fmt.Errorf("run wasm: %w", err)
	}

	// Parse output
	stdoutBytes := stdout.Bytes()
	var logs []string
	if stderr.Len() > 0 {
		for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
			if line != "" {
				logs = append(logs, "[WASM STDERR] "+line)
			}
		}
	}

	resp = parseWasmOutput(stdoutBytes)
	resp.DurationMs = duration
	resp.Runtime = RuntimeWasm
	resp.Logs = logs
	return resp, nil
}

func parseWasmOutput(out []byte) *Response {
	resp := &Response{
		Status:  200,
		Headers: make(map[string]string),
	}

	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		resp.Body = ""
		return resp
	}

	// Attempt to unmarshal as structured response
	var structured struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    any               `json:"body"`
	}

	if err := json.Unmarshal(trimmed, &structured); err == nil && (structured.Status != 0 || len(structured.Headers) > 0) {
		resp.Status = structured.Status
		if resp.Status == 0 {
			resp.Status = 200
		}
		resp.Headers = structured.Headers
		if resp.Headers == nil {
			resp.Headers = make(map[string]string)
		}

		switch b := structured.Body.(type) {
		case string:
			resp.Body = b
		case nil:
			resp.Body = ""
		default:
			encoded, _ := json.Marshal(b)
			resp.Body = string(encoded)
		}

		if resp.Headers["Content-Type"] == "" {
			resp.Headers["Content-Type"] = "application/json"
		}
		return resp
	}

	// Fallback: direct string output
	resp.Body = string(trimmed)
	if strings.HasPrefix(resp.Body, "{") || strings.HasPrefix(resp.Body, "[") {
		resp.Headers["Content-Type"] = "application/json"
	} else if strings.HasPrefix(resp.Body, "<!DOCTYPE") || strings.HasPrefix(resp.Body, "<html") {
		resp.Headers["Content-Type"] = "text/html; charset=utf-8"
	} else {
		resp.Headers["Content-Type"] = "text/plain; charset=utf-8"
	}

	return resp
}

// Close releases the underlying Wazero runtime.
func (w *WasmRunner) Close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.runtime != nil {
		return w.runtime.Close(ctx)
	}
	return nil
}
