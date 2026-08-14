package edge_examples_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nnlgsakib/membuss/core/memedge"
)

func TestEdgeExamples_Execution(t *testing.T) {
	ctx := context.Background()
	engine, err := memedge.NewEngine(ctx, memedge.DefaultConfig())
	if err != nil {
		t.Fatalf("failed to initialize edge engine: %v", err)
	}
	defer engine.Close()

	// -------------------------------------------------------------
	// 1. Run JavaScript Example 1: Hello World
	// -------------------------------------------------------------
	t.Run("JS_Hello_Function", func(t *testing.T) {
		jsPath := filepath.Join("..", "examples", "edge", "js", "hello.js")
		code, err := os.ReadFile(jsPath)
		if err != nil {
			jsPath = filepath.Join("js", "hello.js")
			code, err = os.ReadFile(jsPath)
			if err != nil {
				t.Fatalf("failed to read hello.js: %v", err)
			}
		}

		val, err := memedge.ValidateCode(ctx, "hello.js", code, memedge.RuntimeJS)
		if err != nil || !val.Valid {
			t.Fatalf("validation failed: %v", err)
		}

		req := &memedge.Request{
			Method:   "GET",
			Path:     "/api/hello",
			Query:    map[string]string{"name": "Alice", "greeting": "Bonjour"},
			ClientIP: "192.168.1.100",
		}

		start := time.Now()
		resp, err := engine.Execute(ctx, code, memedge.RuntimeJS, req, nil)
		dur := time.Since(start)

		if err != nil {
			t.Fatalf("execution failed: %v", err)
		}
		if resp.Status != 200 {
			t.Errorf("expected status 200, got %d", resp.Status)
		}
		if !strings.Contains(resp.Body, "Bonjour, Alice!") {
			t.Errorf("expected body to contain 'Bonjour, Alice!', got %s", resp.Body)
		}

		t.Logf("✅ JS Hello Output (Execution time: %v):\n%s", dur, resp.Body)
	})

	// -------------------------------------------------------------
	// 2. Run JavaScript Example 2: Currency Converter
	// -------------------------------------------------------------
	t.Run("JS_Currency_Converter", func(t *testing.T) {
		jsPath := filepath.Join("..", "examples", "edge", "js", "currency_converter.js")
		code, err := os.ReadFile(jsPath)
		if err != nil {
			jsPath = filepath.Join("js", "currency_converter.js")
			code, err = os.ReadFile(jsPath)
			if err != nil {
				t.Fatalf("failed to read currency_converter.js: %v", err)
			}
		}

		req := &memedge.Request{
			Method:   "POST",
			Path:     "/api/convert",
			Body:     `{"amount": 250, "from": "USD", "to": "EUR"}`,
			ClientIP: "10.0.0.5",
		}

		start := time.Now()
		resp, err := engine.Execute(ctx, code, memedge.RuntimeJS, req, nil)
		dur := time.Since(start)

		if err != nil {
			t.Fatalf("execution failed: %v", err)
		}
		if resp.Status != 200 {
			t.Errorf("expected status 200, got %d", resp.Status)
		}
		if !strings.Contains(resp.Body, "230") { // 250 * 0.92 = 230
			t.Errorf("expected body to contain converted amount 230, got %s", resp.Body)
		}

		t.Logf("✅ JS Currency Output (Execution time: %v):\n%s", dur, resp.Body)
	})

	// -------------------------------------------------------------
	// 3. Run JavaScript Example 3: Multi-Route REST API Router
	// -------------------------------------------------------------
	t.Run("JS_MultiRoute_Router", func(t *testing.T) {
		jsPath := filepath.Join("..", "examples", "edge", "js", "router.js")
		code, err := os.ReadFile(jsPath)
		if err != nil {
			jsPath = filepath.Join("js", "router.js")
			code, err = os.ReadFile(jsPath)
			if err != nil {
				t.Fatalf("failed to read router.js: %v", err)
			}
		}

		// 3a. GET /healthz
		respHealth, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "GET",
			Path:   "/healthz",
		}, nil)
		if err != nil || respHealth.Status != 200 || !strings.Contains(respHealth.Body, "healthy") {
			t.Errorf("healthz failed: status=%d, err=%v", respHealth.Status, err)
		}

		// 3b. GET /products?min_price=100
		respFiltered, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "GET",
			Path:   "/products",
			Query:  map[string]string{"min_price": "100"},
		}, nil)
		if err != nil || respFiltered.Status != 200 || !strings.Contains(respFiltered.Body, "Decentralized Storage Node") {
			t.Errorf("products filter failed: status=%d, body=%s", respFiltered.Status, respFiltered.Body)
		}

		// 3c. GET /products/2
		respID, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "GET",
			Path:   "/products/2",
		}, nil)
		if err != nil || respID.Status != 200 || !strings.Contains(respID.Body, "MemEdge Compute Credit") {
			t.Errorf("product ID match failed: status=%d, body=%s", respID.Status, respID.Body)
		}

		// 3d. POST /products
		respPost, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "POST",
			Path:   "/products",
			Body:   `{"name":"Decentralized AI Worker","price":599,"stock":5}`,
		}, nil)
		if err != nil || respPost.Status != 201 || !strings.Contains(respPost.Body, "Product created") {
			t.Errorf("product creation failed: status=%d, body=%s", respPost.Status, respPost.Body)
		}

		// 3e. 404 Route Not Found
		resp404, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "GET",
			Path:   "/unknown/resource",
		}, nil)
		if err != nil || resp404.Status != 404 {
			t.Errorf("expected 404 for unknown route, got %d", resp404.Status)
		}

		t.Logf("✅ JS Multi-Route Router: 5/5 route assertions passed!")
	})

	// -------------------------------------------------------------
	// 4. Run JavaScript Example 4: Auth & RBAC Security Gateway
	// -------------------------------------------------------------
	t.Run("JS_Auth_RBAC_Gateway", func(t *testing.T) {
		jsPath := filepath.Join("..", "examples", "edge", "js", "auth_middleware.js")
		code, err := os.ReadFile(jsPath)
		if err != nil {
			jsPath = filepath.Join("js", "auth_middleware.js")
			code, err = os.ReadFile(jsPath)
			if err != nil {
				t.Fatalf("failed to read auth_middleware.js: %v", err)
			}
		}

		// 4a. Public Route: GET /public/info -> 200
		respPub, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "GET",
			Path:   "/public/info",
		}, nil)
		if err != nil || respPub.Status != 200 {
			t.Errorf("public route failed: %v", err)
		}

		// 4b. Missing Token: GET /admin/dashboard -> 401
		respNoToken, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method: "GET",
			Path:   "/admin/dashboard",
		}, nil)
		if err != nil || respNoToken.Status != 401 {
			t.Errorf("expected 401 for missing token, got %d", respNoToken.Status)
		}

		// 4c. Forbidden Role: GET /admin/dashboard with viewer token -> 403
		respForbidden, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method:  "GET",
			Path:    "/admin/dashboard",
			Headers: map[string]string{"Authorization": "Bearer viewer-secret-token-456"},
		}, nil)
		if err != nil || respForbidden.Status != 403 {
			t.Errorf("expected 403 for viewer token on admin route, got %d", respForbidden.Status)
		}

		// 4d. Authorized Admin: GET /admin/dashboard with admin token -> 200
		respAdmin, err := engine.Execute(ctx, code, memedge.RuntimeJS, &memedge.Request{
			Method:  "GET",
			Path:    "/admin/dashboard",
			Headers: map[string]string{"Authorization": "Bearer admin-secret-token-999"},
		}, nil)
		if err != nil || respAdmin.Status != 200 || !strings.Contains(respAdmin.Body, "Alice Admin") {
			t.Errorf("admin route failed: status=%d, body=%s", respAdmin.Status, respAdmin.Body)
		}

		t.Logf("✅ JS Auth & RBAC: 4/4 security assertions passed!")
	})

	// -------------------------------------------------------------
	// 5. Run Go WebAssembly Example 1: Echo WASI
	// -------------------------------------------------------------
	t.Run("Go_Wasm_Echo_Function", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "edge", "go-echo", "echo.wasm")
		code, err := os.ReadFile(wasmPath)
		if err != nil {
			wasmPath = filepath.Join("go-echo", "echo.wasm")
			code, err = os.ReadFile(wasmPath)
			if err != nil {
				t.Fatalf("failed to read echo.wasm: %v", err)
			}
		}

		val, err := memedge.ValidateCode(ctx, "echo.wasm", code, memedge.RuntimeWasm)
		if err != nil || !val.Valid {
			t.Fatalf("wasm validation failed: %v", err)
		}

		req := &memedge.Request{
			Method:   "GET",
			Path:     "/api/gopher",
			Query:    map[string]string{"user": "Membuss-Developer"},
			ClientIP: "127.0.0.1",
		}

		start := time.Now()
		resp, err := engine.Execute(ctx, code, memedge.RuntimeWasm, req, nil)
		dur := time.Since(start)

		if err != nil {
			t.Fatalf("wasm execution failed: %v", err)
		}
		if resp.Status != 200 {
			t.Errorf("expected status 200, got %d", resp.Status)
		}
		if !strings.Contains(resp.Body, "Membuss-Developer") {
			t.Errorf("expected body to contain 'Membuss-Developer', got %s", resp.Body)
		}

		t.Logf("✅ Go Wasm Echo Output (Execution time: %v):\n%s", dur, resp.Body)
	})

	// -------------------------------------------------------------
	// 6. Run Go WebAssembly Example 2: Hasher WASI
	// -------------------------------------------------------------
	t.Run("Go_Wasm_Hasher_Function", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "edge", "go-hasher", "hasher.wasm")
		code, err := os.ReadFile(wasmPath)
		if err != nil {
			wasmPath = filepath.Join("go-hasher", "hasher.wasm")
			code, err = os.ReadFile(wasmPath)
			if err != nil {
				t.Fatalf("failed to read hasher.wasm: %v", err)
			}
		}

		val, err := memedge.ValidateCode(ctx, "hasher.wasm", code, memedge.RuntimeWasm)
		if err != nil || !val.Valid {
			t.Fatalf("wasm validation failed: %v", err)
		}

		req := &memedge.Request{
			Method:   "POST",
			Path:     "/api/hash",
			Body:     "Super-Secret-Data-To-Hash-On-Edge",
			ClientIP: "192.168.1.50",
		}

		start := time.Now()
		resp, err := engine.Execute(ctx, code, memedge.RuntimeWasm, req, nil)
		dur := time.Since(start)

		if err != nil {
			t.Fatalf("wasm execution failed: %v", err)
		}
		if resp.Status != 200 {
			t.Errorf("expected status 200, got %d", resp.Status)
		}
		if !strings.Contains(resp.Body, "850a6ce2f710bfd6af764bb78c75becc5f9dda41dfce488767d157639f66790e") {
			t.Errorf("expected correct sha256 in response, got %s", resp.Body)
		}

		t.Logf("✅ Go Wasm Hasher Output (Execution time: %v):\n%s", dur, resp.Body)
	})

	// -------------------------------------------------------------
	// 7. Run Go WebAssembly Example 3: Multi-Route Router WASI
	// -------------------------------------------------------------
	t.Run("Go_Wasm_MultiRoute_Router", func(t *testing.T) {
		wasmPath := filepath.Join("..", "examples", "edge", "go-router", "router.wasm")
		code, err := os.ReadFile(wasmPath)
		if err != nil {
			wasmPath = filepath.Join("go-router", "router.wasm")
			code, err = os.ReadFile(wasmPath)
			if err != nil {
				t.Fatalf("failed to read router.wasm: %v", err)
			}
		}

		val, err := memedge.ValidateCode(ctx, "router.wasm", code, memedge.RuntimeWasm)
		if err != nil || !val.Valid {
			t.Fatalf("wasm validation failed: %v", err)
		}

		// 7a. GET /healthz
		respHealth, err := engine.Execute(ctx, code, memedge.RuntimeWasm, &memedge.Request{
			Method: "GET",
			Path:   "/healthz",
		}, nil)
		if err != nil || respHealth.Status != 200 || !strings.Contains(respHealth.Body, "healthy") {
			t.Errorf("WASI healthz failed: status=%d, err=%v", respHealth.Status, err)
		}

		// 7b. GET /api/v1/users
		respUsers, err := engine.Execute(ctx, code, memedge.RuntimeWasm, &memedge.Request{
			Method: "GET",
			Path:   "/api/v1/users",
		}, nil)
		if err != nil || respUsers.Status != 200 || !strings.Contains(respUsers.Body, "alice") {
			t.Errorf("WASI users failed: status=%d, body=%s", respUsers.Status, respUsers.Body)
		}

		// 7c. POST /api/v1/users
		respCreate, err := engine.Execute(ctx, code, memedge.RuntimeWasm, &memedge.Request{
			Method: "POST",
			Path:   "/api/v1/users",
			Body:   `{"username":"satoshi","role":"founder"}`,
		}, nil)
		if err != nil || respCreate.Status != 201 || !strings.Contains(respCreate.Body, "User registered successfully") {
			t.Errorf("WASI create user failed: status=%d, body=%s", respCreate.Status, respCreate.Body)
		}

		// 7d. GET /api/v1/calc?op=mul&a=6&b=7
		respCalc, err := engine.Execute(ctx, code, memedge.RuntimeWasm, &memedge.Request{
			Method: "GET",
			Path:   "/api/v1/calc",
			Query:  map[string]string{"op": "mul", "a": "6", "b": "7"},
		}, nil)
		if err != nil || respCalc.Status != 200 || !strings.Contains(respCalc.Body, "42") {
			t.Errorf("WASI math calc failed: status=%d, body=%s", respCalc.Status, respCalc.Body)
		}

		t.Logf("✅ Go WASI Multi-Route Router: 4/4 route assertions passed!")
	})
}
