package memedge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
)

// JSRunner executes JavaScript serverless functions inside a Goja runtime.
type JSRunner struct {
	cache *CodeCache
}

// NewJSRunner creates a new JSRunner instance.
func NewJSRunner(cache *CodeCache) *JSRunner {
	if cache == nil {
		cache = NewCodeCache(128)
	}
	return &JSRunner{
		cache: cache,
	}
}

// Execute runs the JavaScript source code against the provided Request and Limits.
func (r *JSRunner) Execute(ctx context.Context, code []byte, req *Request, limits Limits) (resp *Response, err error) {
	start := time.Now()

	// Panic safety watchdog
	defer func() {
		if rec := recover(); rec != nil {
			resp = &Response{
				Status:     500,
				DurationMs: float64(time.Since(start).Microseconds()) / 1000.0,
				Runtime:    RuntimeJS,
				Error:      fmt.Sprintf("JavaScript runtime panic: %v", rec),
			}
			err = fmt.Errorf("js panic: %v", rec)
		}
	}()

	execCtx, cancel := context.WithTimeout(ctx, limits.MaxExecutionTime)
	defer cancel()

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	// Setup logging capture
	var logs []string
	var logsMu sync.Mutex
	logFn := func(prefix string) func(call goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			var parts []string
			for _, arg := range call.Arguments {
				parts = append(parts, fmt.Sprintf("%v", arg.Export()))
			}
			line := prefix + strings.Join(parts, " ")
			logsMu.Lock()
			logs = append(logs, line)
			logsMu.Unlock()
			return goja.Undefined()
		}
	}

	consoleObj := vm.NewObject()
	_ = consoleObj.Set("log", logFn("[LOG] "))
	_ = consoleObj.Set("info", logFn("[INFO] "))
	_ = consoleObj.Set("warn", logFn("[WARN] "))
	_ = consoleObj.Set("error", logFn("[ERROR] "))
	_ = vm.Set("console", consoleObj)

	// Setup request object
	reqObj := vm.NewObject()
	_ = reqObj.Set("method", req.Method)
	_ = reqObj.Set("url", req.URL)
	_ = reqObj.Set("path", req.Path)
	_ = reqObj.Set("client_ip", req.ClientIP)
	_ = reqObj.Set("headers", req.Headers)
	_ = reqObj.Set("query", req.Query)
	_ = reqObj.Set("params", req.Params)
	_ = reqObj.Set("body", req.Body)

	// JSON parsing helper method: req.json()
	_ = reqObj.Set("json", func(call goja.FunctionCall) goja.Value {
		if req.Body == "" {
			return vm.ToValue(nil)
		}
		var parsed any
		if err := json.Unmarshal([]byte(req.Body), &parsed); err != nil {
			panic(vm.ToValue(fmt.Sprintf("Invalid JSON body: %v", err)))
		}
		return vm.ToValue(parsed)
	})

	_ = vm.Set("req", reqObj)
	_ = vm.Set("request", reqObj)

	// Web standard polyfills: atob, btoa, crypto.randomUUID
	_ = vm.Set("btoa", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		str := call.Arguments[0].String()
		return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(str)))
	})

	_ = vm.Set("atob", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		str := call.Arguments[0].String()
		decoded, err := base64.StdEncoding.DecodeString(str)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("Invalid base64 string: %v", err)))
		}
		return vm.ToValue(string(decoded))
	})

	cryptoObj := vm.NewObject()
	_ = cryptoObj.Set("randomUUID", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(uuid.NewString())
	})
	_ = vm.Set("crypto", cryptoObj)

	// Setup module/exports compatibility shims
	moduleObj := vm.NewObject()
	exportsObj := vm.NewObject()
	_ = moduleObj.Set("exports", exportsObj)
	_ = vm.Set("module", moduleObj)
	_ = vm.Set("exports", exportsObj)

	// Compile or retrieve from cache
	cacheKey := "js:" + KeyForCode(code)
	var program *goja.Program
	if cached, found := r.cache.Get(cacheKey); found {
		if p, ok := cached.(*goja.Program); ok {
			program = p
		}
	}

	if program == nil {
		sourceCode := normalizeJSSource(string(code))
		compiled, err := goja.Compile("handler.js", sourceCode, true)
		if err != nil {
			return &Response{
				Status:     500,
				DurationMs: float64(time.Since(start).Microseconds()) / 1000.0,
				Runtime:    RuntimeJS,
				Error:      "JavaScript compilation error: " + err.Error(),
				Logs:       logs,
			}, fmt.Errorf("compile js: %w", err)
		}
		program = compiled
		r.cache.Set(cacheKey, program)
	}

	// Timeout watchdog
	stopWatchdog := make(chan struct{})
	defer close(stopWatchdog)

	go func() {
		select {
		case <-stopWatchdog:
			// Execution finished normally, do not interrupt
			return
		case <-execCtx.Done():
			vm.Interrupt(ErrExecutionTimeout{Limit: limits.MaxExecutionTime})
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		}
	}()

	resVal, err := vm.RunProgram(program)
	if err != nil {
		return r.handleError(err, start, limits, logs)
	}

	// If the script defined an exported handler function (e.g. module.exports or handler), invoke it
	handlerVal := vm.Get("handler")
	if handlerVal == nil || goja.IsUndefined(handlerVal) {
		modExp := moduleObj.Get("exports")
		if modExp != nil && !goja.IsUndefined(modExp) {
			if _, ok := goja.AssertFunction(modExp); ok {
				handlerVal = modExp
			} else if modExpObj := modExp.ToObject(vm); modExpObj != nil {
				if def := modExpObj.Get("default"); def != nil && !goja.IsUndefined(def) {
					if _, ok := goja.AssertFunction(def); ok {
						handlerVal = def
					}
				}
			}
		}
	}

	if handlerVal != nil && !goja.IsUndefined(handlerVal) {
		if fn, ok := goja.AssertFunction(handlerVal); ok {
			invokedVal, invErr := fn(goja.Undefined(), reqObj)
			if invErr != nil {
				return r.handleError(invErr, start, limits, logs)
			}
			resVal = invokedVal
		}
	}

	// If the return value is a Promise, unwrap the resolved/fulfilled value
	if resVal != nil {
		if p, ok := resVal.Export().(*goja.Promise); ok {
			switch p.State() {
			case goja.PromiseStateFulfilled:
				resVal = p.Result()
			case goja.PromiseStateRejected:
				return r.handleError(fmt.Errorf("promise rejected: %v", p.Result()), start, limits, logs)
			case goja.PromiseStatePending:
				resVal = p.Result()
			}
		}
	}

	resp = formatJSResult(resVal, vm)
	resp.DurationMs = float64(time.Since(start).Microseconds()) / 1000.0
	resp.Runtime = RuntimeJS
	resp.Logs = logs
	return resp, nil
}

