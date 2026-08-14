package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nnlgsakib/membuss/core/memedge"
)

func TestNodeAPI_EdgeEndpoints(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend()

	edgeEngine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create edge engine: %v", err)
	}
	defer edgeEngine.Close()

	nodeAPI, err := New(Config{
		Backend:    b,
		EdgeEngine: edgeEngine,
	})
	if err != nil {
		t.Fatalf("failed to create node API: %v", err)
	}

	handler := nodeAPI.Handler()

	// 1. Test POST /api/v1/edge/run
	runPayload := map[string]any{
		"code": `
export default function(req) {
	return {
		status: 200,
		body: { message: "API ok", user: req.query.user }
	};
}
`,
		"runtime": "js",
		"path":    "/api/calc.js",
		"query":   map[string]string{"user": "Alice"},
	}
	body, _ := json.Marshal(runPayload)
	req := httptest.NewRequest("POST", "/api/v1/edge/run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for edge run, got %d: %s", w.Code, w.Body.String())
	}

	var runResp envelope
	if err := json.Unmarshal(w.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("unmarshal run resp: %v", err)
	}
	if !runResp.OK {
		t.Errorf("expected OK=true, got %v", runResp)
	}

	// 2. Test GET /api/v1/edge/status
	statusReq := httptest.NewRequest("GET", "/api/v1/edge/status", nil)
	statusW := httptest.NewRecorder()

	handler.ServeHTTP(statusW, statusReq)

	if statusW.Code != http.StatusOK {
		t.Fatalf("expected status 200 for edge status, got %d: %s", statusW.Code, statusW.Body.String())
	}

	// 3. Test POST /api/v1/edge/validate
	valPayload := map[string]any{
		"code":    "function handler(req) { return 1; }",
		"path":    "/api/test.js",
		"runtime": "js",
	}
	valBody, _ := json.Marshal(valPayload)
	valReq := httptest.NewRequest("POST", "/api/v1/edge/validate", bytes.NewReader(valBody))
	valReq.Header.Set("Content-Type", "application/json")
	valW := httptest.NewRecorder()

	handler.ServeHTTP(valW, valReq)

	if valW.Code != http.StatusOK {
		t.Fatalf("expected status 200 for validate, got %d", valW.Code)
	}

	var valResp envelope
	if err := json.Unmarshal(valW.Body.Bytes(), &valResp); err != nil {
		t.Fatalf("unmarshal val resp: %v", err)
	}
	if !valResp.OK {
		t.Errorf("expected validation OK=true, got %v", valResp)
	}
}
