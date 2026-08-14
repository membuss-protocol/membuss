package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type EdgeRequest struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Query    map[string]string `json:"query"`
	Headers  map[string]string `json:"headers"`
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
	_ = json.NewDecoder(os.Stdin).Decode(&req)

	path := req.Path
	if strings.HasPrefix(path, "/mem/") {
		parts := strings.SplitN(strings.TrimPrefix(path, "/mem/"), "/", 2)
		if len(parts) == 2 {
			path = "/" + parts[1]
		} else {
			path = "/"
		}
	}

	var res EdgeResponse
	res.Headers = map[string]string{
		"Content-Type":  "application/json",
		"X-WASI-Router": "Go-Wazero-Router",
	}

	switch {
	// 1. Health check & route catalog: GET / or GET /healthz
	case path == "/" || path == "/healthz":
		res.Status = 200
		res.Body = renderJSON(map[string]any{
			"status":  "healthy",
			"service": "Go WASI Multi-Route Microservice",
			"time":    time.Now().UTC().Format(time.RFC3339),
			"routes": []string{
				"GET  /healthz",
				"GET  /api/v1/users",
				"POST /api/v1/users",
				"GET  /api/v1/calc",
			},
		})

	// 2. List users: GET /api/v1/users
	case path == "/api/v1/users" && req.Method == "GET":
		res.Status = 200
		res.Body = renderJSON(map[string]any{
			"total": 3,
			"users": []map[string]any{
				{"id": 1, "username": "alice", "role": "admin"},
				{"id": 2, "username": "bob", "role": "developer"},
				{"id": 3, "username": "carol", "role": "node_operator"},
			},
		})

	// 3. Create user: POST /api/v1/users
	case path == "/api/v1/users" && req.Method == "POST":
		var userPayload map[string]any
		if err := json.Unmarshal([]byte(req.Body), &userPayload); err != nil || len(userPayload) == 0 {
			res.Status = 400
			res.Body = renderJSON(map[string]any{"error": "Invalid or empty JSON body"})
		} else {
			res.Status = 201
			res.Body = renderJSON(map[string]any{
				"message": "User registered successfully",
				"user":    userPayload,
				"mid":     "mem1user" + fmt.Sprintf("%d", time.Now().UnixNano()),
			})
		}

	// 4. Query Parameter Math Engine: GET /api/v1/calc?op=add&a=10&b=25
	case path == "/api/v1/calc" && req.Method == "GET":
		op := req.Query["op"]
		aStr := req.Query["a"]
		bStr := req.Query["b"]

		a, _ := strconv.ParseFloat(aStr, 64)
		b, _ := strconv.ParseFloat(bStr, 64)

		var result float64
		switch op {
		case "add":
			result = a + b
		case "sub":
			result = a - b
		case "mul":
			result = a * b
		case "div":
			if b != 0 {
				result = a / b
			}
		default:
			op = "add"
			result = a + b
		}

		res.Status = 200
		res.Body = renderJSON(map[string]any{
			"operation": op,
			"a":         a,
			"b":         b,
			"result":    result,
			"engine":    "WASI-Pure-Go",
		})

	default:
		res.Status = 404
		res.Body = renderJSON(map[string]any{
			"error":  "Endpoint not found",
			"path":   path,
			"method": req.Method,
		})
	}

	_ = json.NewEncoder(os.Stdout).Encode(res)
}

func renderJSON(data any) string {
	b, _ := json.MarshalIndent(data, "", "  ")
	return string(b)
}
