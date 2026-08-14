---
title: MemEdge — Decentralized Serverless Edge Functions
sidebar_label: MemEdge (Edge Compute)
sidebar_position: 6
---

# ⚡ MemEdge: Decentralized Serverless Edge Functions

**MemEdge** is Membuss's decentralized, content-addressed serverless compute engine. It enables executing dynamic **JavaScript** and **WebAssembly (WASI / Go / Rust)** functions directly on the P2P edge with microsecond cold starts, zero centralized infrastructure, and native integration with **MemFS (Merkle DAGs)** and **MemNS (Mutable Pointers)**.

```
                           Incoming Gateway HTTP Request
                    (e.g., /mem/{MID}/api/products/42?exec=true)
                                         │
                                         ▼
                          ┌─────────────────────────────┐
                          │   Public Gateway (MemGate)  │
                          └──────────────┬──────────────┘
                                         │
             ┌───────────────────────────┴───────────────────────────┐
             ▼                                                       │ (Publisher Offline / Stalled)
┌──────────────────────────────────────────────────┐                 ▼
│ Tier 1: Publisher First Delegation               │ ────────► [Fail / Timeout]
│ • Gateway checks if Publisher is online via DHT. │                 │
│ • Streams execution request over P2P protocol    │                 ▼
│   `/membuss/edge/exec/v1`.                       │ ┌──────────────────────────────────────────────────┐
│ • Publisher executes code on their own node.     │ │ Tier 2: Connected Peer Delegation                │
└──────────────────────────────────────────────────┘ │ • Gateway discovers connected peers advertising  │
                                                     │   the `CAP_MEMEDGE` capability.                  │
                                                     │ • Randomly delegates execution via P2P stream.   │
                                                     │ • Peer executes in sandbox and returns result.   │
                                                     └───────────────────────┬──────────────────────────┘
                                                                             │ (No peers / All peers fail)
                                                                             ▼
                                                     ┌──────────────────────────────────────────────────┐
                                                     │ Tier 3: Local Gateway Sandboxed Execution        │
                                                     │ • Fallback execution on the local gateway node.  │
                                                     │ • Pure Go Goja ES6 (JS) or Wazero JIT (WASI).    │
                                                     │ • Enforces strict hard resource limits:          │
                                                     │   - CPU Watchdog: 500ms hard interrupt           │
                                                     │   - Memory Ceiling: 32MB / execution             │
                                                     │   - Panic recovery & logging capture             │
                                                     └──────────────────────────────────────────────────┘
```

---

## 🌟 Core Protocol Architecture

MemEdge bridges content-addressed storage with stateless edge execution:

1. **Content-Addressed Code Assets**:  
   Every edge function is stored as an immutable **Mem ID (MID)** in MemFS, protected by **Reed-Solomon 10+4** erasure coding. Code cannot be tampered with or secretly modified in transit.
2. **3-Tier Fair Compute Scheduling (FCS)**:  
   Compute loads are automatically routed to the content creator first (Tier 1), then to community edge peers (Tier 2), and finally to the gateway itself (Tier 3), preventing gateway resource exhaustion.
3. **Multi-Route Subpath Dispatching**:  
   A single edge function MID can act as an entire microservice with multiple sub-routes (`/products`, `/users/:id`, `/healthz`), matching HTTP methods (`GET`, `POST`, `PUT`, `DELETE`), headers, and query parameters.
4. **Instant Mutable Updates via MemNS**:  
   Developers can attach an optional **MemNS** key to their function MID, allowing seamless code updates (`memns://alice/api`) without breaking consumer URLs.

---

## 🚀 Supported Runtimes