func (r *JSRunner) handleError(err error, start time.Time, limits Limits, logs []string) (*Response, error) {
	duration := float64(time.Since(start).Microseconds()) / 1000.0
	var timeoutErr ErrExecutionTimeout
	if errors.As(err, &timeoutErr) || strings.Contains(err.Error(), "edge execution exceeded timeout limit") || errors.Is(err, context.DeadlineExceeded) {
		return &Response{
			Status:     504,
			DurationMs: duration,
			Runtime:    RuntimeJS,
			Error:      fmt.Sprintf("Execution timed out after %v", limits.MaxExecutionTime),
			Logs:       logs,
		}, ErrExecutionTimeout{Limit: limits.MaxExecutionTime}
	}

	return &Response{
		Status:     500,
		DurationMs: duration,
		Runtime:    RuntimeJS,
		Error:      "JavaScript error: " + err.Error(),
		Logs:       logs,
	}, fmt.Errorf("run js: %w", err)
}

// normalizeJSSource adapts modern syntax to standard function definitions.
func normalizeJSSource(src string) string {
	// Strip "async " before function
	res := strings.ReplaceAll(src, "async function", "function")
	res = strings.ReplaceAll(res, "async (", "(")

	// Replace "export default function(" or "export default function ("
	res = strings.ReplaceAll(res, "export default function(", "function handler(")
	res = strings.ReplaceAll(res, "export default function (", "function handler(")
	// Replace "export default function <name>" -> "function <name>"
	res = strings.ReplaceAll(res, "export default function ", "function ")
	// Replace any other "export default " -> "module.exports.default = "
	res = strings.ReplaceAll(res, "export default ", "module.exports.default = ")

	return res
}

func formatJSResult(val goja.Value, vm *goja.Runtime) *Response {
	resp := &Response{
		Status:  200,
		Headers: make(map[string]string),
	}

	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		resp.Body = ""
		return resp
	}

	exported := val.Export()
	switch v := exported.(type) {
	case string:
		resp.Body = v
		if resp.Headers["Content-Type"] == "" {
			if strings.HasPrefix(strings.TrimSpace(v), "<!DOCTYPE") || strings.HasPrefix(strings.TrimSpace(v), "<html") {
				resp.Headers["Content-Type"] = "text/html; charset=utf-8"
			} else if (strings.HasPrefix(strings.TrimSpace(v), "{") && strings.HasSuffix(strings.TrimSpace(v), "}")) ||
				(strings.HasPrefix(strings.TrimSpace(v), "[") && strings.HasSuffix(strings.TrimSpace(v), "]")) {
				resp.Headers["Content-Type"] = "application/json"
			} else {
				resp.Headers["Content-Type"] = "text/plain; charset=utf-8"
			}
		}
	case map[string]any:
		// Check if it returned a standard { status, headers, body } object
		if statusVal, ok := v["status"]; ok {
			if s, ok := statusVal.(int64); ok {
				resp.Status = int(s)
			} else if s, ok := statusVal.(int); ok {
				resp.Status = s
			} else if s, ok := statusVal.(float64); ok {
				resp.Status = int(s)
			}
		}
		if headersVal, ok := v["headers"]; ok {
			if hm, ok := headersVal.(map[string]any); ok {
				for hk, hv := range hm {
					resp.Headers[hk] = fmt.Sprintf("%v", hv)
				}
			}
		}
		if bodyVal, ok := v["body"]; ok {
			switch b := bodyVal.(type) {
			case string:
				resp.Body = b
			default:
				encoded, _ := json.Marshal(b)
				resp.Body = string(encoded)
			}
		} else {
			// Object without explicit body is treated as JSON response
			delete(v, "status")
			delete(v, "headers")
			if len(v) > 0 {
				encoded, _ := json.Marshal(v)
				resp.Body = string(encoded)
			}
		}
		if resp.Headers["Content-Type"] == "" {
			resp.Headers["Content-Type"] = "application/json"
		}
	default:
		encoded, err := json.Marshal(v)
		if err == nil {
			resp.Body = string(encoded)
			resp.Headers["Content-Type"] = "application/json"
		} else {
			resp.Body = fmt.Sprintf("%v", v)
		}
	}

	if resp.Status == 0 {
		resp.Status = 200
	}
	return resp
}
