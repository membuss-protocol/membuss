package explorer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nnlgsakib/membuss/core/memedge"
)

func TestExplorer_EdgeRunAndStatus(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend()

	edgeEngine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create edge engine: %v", err)
	}
	defer edgeEngine.Close()

	exp, err := New(Config{
		Backend:    b,
		EdgeEngine: edgeEngine,
	})
	if err != nil {
		t.Fatalf("failed to create explorer: %v", err)
	}

	// 1. Test /edge/run
	runPayload := map[string]any{
		"code": `
export default function(req) {
	return {
		status: 200,
		body: { message: "Hello " + (req.query.user || "dev") }
	};
}
`,
		"runtime": "js",
		"path":    "/api/greet.js",
		"query":   map[string]string{"user": "Satoshi"},
	}

	bodyBytes, _ := json.Marshal(runPayload)
	req := httptest.NewRequest("POST", "/edge/run", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	exp.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp memedge.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != 200 {
		t.Errorf("expected inner status 200, got %d", resp.Status)
	}
	if !strings.Contains(resp.Body, "Satoshi") {
		t.Errorf("expected body to contain 'Satoshi', got %s", resp.Body)
	}

	// 2. Test /edge/status
	statusReq := httptest.NewRequest("GET", "/edge/status", nil)
	statusW := httptest.NewRecorder()

	exp.Router().ServeHTTP(statusW, statusReq)

	if statusW.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /edge/status, got %d", statusW.Code)
	}

	var stats memedge.Stats
	if err := json.Unmarshal(statusW.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to unmarshal stats: %v", err)
	}

	if !stats.Enabled {
		t.Errorf("expected stats.Enabled to be true")
	}
	if stats.TotalExecutions != 1 {
		t.Errorf("expected TotalExecutions to be 1, got %d", stats.TotalExecutions)
	}
}

func TestExplorer_EdgeDeployAndList(t *testing.T) {
	ctx := context.Background()
	b := newMemBackend()

	edgeEngine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create edge engine: %v", err)
	}
	defer edgeEngine.Close()

	exp, err := New(Config{
		Backend:    b,
		EdgeEngine: edgeEngine,
	})
	if err != nil {
		t.Fatalf("failed to create explorer: %v", err)
	}

	// 1. Deploy edge function
	deployPayload := map[string]any{
		"name": "api/hello.js",
		"code": "export default function handler(req) { return { status: 200, body: 'Hello Edge' }; }",
		"runtime": "js",
	}
	dBytes, _ := json.Marshal(deployPayload)
	dReq := httptest.NewRequest("POST", "/edge/deploy", bytes.NewReader(dBytes))
	dReq.Header.Set("Content-Type", "application/json")
	dW := httptest.NewRecorder()

	exp.Router().ServeHTTP(dW, dReq)

	if dW.Code != http.StatusOK {
		t.Fatalf("expected status 200 for deploy, got %d: %s", dW.Code, dW.Body.String())
	}

	var dep EdgeFunctionDeployment
	if err := json.Unmarshal(dW.Body.Bytes(), &dep); err != nil {
		t.Fatalf("failed to unmarshal deploy response: %v", err)
	}

	if dep.MID == "" || dep.Name != "api/hello.js" {
		t.Errorf("unexpected deploy response: %+v", dep)
	}

	// 2. List deployed functions
	listReq := httptest.NewRequest("GET", "/edge/functions", nil)
	listW := httptest.NewRecorder()

	exp.Router().ServeHTTP(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Fatalf("expected status 200 for list, got %d", listW.Code)
	}

	var list []EdgeFunctionDeployment
	if err := json.Unmarshal(listW.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to unmarshal list: %v", err)
	}

	if len(list) != 1 || list[0].MID != dep.MID {
		t.Errorf("expected 1 deployment in list, got %+v", list)
	}

	// 3. Delete function
	delReq := httptest.NewRequest("DELETE", "/edge/functions/"+dep.MID, nil)
	delW := httptest.NewRecorder()

	exp.Router().ServeHTTP(delW, delReq)

	if delW.Code != http.StatusOK {
		t.Fatalf("expected status 200 for delete, got %d", delW.Code)
	}
}