| Feature | JavaScript / TypeScript | WebAssembly (WASI) |
|---|---|---|
| **Engine** | **Goja** (Pure Go ECMAScript 5.1+ / ES6) | **Wazero** (Pure Go WASI Snapshot Preview 1 JIT) |
| **Dependencies** | Pure Go (0 CGo dependencies) | Pure Go (0 CGo, zero-dependency JIT compiler) |
| **Cold Start** | $< 0.5$ milliseconds | $\sim 50–150$ microseconds (warm cache) |
| **Languages** | JavaScript, TypeScript (transpiled), ES Modules | Go (`wasip1`), Rust (`wasm32-wasip1`), TinyGo, C/C++ |
| **I/O Interface** | `handler(req)` invocation + `console` capture | `stdin`/`stdout` JSON stream + CGI Environment |
| **Memory Isolation** | Sandboxed Goja Runtime per execution | Wazero Memory Isolation & Host Trap Protections |

---

## 📜 Writing Edge Functions

### 1. JavaScript Handler API

In JavaScript, functions export a default `handler(req)` function. The `req` object provides full access to the HTTP request context:

#### Request Object API (`req`)
* `req.method`: HTTP method (`"GET"`, `"POST"`, `"PUT"`, `"DELETE"`, etc.)
* `req.path`: Clean subpath relative to the function root (e.g. `"/products/42"`)
* `req.url`: Full original invocation URL
* `req.query`: Key-value map of URL query parameters (e.g. `{ "min_price": "50" }`)
* `req.headers`: Key-value map of incoming HTTP headers
* `req.body`: Raw request payload as a string
* `req.json()`: Helper method that automatically parses `req.body` into a JavaScript object (throws on malformed JSON)
* `req.client_ip`: Remote caller IP address

```javascript
// api/converter.js - Currency FX REST API
export default function handler(req) {
    let payload = {};
    if (req.method === "POST" && req.body) {
        try {
            payload = req.json();
        } catch (e) {
            return {
                status: 400,
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ error: "Invalid JSON body" })
            };
        }
    }

    const rates = { USD: 1.0, EUR: 0.92, GBP: 0.79, JPY: 154.20 };
    const amount = parseFloat(req.query.amount || payload.amount || "100");
    const from = (req.query.from || payload.from || "USD").toUpperCase();
    const to = (req.query.to || payload.to || "EUR").toUpperCase();

    if (!rates[from] || !rates[to]) {
        return { status: 400, body: JSON.stringify({ error: "Unsupported currency" }) };
    }

    const result = (amount / rates[from]) * rates[to];
    console.log(`Converted ${amount} ${from} -> ${result.toFixed(2)} ${to}`);

    return {
        status: 200,
        headers: { "Content-Type": "application/json", "X-Powered-By": "MemEdge" },
        body: JSON.stringify({
            from,
            to,
            original_amount: amount,
            converted_amount: parseFloat(result.toFixed(4)),
            rate: parseFloat((rates[to] / rates[from]).toFixed(6)),
            converted_at: new Date().toISOString()
        }, null, 2)
    };
}
```

---

### 2. Multi-Route REST API Microservice (JavaScript)

You can route multiple endpoints and HTTP methods inside a single function MID:

