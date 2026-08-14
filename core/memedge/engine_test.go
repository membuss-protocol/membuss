package memedge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJSRunner_SimpleHello(t *testing.T) {
	ctx := context.Background()
	engine, err := NewEngine(ctx, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	jsCode := `
export default async function handler(req) {
	const name = req.query.name || "World";
	console.log("Processing request for:", name);
	return {
		status: 200,
		headers: { "X-Custom": "MemEdge" },
		body: JSON.stringify({ message: "Hello " + name })
	};
}
`

	req := &Request{
		Method: "GET",
		Path:   "/api/hello.js",
		Query:  map[string]string{"name": "Alice"},
	}

	resp, err := engine.Execute(ctx, []byte(jsCode), RuntimeJS, req, nil)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if resp.Status != 200 {
		t.Errorf("expected status 200, got %d", resp.Status)
	}
	if !strings.Contains(resp.Body, "Hello Alice") {
		t.Errorf("expected body to contain 'Hello Alice', got %s", resp.Body)
	}
	if resp.Headers["X-Custom"] != "MemEdge" {
		t.Errorf("expected X-Custom header, got %v", resp.Headers)
	}
	if len(resp.Logs) == 0 || !strings.Contains(resp.Logs[0], "Processing request for: Alice") {
		t.Errorf("expected logs to be captured, got %v", resp.Logs)
	}
}

func TestJSRunner_POSTWithJSONBody(t *testing.T) {
	ctx := context.Background()
	engine, err := NewEngine(ctx, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	jsCode := `
function handler(req) {
	const data = req.json();
	return {
		status: 201,
		body: {
			received: data,
			total: data.a + data.b
		}
	};
}
`

	req := &Request{
		Method: "POST",
		Path:   "/api/calculate.js",
		Body:   `{"a": 15, "b": 27}`,
	}

	resp, err := engine.Execute(ctx, []byte(jsCode), RuntimeJS, req, nil)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if resp.Status != 201 {
		t.Errorf("expected status 201, got %d", resp.Status)
	}
	if !strings.Contains(resp.Body, `"total":42`) {
		t.Errorf("expected body to contain '\"total\":42', got %s", resp.Body)
	}
}

func TestJSRunner_Timeout(t *testing.T) {
	ctx := context.Background()
	engine, err := NewEngine(ctx, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	infiniteLoopJS := `
function handler(req) {
	while(true) {}
}
`

	limits := &Limits{
		MaxExecutionTime: 50 * time.Millisecond,
		MaxMemoryBytes:   16 << 20,
	}

	req := &Request{
		Method: "GET",
		Path:   "/api/hang.js",
	}

	resp, err := engine.Execute(ctx, []byte(infiniteLoopJS), RuntimeJS, req, limits)
	if err == nil {
		t.Fatalf("expected timeout error, but got nil error")
	}

	if resp == nil || resp.Status != 504 {
		t.Errorf("expected status 504 Gateway Timeout, got %v", resp)
	}
}

func TestWasmRunner_Execution(t *testing.T) {
	ctx := context.Background()
	engine, err := NewEngine(ctx, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	tmpDir := t.TempDir()
	goCode := `package main
import "fmt"
func main() {
	fmt.Println("{\"status\": 200, \"body\": \"Hello from Wasm!\"}")
}
`
	srcPath := filepath.Join(tmpDir, "main.go")
	wasmPath := filepath.Join(tmpDir, "main.wasm")
	if err := os.WriteFile(srcPath, []byte(goCode), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", wasmPath, srcPath)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("skipping live wasip1 compilation: %v (out: %s)", err, string(out))
		return
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}

	req := &Request{
		Method: "GET",
		Path:   "/api/test.wasm",
	}

	resp, err := engine.Execute(ctx, wasmBytes, RuntimeWasm, req, nil)
	if err != nil {
		t.Fatalf("wasm execution failed: %v", err)
	}

	if resp.Status != 200 {
		t.Errorf("expected status 200, got %d", resp.Status)
	}
	if !strings.Contains(resp.Body, "Hello from Wasm!") {
		t.Errorf("expected body to contain 'Hello from Wasm!', got %s", resp.Body)
	}
}

func TestEngine_Concurrency(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.MaxConcurrentTasks = 8
	engine, err := NewEngine(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	jsCode := `
function handler(req) {
	return { status: 200, body: "worker-" + req.query.id };
}
`

	var wg sync.WaitGroup
	numRequests := 20

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := &Request{
				Method: "GET",
				Path:   "/api/concurrency.js",
				Query:  map[string]string{"id": string(rune('0' + (id % 10)))},
			}
			resp, execErr := engine.Execute(ctx, []byte(jsCode), RuntimeJS, req, nil)
			if execErr != nil {
				t.Errorf("concurrent request %d failed: %v", id, execErr)
				return
			}
			if resp.Status != 200 {
				t.Errorf("request %d returned non-200: %d", id, resp.Status)
			}
		}(i)
	}

	wg.Wait()

	stats := engine.Stats()
	if stats.TotalExecutions != uint64(numRequests) {
		t.Errorf("expected %d total executions, got %d", numRequests, stats.TotalExecutions)
	}
}

func TestEngine_DetectRuntime(t *testing.T) {
	if DetectRuntime("/api/test.wasm", []byte("xyz")) != RuntimeWasm {
		t.Errorf("expected .wasm extension to detect as RuntimeWasm")
	}
	if DetectRuntime("/api/test.js", []byte("xyz")) != RuntimeJS {
		t.Errorf("expected .js extension to detect as RuntimeJS")
	}
	if DetectRuntime("/api/unknown", []byte{0x00, 0x61, 0x73, 0x6d, 0x01}) != RuntimeWasm {
		t.Errorf("expected wasm magic bytes to detect as RuntimeWasm")
	}
}
