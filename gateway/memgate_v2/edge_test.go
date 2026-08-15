package memgate_v2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nnlgsakib/membuss/core/memedge"
	"github.com/nnlgsakib/membuss/core/mid"
)

func TestMemGate_EdgeExecution_JS(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend()

	jsCode := []byte(`
export default function(req) {
	return {
		status: 200,
		headers: { "X-Custom-Edge": "MemEdge-V2" },
		body: JSON.stringify({ hello: req.query.name || "stranger" })
	};
}
`)

	funcMID := mid.FromBytes(jsCode)
	b.putWithMeta(funcMID, jsCode, "application/javascript", "api/hello.js", "application/javascript")

	edgeEngine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create edge engine: %v", err)
	}
	defer edgeEngine.Close()

	mg, err := New(Config{
		Backend:    b,
		EdgeEngine: edgeEngine,
	})
	if err != nil {
		t.Fatalf("failed to create memgate: %v", err)
	}

	req := httptest.NewRequest("GET", "/mem/"+funcMID.String()+"?exec=true&name=MembussExplorer", nil)
	w := httptest.NewRecorder()

	mg.Router().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("X-Membuss-Edge") != "true" {
		t.Errorf("expected X-Membuss-Edge header 'true', got %s", resp.Header.Get("X-Membuss-Edge"))
	}
	if resp.Header.Get("X-Custom-Edge") != "MemEdge-V2" {
		t.Errorf("expected X-Custom-Edge header 'MemEdge-V2', got %s", resp.Header.Get("X-Custom-Edge"))
	}

	body := w.Body.String()
	if !strings.Contains(body, "MembussExplorer") {
		t.Errorf("expected body to contain 'MembussExplorer', got %s", body)
	}
}

func TestMemGate_EdgeExecution_MultiRoute(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend()

	jsCode := []byte(`
export default function(req) {
	if (req.path === "/users/42" && req.method === "GET") {
		return {
			status: 200,
			body: JSON.stringify({ id: 42, name: "Alice", role: "admin" })
		};
	}
	if (req.path === "/healthz") {
		return {
			status: 200,
			body: JSON.stringify({ status: "ok" })
		};
	}
	return {
		status: 404,
		body: JSON.stringify({ error: "Not found", path: req.path })
	};
}
`)

	funcMID := mid.FromBytes(jsCode)
	b.putWithMeta(funcMID, jsCode, "application/javascript", "api/router.js", "application/javascript")

	edgeEngine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create edge engine: %v", err)
	}
	defer edgeEngine.Close()

	mg, err := New(Config{
		Backend:    b,
		EdgeEngine: edgeEngine,
	})
	if err != nil {
		t.Fatalf("failed to create memgate: %v", err)
	}

	// 1. Call sub-route /mem/{MID}/users/42?exec=true
	req1 := httptest.NewRequest("GET", "/mem/"+funcMID.String()+"/users/42?exec=true", nil)
	w1 := httptest.NewRecorder()
	mg.Router().ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected status 200 for subroute, got %d: %s", w1.Code, w1.Body.String())
	}
	if !strings.Contains(w1.Body.String(), "Alice") {
		t.Errorf("expected body to contain 'Alice', got %s", w1.Body.String())
	}

	// 2. Call sub-route /mem/{MID}/healthz?exec=true
	req2 := httptest.NewRequest("GET", "/mem/"+funcMID.String()+"/healthz?exec=true", nil)
	w2 := httptest.NewRecorder()
	mg.Router().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /healthz, got %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "ok") {
		t.Errorf("expected body to contain 'ok', got %s", w2.Body.String())
	}
}

func TestMemGate_EdgeExecution_CORSAndPayloadLimits(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend()

	jsCode := []byte(`
export default function(req) {
	return { status: 200, body: JSON.stringify({ ok: true }) };
}
`)

	funcMID := mid.FromBytes(jsCode)
	b.putWithMeta(funcMID, jsCode, "application/javascript", "api/test.js", "application/javascript")

	edgeEngine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create edge engine: %v", err)
	}
	defer edgeEngine.Close()

	customLimits := memedge.DefaultLimits()
	customLimits.MaxBodySizeBytes = 64 // 64 bytes max for testing

	mg, err := New(Config{
		Backend:    b,
		EdgeEngine: edgeEngine,
		EdgeLimits: &customLimits,
	})
	if err != nil {
		t.Fatalf("failed to create memgate: %v", err)
	}

	// 1. Test CORS OPTIONS preflight
	reqOptions := httptest.NewRequest("OPTIONS", "/mem/"+funcMID.String()+"?exec=true", nil)
	wOptions := httptest.NewRecorder()
	mg.Router().ServeHTTP(wOptions, reqOptions)

	if wOptions.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 for OPTIONS preflight, got %d", wOptions.Code)
	}
	if wOptions.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected Access-Control-Allow-Origin '*', got %s", wOptions.Header().Get("Access-Control-Allow-Origin"))
	}

	// 2. Test Payload Too Large (413)
	largeBody := strings.Repeat("x", 200) // 200 bytes > 64 bytes limit
	reqLarge := httptest.NewRequest("POST", "/mem/"+funcMID.String()+"?exec=true", strings.NewReader(largeBody))
	wLarge := httptest.NewRecorder()
	mg.Router().ServeHTTP(wLarge, reqLarge)

	if wLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 Payload Too Large, got %d", wLarge.Code)
	}
}