```javascript
// api/router.js - Multi-Route REST Microservice
export default function handler(req) {
    const { method, path, query } = req;

    const products = [
        { id: 1, name: "Decentralized Storage Node", price: 299, stock: 12 },
        { id: 2, name: "MemEdge Compute Credit", price: 49, stock: 500 },
        { id: 3, name: "MemNS Premium Domain", price: 99, stock: 8 }
    ];

    // 1. Health check & route catalog: GET / or GET /healthz
    if ((path === "/" || path === "/healthz") && method === "GET") {
        return json(200, {
            status: "healthy",
            service: "E-Commerce Edge Microservice",
            version: "v2.4.0",
            routes: ["GET /healthz", "GET /products", "GET /products/:id", "POST /products", "GET /stats"]
        });
    }

    // 2. Query parameter filtering: GET /products?min_price=50
    if (path === "/products" && method === "GET") {
        let result = products;
        if (query.min_price) {
            const min = parseFloat(query.min_price);
            result = result.filter(p => p.price >= min);
        }
        return json(200, { total: result.length, products: result });
    }

    // 3. Dynamic path parameter matching: GET /products/:id
    const match = path.match(/^\/products\/(\d+)$/);
    if (match && method === "GET") {
        const id = parseInt(match[1]);
        const item = products.find(p => p.id === id);
        if (!item) return json(404, { error: `Product #${id} not found` });
        return json(200, { product: item });
    }

    // 4. Create resource: POST /products
    if (path === "/products" && method === "POST") {
        try {
            const body = req.json();
            if (!body.name || !body.price) {
                return json(400, { error: "Fields 'name' and 'price' are required" });
            }
            const newProduct = {
                id: products.length + 1,
                name: body.name,
                price: parseFloat(body.price),
                stock: parseInt(body.stock || "10")
            };
            return json(201, { message: "Product created", product: newProduct });
        } catch (e) {
            return json(400, { error: "Malformed JSON payload" });
        }
    }

    // 5. System stats: GET /stats
    if (path === "/stats" && method === "GET") {
        return json(200, {
            client_ip: req.client_ip,
            timestamp: new Date().toISOString(),
            headers_count: Object.keys(req.headers || {}).length,
            runtime: "MemEdge-Goja"
        });
    }

    return json(404, { error: "Route Not Found", requested_path: path, method });
}

function json(status, data) {
    return {
        status: status,
        headers: { "Content-Type": "application/json", "X-Edge-Router": "MemEdge-MultiRoute-v2" },
        body: JSON.stringify(data, null, 2)
    };
}
```

---

### 3. Go WebAssembly (WASI) Edge Functions

Write native Go code and compile to WebAssembly using standard Go tooling. The function reads the JSON request payload from `os.Stdin` and writes the JSON response to `os.Stdout`:

```go
// main.go - Go WASI Router Microservice
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

	// Clean path prefix if invoked via gateway
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
		"Content-Type": "application/json",
		"X-Engine":     "MemEdge-Wazero-WASI",
	}

	switch {
	case path == "/" || path == "/healthz":
		res.Status = 200
		res.Body = renderJSON(map[string]any{
			"status":  "healthy",
			"service": "Go WASI Multi-Route Microservice",
			"time":    time.Now().UTC().Format(time.RFC3339),
			"routes":  []string{"GET /healthz", "GET /api/v1/calc"},
		})

	case path == "/api/v1/calc" && req.Method == "GET":
		op := req.Query["op"]
		a, _ := strconv.ParseFloat(req.Query["a"], 64)
		b, _ := strconv.ParseFloat(req.Query["b"], 64)

		var result float64
		switch op {
		case "mul":
			result = a * b
		case "div":
			if b != 0 { result = a / b }
		default:
			result = a + b
		}

		res.Status = 200
		res.Body = renderJSON(map[string]any{
			"operation": op,
			"a": a,
			"b": b,
			"result": result,
		})

	default:
		res.Status = 404
		res.Body = renderJSON(map[string]any{"error": "Endpoint not found", "path": path})
	}

	_ = json.NewEncoder(os.Stdout).Encode(res)
}

func renderJSON(data any) string {
	b, _ := json.MarshalIndent(data, "", "  ")
	return string(b)
}
```

#### Compiling Go to WASI
```bash
GOOS=wasip1 GOARCH=wasm go build -o router.wasm main.go
```

---

## 🌐 HTTP Invocation & Subpath Routing

Execute any deployed edge function directly through the public gateway using the `?exec=true` query parameter or `X-MemEdge: run` header:

```bash
# 1. Root invocation
curl "http://localhost:8080/mem/<FUNCTION_MID>?exec=true"

# 2. Subpath routing
curl "http://localhost:8080/mem/<FUNCTION_MID>/products?exec=true"

# 3. Dynamic route with query parameters
curl "http://localhost:8080/mem/<FUNCTION_MID>/products/2?exec=true"

