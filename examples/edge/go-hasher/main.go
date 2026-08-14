// examples/edge/go-hasher/main.go
// Go WebAssembly / WASI Edge Function for Cryptographic Hashing
package main

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"os"
)

type EdgeRequest struct {
	Method   string            `json:"method"`
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
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		emit(400, `{"error":"invalid json input"}`)
		return
	}

	input := req.Query["data"]
	if input == "" {
		input = req.Body
	}
	if input == "" {
		input = "membuss-decentralized-content-network"
	}

	// Compute SHA-256 and SHA-512 in WASI sandbox
	s256 := sha256.Sum256([]byte(input))
	s512 := sha512.Sum512([]byte(input))

	respMap := map[string]any{
		"input":          input,
		"input_length":   len(input),
		"sha256":         hex.EncodeToString(s256[:]),
		"sha512":         hex.EncodeToString(s512[:]),
		"algorithm":      "WASI-Crypto-Sandbox",
		"secure_runtime": "Wazero Pure Go WebAssembly",
	}

	bodyBytes, _ := json.MarshalIndent(respMap, "", "  ")
	emit(200, string(bodyBytes))
}

func emit(status int, body string) {
	res := EdgeResponse{
		Status: status,
		Headers: map[string]string{
			"Content-Type":      "application/json",
			"X-Membuss-Runtime": "Wazero-WASI-Go",
		},
		Body: body,
	}
	_ = json.NewEncoder(os.Stdout).Encode(res)
}
