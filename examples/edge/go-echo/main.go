// examples/edge/go-echo/main.go
// Go WebAssembly / WASI Edge Function for MemEdge
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type EdgeRequest struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Query    map[string]string `json:"query"`
	Body     string            `json:"body"`
	ClientIP string            `json:"client_ip"`
}

type EdgeResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func main() {
	var req EdgeRequest
	// 1. Read the HTTP request payload passed from MemEdge via Stdin
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		emitError(400, fmt.Sprintf("failed to decode JSON request: %v", err))
		return
	}

	user := req.Query["user"]
	if user == "" {
		user = "Gopher Explorer"
	}

	// 2. Build our response
	responseData := map[string]any{
		"message":      fmt.Sprintf("Hello %s from pure Go compiled to WebAssembly (WASI)!", user),
		"method":       req.Method,
		"path":         req.Path,
		"client_ip":    req.ClientIP,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
		"engine":       "MemEdge (Wazero JIT Runtime)",
		"wasi_version": "wasip1",
	}

	bodyBytes, _ := json.MarshalIndent(responseData, "", "  ")

	res := EdgeResponse{
		Status: 200,
		Headers: map[string]string{
			"Content-Type":       "application/json",
			"X-Membuss-Runtime":  "Wazero-Wasm-WASI",
			"X-Engine-Language":  "Go",
		},
		Body: string(bodyBytes),
	}

	// 3. Output the HTTP response to Stdout
	_ = json.NewEncoder(os.Stdout).Encode(res)
}

func emitError(status int, msg string) {
	res := EdgeResponse{
		Status: status,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    fmt.Sprintf(`{"error": "%s"}`, msg),
	}
	_ = json.NewEncoder(os.Stdout).Encode(res)
}