# 4. POST request with JSON body
curl -X POST "http://localhost:8080/mem/<FUNCTION_MID>/products?exec=true" \
     -H "Content-Type: application/json" \
     -d '{"name":"Decentralized GPU Compute","price":499}'

# 5. MemNS mutable endpoint invocation
curl "http://localhost:8080/memns/alice/products?exec=true"
```

### Gateway Response Headers
Every edge execution response contains execution metadata headers:
* `X-Membuss-Edge: executed`: Confirms response was computed dynamically
* `X-Membuss-Edge-Tier: 1 | 2 | 3`: Identifies which tier executed the function (`publisher`, `peer`, or `gateway`)
* `X-Membuss-Edge-Duration: 0.42ms`: Total execution time inside the sandbox runtime

---

## 🖥️ Web Explorer Edge Studio (`/edge`)

The built-in Web Explorer provides a full serverless management cloud environment:

```
 ┌─────────────────────────────────────────────────────────────────────────────┐
 │ 🚀 Deploy Function    │ 📦 Deployed Functions (4) │ ⚡ Test & Playground     │
 ├───────────────────────┴───────────────────────────┴─────────────────────────┤
 │  Function Template: [ Multi-Route REST Microservice (JavaScript)    ▼ ]     │
 │  Name: [ api/router.js          ]   Runtime: (•) JavaScript   ( ) WASI      │
 │                                                                             │
 │  ┌───────────────────────────────────────────────────────────────────────┐  │
 │  │ 1 | // MemEdge Multi-Route REST API Microservice                      │  │
 │  │ 2 | export default function handler(req) {                            │  │
 │  │ 3 |     const { method, path, query } = req;                          │  │
 │  │ 4 |     if (path === "/healthz") return json(200, { status: "ok" });  │  │
 │  │ 5 | }                                                                 │  │
 │  └───────────────────────────────────────────────────────────────────────┘  │
 │                                                                             │
 │  [ ] Attach MemNS Pointer (Optional)                                        │
 │  Keyring Name: [ production-api        ]  TTL: [ 86400  ]                   │
 │                                                                             │
 │  [ ✔ Validate Syntax ]                  [ 🚀 Publish & Deploy Function ]    │
 └─────────────────────────────────────────────────────────────────────────────┘
```

1. **Deploy Tab**: Code editor with built-in templates (Hello, Currency Converter, Token Auth, Multi-Route REST API), real-time syntax validator, optional MemNS key binding, and post-deploy copyable URLs and cURL snippets.
2. **Deployed Functions Tab**: Inventory of all deployed edge functions on the node with direct links to DAG structures, runtime indicators, and 1-click test triggers.
3. **Test & Playground Tab**: Configurable HTTP request builder (Method, Subpath, Headers, Query Params, Body) and response inspector with 3-tier execution badges, duration tracking, and console log captures.

---

## 💻 CLI Commands

MemEdge is fully accessible from the `membuss` CLI:

```bash
# Validate code syntax before deployment
membuss edge validate examples/edge/js/router.js
membuss edge validate examples/edge/go-router/router.wasm

# Test run a local file or deployed MID
membuss edge run examples/edge/js/router.js --path /products --method GET --query min_price=50
membuss edge run <FUNCTION_MID> --path /healthz

# Build a Go project into a WASI WebAssembly binary
membuss edge build ./examples/edge/go-router -o router.wasm

# Check local edge compute statistics and runtime health
membuss edge status
```

---

## ⚙️ Configuration Reference (`membuss.yaml`)

Node operators can configure compute policies and sandboxing limits in `membuss.yaml`:

```yaml
edge_compute:
  enabled: true                 # Enable or disable edge compute on this node
  mode: "community"             # "community" (3-tier), "publisher_only" (tier 1), or "off"
  max_execution_time: 500ms     # Hard CPU watchdog limit per execution
  max_memory_mb: 32             # Max RAM allocated per function VM
  max_concurrent_tasks: 16      # Max simultaneous execution workers
  cache_capacity: 256           # LRU cache capacity for compiled bytecode
```
