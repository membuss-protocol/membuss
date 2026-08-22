<script lang="ts">
	import { onMount } from 'svelte';
	import { apiFetch } from '$lib/api';
	import { toast } from '$lib/toast';
	import { base } from '$app/paths';
	import Icon from '@iconify/svelte';

	interface Key {
		name: string;
		type: string;
		pubkey: string;
		memns_name: string;
	}

	interface EdgeStats {
		enabled: boolean;
		mode: string;
		total_executions: number;
		total_errors: number;
		avg_duration_ms: number;
		active_tasks: number;
		max_concurrency: number;
	}

	interface DeployedFunction {
		mid: string;
		name: string;
		runtime: string;
		size: number;
		memns_name?: string;
		memns_key?: string;
		gateway_url: string;
		memns_url?: string;
		created_at: string;
	}

	interface EdgeResponse {
		status: number;
		headers: Record<string, string>;
		body: string;
		logs?: string[];
		duration_ms: number;
		tier: string;
		error?: string;
	}

	const templates = [
		{
			id: 'hello-js',
			name: 'Hello World (JavaScript)',
			runtime: 'js',
			funcName: 'api/hello.js',
			method: 'GET',
			query: [{ key: 'name', value: 'Explorer' }],
			headers: [{ key: 'Accept', value: 'application/json' }],
			body: '',
			code: `// MemEdge JavaScript Edge Function
export default function handler(req) {
    const user = req.query.name || "Membuss User";
    console.log("Processing edge request for:", user);

    return {
        status: 200,
        headers: {
            "Content-Type": "application/json",
            "X-Powered-By": "Membuss-MemEdge"
        },
        body: JSON.stringify({
            message: "Hello " + user + " from the P2P edge!",
            timestamp: new Date().toISOString(),
            client_ip: req.client_ip
        }, null, 2)
    };
}`
		},
		{
			id: 'calc-js',
			name: 'Currency FX & Math REST API (JavaScript)',
			runtime: 'js',
			funcName: 'api/converter.js',
			method: 'POST',
			query: [],
			headers: [{ key: 'Content-Type', value: 'application/json' }],
			body: JSON.stringify({ amount: 150, from: 'USD', to: 'EUR' }, null, 2),
			code: `// REST API with JSON Body Parsing
export default function handler(req) {
    let payload = {};
    if (req.method === "POST" && req.body) {
        try {
            payload = req.json();
        } catch(e) {
            return { status: 400, body: JSON.stringify({ error: "Invalid JSON body" }) };
        }
    }

    const rates = { USD: 1.0, EUR: 0.92, GBP: 0.79, JPY: 154.20, BTC: 0.0000105 };
    const amount = parseFloat(req.query.amount || payload.amount || "100");
    const from = (req.query.from || payload.from || "USD").toUpperCase();
    const to = (req.query.to || payload.to || "EUR").toUpperCase();

    if (!rates[from] || !rates[to]) {
        return { status: 400, body: JSON.stringify({ error: "Unsupported currency" }) };
    }

    const result = (amount / rates[from]) * rates[to];
    console.log("Converted " + amount + " " + from + " -> " + result.toFixed(4) + " " + to);

    return {
        status: 200,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
            from,
            to,
            original_amount: amount,
            converted_amount: parseFloat(result.toFixed(4)),
            rate: parseFloat((rates[to] / rates[from]).toFixed(6)),
            converted_at: new Date().toISOString()
        }, null, 2)
    };
}`
		},
		{
			id: 'auth-js',
			name: 'Bearer Token Validator (JavaScript)',
			runtime: 'js',
			funcName: 'api/auth.js',
			method: 'GET',
			query: [],
			headers: [{ key: 'Authorization', value: 'Bearer membuss-secret-token-2026' }],
			body: '',
			code: `// Edge Authentication & Token Inspection
export default function handler(req) {
    const authHeader = req.headers["Authorization"] || "";
    if (!authHeader.startsWith("Bearer ")) {
        return {
            status: 401,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ error: "Missing or invalid Bearer token" })
        };
    }

    const token = authHeader.substring(7);
    console.log("Validating token signature on the edge...");

    return {
        status: 200,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
            authorized: true,
            subject: "user_7f8a92b",
            token_prefix: token.substring(0, 10) + "...",
            scope: ["storage:read", "storage:write", "edge:exec"]
        }, null, 2)
    };
}`
		},
		{
			id: 'router-js',
			name: 'Multi-Route REST Microservice (JavaScript)',
			runtime: 'js',
			funcName: 'api/router.js',
			method: 'GET',
			query: [{ key: 'min_price', value: '50' }],
			headers: [{ key: 'Accept', value: 'application/json' }],
			body: '',
			code: `// MemEdge Multi-Route REST API Microservice
export default function handler(req) {
    const { method, path, query } = req;

    const products = [
        { id: 1, name: "Decentralized Storage Node", price: 299, stock: 12 },
        { id: 2, name: "MemEdge Compute Credit", price: 49, stock: 500 },
        { id: 3, name: "MemNS Premium Domain", price: 99, stock: 8 }
    ];

    // GET / or /healthz -> Service Health & Route Manifest
    if ((path === "/" || path === "/healthz") && method === "GET") {
        return json(200, {
            status: "healthy",
            service: "E-Commerce Edge Microservice",
            version: "v2.4.0",
            routes: ["GET /products", "GET /products/:id", "POST /products", "GET /stats"]
        });
    }

    // GET /products -> List products with optional ?min_price filter
    if (path === "/products" && method === "GET") {
        let result = products;
        if (query.min_price) {
            const min = parseFloat(query.min_price);
            result = result.filter(p => p.price >= min);
        }
        return json(200, { total: result.length, products: result });
    }

    // GET /products/:id -> Find product by ID
    const productMatch = path.match(/^\\/products\\/(\\d+)$/);
    if (productMatch && method === "GET") {
        const id = parseInt(productMatch[1]);
        const item = products.find(p => p.id === id);
        if (!item) return json(404, { error: "Product #" + id + " not found" });
        return json(200, { product: item });
    }

    // POST /products -> Create new product
    if (path === "/products" && method === "POST") {
        try {
            const body = req.json();
            if (!body.name || !body.price) {
                return json(400, { error: "Fields 'name' and 'price' required" });
            }
            return json(201, { message: "Product created", product: { id: products.length + 1, ...body } });
        } catch(e) {
            return json(400, { error: "Invalid JSON payload" });
        }
    }

    // GET /stats -> Compute Metadata
    if (path === "/stats" && method === "GET") {
        return json(200, {
            client_ip: req.client_ip,
            timestamp: new Date().toISOString(),
            runtime: "MemEdge-Goja"
        });
    }

    return json(404, { error: "Route Not Found", requested_path: path, method: method });
}

function json(status, data) {
    return {
        status: status,
        headers: { "Content-Type": "application/json", "X-Edge-Router": "MemEdge-v2" },
        body: JSON.stringify(data, null, 2)
    };
}`
		},
		{
			id: 'wasi-go-router',
			name: 'Go WASI Multi-Route Microservice (Go)',
			runtime: 'wasm',
			funcName: 'router.wasm',
			method: 'GET',
			query: [],
			headers: [{ key: 'Accept', value: 'application/json' }],
			body: '',
			code: `// Compile with: GOOS=wasip1 GOARCH=wasm go build -o router.wasm main.go
package main

import (
	"encoding/json"
	"os"
	"time"
)

type EdgeRequest struct {
	Method   string            \`json:"method"\`
	Path     string            \`json:"path"\`
	Query    map[string]string \`json:"query"\`
	Headers  map[string]string \`json:"headers"\`
	Body     string            \`json:"body"\`
	ClientIP string            \`json:"client_ip"\`
}

type EdgeResponse struct {
	Status  int               \`json:"status"\`
	Headers map[string]string \`json:"headers"\`
	Body    string            \`json:"body"\`
}

func main() {
	var req EdgeRequest
	_ = json.NewDecoder(os.Stdin).Decode(&req)

	res := EdgeResponse{
		Status: 200,
		Headers: map[string]string{
			"Content-Type":  "application/json",
			"X-WASI-Engine": "Wazero-ZeroCGO",
		},
	}

	payload, _ := json.MarshalIndent(map[string]any{
		"status":    "healthy",
		"service":   "Go WASI Serverless Microservice",
		"runtime":   "Wazero WASI",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"method":    req.Method,
		"path":      req.Path,
	}, "", "  ")

	res.Body = string(payload)
	_ = json.NewEncoder(os.Stdout).Encode(res)
}`
		},
		{
			id: 'wasi-rust-echo',
			name: 'Rust WASI Microservice (Rust)',
			runtime: 'wasm',
			funcName: 'echo.wasm',
			method: 'POST',
			query: [],
			headers: [{ key: 'Content-Type', value: 'application/json' }],
			body: '{\n  "message": "Hello Rust WASI on MemEdge!"\n}',
			code: `// Compile with: cargo build --target wasm32-wasip1 --release
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::io::{self, Read, Write};

#[derive(Deserialize)]
struct EdgeRequest {
    method: String,
    path: String,
    body: Option<String>,
}

#[derive(Serialize)]
struct EdgeResponse {
    status: u16,
    headers: HashMap<String, String>,
    body: String,
}

fn main() -> io::Result<()> {
    let mut input = String::new();
    io::stdin().read_to_string(&mut input)?;

    let req: EdgeRequest = serde_json::from_str(&input).unwrap_or(EdgeRequest {
        method: "GET".into(),
        path: "/".into(),
        body: None,
    });

    let mut headers = HashMap::new();
    headers.insert("Content-Type".into(), "application/json".into());
    headers.insert("X-WASI-Lang".into(), "Rust".into());

    let res = EdgeResponse {
        status: 200,
        headers,
        body: format!(r#"{{"engine":"Rust-WASI","method":"{}","path":"{}"}}"#, req.method, req.path),
    };

    let out = serde_json::to_string_pretty(&res).unwrap();
    io::stdout().write_all(out.as_bytes())?;
    Ok(())
}`
		}
	];

	// Studio Navigation Tab
	let studioTab = $state<'deploy' | 'functions' | 'test'>('deploy');

	// Keys & Stats
	let keys = $state<Key[]>([]);
	let stats = $state<EdgeStats | null>(null);
	let deployedFunctions = $state<DeployedFunction[]>([]);
	let loadingFunctions = $state(false);

	// Deploy State
	let deployName = $state('api/hello.js');
	let deployRuntime = $state<'js' | 'wasm'>('js');
	let deployCode = $state(templates[0].code);
	let attachMemNS = $state(false); // Optional toggle
	let deployKey = $state('');
	let deployTTL = $state(86400);
	let deploying = $state(false);
	let validatingDeploy = $state(false);
	let lastDeployed = $state<DeployedFunction | null>(null);

	// WASM Upload & Drag-and-Drop state
	let wasmFileName = $state('');
	let wasmFileSize = $state(0);
	let wasmDragOver = $state(false);

	function handleWasmFileUpload(file: File) {
		if (!file) return;
		if (!file.name.endsWith('.wasm')) {
			toast.error('Please select a compiled .wasm binary file');
			return;
		}
		wasmFileName = file.name;
		wasmFileSize = file.size;
		deployName = file.name;
		deployRuntime = 'wasm';

		const reader = new FileReader();
		reader.onload = (e) => {
			const arrayBuffer = e.target?.result as ArrayBuffer;
			if (arrayBuffer) {
				const bytes = new Uint8Array(arrayBuffer);
				let binary = '';
				for (let i = 0; i < bytes.byteLength; i++) {
					binary += String.fromCharCode(bytes[i]);
				}
				deployCode = btoa(binary);
				toast.success(`Loaded "${file.name}" (${(file.size / 1024).toFixed(1)} KB)`);
			}
		};
		reader.readAsArrayBuffer(file);
	}

	// Quick Key Gen modal state inside deploy
	let showNewKeyModal = $state(false);
	let newKeyNameInput = $state('');
	let generatingKey = $state(false);

	// Rebind MemNS modal state
	let bindModalFunction = $state<DeployedFunction | null>(null);
	let selectedBindKey = $state('');
	let bindingKey = $state(false);

	// Test & Playground State
	type TestTargetType = 'deployed' | 'memns' | 'mid' | 'scratch';
	let testTargetType = $state<TestTargetType>('deployed');
	let testTargetMID = $state('');
	let testTargetMemNS = $state('');
	let testSelectedFunction = $state<DeployedFunction | null>(null);
	let testPath = $state('/');
	let testMethod = $state('GET');
	let testCode = $state(templates[0].code);
	let testRuntime = $state<'js' | 'wasm'>('js');
	let testBody = $state('');
	let testQueryParams = $state<{ key: string; value: string }[]>([]);
	let testHeaders = $state<{ key: string; value: string }[]>([]);
	const PUBLIC_GATEWAY = 'https://gateway.membuss.dpdns.org';

	function getLocalOrigin(): string {
		if (typeof window !== 'undefined' && window.location.origin) {
			return window.location.origin;
		}
		return 'http://localhost:8080';
	}

	let showShareMenu = $state(false);
	let showDeployedShareMenu = $state(false);

	let testing = $state(false);
	let testResponse = $state<EdgeResponse | null>(null);
	let testActiveTab = $state<'body' | 'headers' | 'logs'>('body');

	interface TestHistoryItem {
		id: string;
		time: string;
		targetType: TestTargetType;
		target: string;
		path: string;
		method: string;
		status: number;
		duration_ms: number;
		tier: string;
	}
	let testHistory = $state<TestHistoryItem[]>([]);

	let liveGatewayURL = $derived.by(() => {
		let basePath = '';
		if (testTargetType === 'deployed') {
			if (testSelectedFunction?.memns_name) {
				basePath = `/memns/${testSelectedFunction.memns_name}`;
			} else if (testSelectedFunction?.mid) {
				basePath = `/mem/${testSelectedFunction.mid}`;
			} else if (testTargetMID) {
				basePath = `/mem/${testTargetMID}`;
			}
		} else if (testTargetType === 'memns' && testTargetMemNS) {
			const cleanName = testTargetMemNS.replace(/^memns:\/\//, '');
			basePath = `/memns/${cleanName}`;
		} else if (testTargetType === 'mid' && testTargetMID) {
			basePath = `/mem/${testTargetMID}`;
		} else {
			basePath = `/explorer/edge/scratch`;
		}

		let sub = testPath || '';
		if (sub && !sub.startsWith('/')) sub = '/' + sub;
		if (sub === '/') sub = '';

		const qParams: string[] = ['exec=true'];
		for (const q of testQueryParams) {
			if (q.key.trim()) {
				qParams.push(`${encodeURIComponent(q.key.trim())}=${encodeURIComponent(q.value)}`);
			}
		}

		return `${basePath}${sub}?${qParams.join('&')}`;
	});

	function handlePathInput(newVal: string) {
		if (!newVal) {
			testPath = '/';
			return;
		}
		// If user pastes or types path with query params (e.g. /products?min_price=100&category=vpn)
		if (newVal.includes('?')) {
			const [pathPart, queryPart] = newVal.split('?', 2);
			testPath = pathPart.trim() || '/';
			if (queryPart) {
				const params = new URLSearchParams(queryPart);
				const extracted: { key: string; value: string }[] = [];
				params.forEach((v, k) => {
					if (k !== 'exec') {
						extracted.push({ key: k, value: v });
					}
				});
				if (extracted.length > 0) {
					testQueryParams = extracted;
				}
			}
		} else {
			testPath = newVal;
		}
	}

	function applyRoutePreset(method: string, path: string, query: { key: string; value: string }[] = [], headers: { key: string; value: string }[] = [], body: string = '') {
		testMethod = method;
		testPath = path;
		testQueryParams = query;
		testHeaders = headers.length > 0 ? headers : [];
		testBody = body;
	}

	let detectedRoutes = $state<string[]>([]);
	let discoveringRoutes = $state(false);

	interface RouteSuggestion {
		label: string;
		method: string;
		path: string;
		query?: { key: string; value: string }[];
		headers?: { key: string; value: string }[];
		body?: string;
	}

	let contextualSuggestions = $derived.by<RouteSuggestion[]>(() => {
		// 1. If discovered routes from self-describing manifest exist, use them
		if (detectedRoutes.length > 0) {
			return detectedRoutes.map((r) => {
				const parts = r.trim().split(/\s+/);
				let method = 'GET';
				let path = '/';
				if (parts.length >= 2) {
					method = parts[0].toUpperCase();
					path = parts[1];
				} else if (parts.length === 1) {
					path = parts[0];
				}
				const cleanPath = path.replace(/:([a-zA-Z0-9_]+)/g, '1');
				const isMut = method === 'POST' || method === 'PUT' || method === 'PATCH';
				return {
					label: `${method} ${cleanPath}`,
					method,
					path: cleanPath,
					headers: isMut ? [{ key: 'Content-Type', value: 'application/json' }] : [],
					body: isMut ? JSON.stringify({ name: 'Sample Item', price: 99 }, null, 2) : ''
				};
			});
		}

		// 2. Check the active function's name / scratchpad code for specific context
		const name = (testSelectedFunction?.name || deployName || '').toLowerCase();
		const code = (testTargetType === 'scratch' ? testCode : '').toLowerCase();

		if (name.includes('converter') || name.includes('calc') || code.includes('rates') || code.includes('amount')) {
			return [
				{
					label: '💱 GET /?amount=150&from=USD&to=EUR',
					method: 'GET',
					path: '/',
					query: [
						{ key: 'amount', value: '150' },
						{ key: 'from', value: 'USD' },
						{ key: 'to', value: 'EUR' }
					]
				},
				{
					label: '💱 POST / (JSON FX Payload)',
					method: 'POST',
					path: '/',
					headers: [{ key: 'Content-Type', value: 'application/json' }],
					body: JSON.stringify({ amount: 250, from: 'GBP', to: 'USD' }, null, 2)
				}
			];
		}

		if (name.includes('auth') || code.includes('bearer') || code.includes('authorization')) {
			return [
				{ label: '🔓 GET / (Public / No Token)', method: 'GET', path: '/' },
				{
					label: '🔒 GET / (Authorized Bearer)',
					method: 'GET',
					path: '/',
					headers: [{ key: 'Authorization', value: 'Bearer membuss-secret-token-2026' }]
				}
			];
		}

		if (name.includes('router') || code.includes('/products') || code.includes('/healthz')) {
			return [
				{ label: '🩺 GET /healthz', method: 'GET', path: '/healthz' },
				{ label: '📦 GET /products?min_price=50', method: 'GET', path: '/products', query: [{ key: 'min_price', value: '50' }] },
				{ label: '🎯 GET /products/1', method: 'GET', path: '/products/1' },
				{
					label: '➕ POST /products',
					method: 'POST',
					path: '/products',
					headers: [{ key: 'Content-Type', value: 'application/json' }],
					body: JSON.stringify({ name: 'Decentralized VPN', price: 49 }, null, 2)
				},
				{ label: '📊 GET /stats', method: 'GET', path: '/stats' }
			];
		}

		if (name.includes('hello') || code.includes('name')) {
			return [
				{ label: '⚡ GET /', method: 'GET', path: '/' },
				{ label: '👋 GET /?name=Explorer', method: 'GET', path: '/', query: [{ key: 'name', value: 'Explorer' }] }
			];
		}

		// 3. Generic fallback
		return [
			{ label: '⚡ GET /', method: 'GET', path: '/' },
			{ label: '🩺 GET /healthz', method: 'GET', path: '/healthz' }
		];
	});

	function applySuggestion(s: RouteSuggestion) {
		testMethod = s.method;
		testPath = s.path;
		testQueryParams = s.query || [];
		testHeaders = s.headers || [];
		testBody = s.body || '';
	}

	async function discoverFunctionRoutes(midStr: string, runtime: string) {
		detectedRoutes = [];
		if (!midStr) return;
		discoveringRoutes = true;
		try {
			// Probe the function at GET /healthz to retrieve self-described route manifest
			const res = await apiFetch('/edge/run', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					mid: midStr,
					runtime: runtime,
					method: 'GET',
					path: '/healthz'
				})
			});
			if (res && res.body) {
				try {
					const data = JSON.parse(res.body);
					if (Array.isArray(data.routes) && data.routes.length > 0) {
						detectedRoutes = data.routes;
					}
				} catch (_) {}
			}
		} catch (_) {
		} finally {
			discoveringRoutes = false;
		}
	}

	function onSelectDeployedFunction(funcMID: string) {
		const func = deployedFunctions.find((f) => f.mid === funcMID);
		if (func) {
			testSelectedFunction = func;
			testTargetMID = func.mid;
			testTargetMemNS = func.memns_name ? func.memns_name : '';
			testRuntime = func.runtime as 'js' | 'wasm';
			testPath = '/';
			testQueryParams = [];
			testHeaders = [];
			testBody = '';
			discoverFunctionRoutes(func.mid, func.runtime);
		}
	}

	function selectTemplate(tId: string) {
		const t = templates.find((item) => item.id === tId);
		if (!t) return;
		deployName = t.funcName;
		deployRuntime = t.runtime as 'js' | 'wasm';
		deployCode = t.code;
		lastDeployed = null;
	}

	async function fetchKeys() {
		try {
			const res = await apiFetch('/keyring/list');
			keys = res || [];
			if (keys.length > 0 && !deployKey) {
				deployKey = keys[0].name;
			}
		} catch (_) {}
	}

	async function fetchStats() {
		try {
			const res = await apiFetch('/edge/status');
			if (res && res.enabled !== undefined) {
				stats = res;
			}
		} catch (_) {}
	}

	async function fetchDeployedFunctions() {
		loadingFunctions = true;
		try {
			const res = await apiFetch('/edge/functions');
			deployedFunctions = res || [];
		} catch (_) {
		} finally {
			loadingFunctions = false;
		}
	}

	async function handleValidateCode() {
		validatingDeploy = true;
		try {
			const res = await apiFetch('/edge/run', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					code: deployCode,
					path: deployName,
					runtime: deployRuntime,
					method: 'GET'
				})
			});
			if (res && !res.error) {
				toast.success('✅ Code passed pre-flight validation!');
			} else {
				toast.error(`❌ Validation error: ${res.error || 'Syntax error'}`);
			}
		} catch (err: any) {
			toast.error(`Validation failed: ${err.message}`);
		} finally {
			validatingDeploy = false;
		}
	}

	async function handleDeploy() {
		deploying = true;
		lastDeployed = null;
		try {
			const res = await apiFetch('/edge/deploy', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					name: deployName,
					code: deployCode,
					runtime: deployRuntime,
					key_name: attachMemNS ? deployKey : '',
					ttl: attachMemNS ? deployTTL : 86400,
					seal: true
				})
			});

			lastDeployed = res;
			toast.success(`🎉 Deployed "${deployName}" as ${res.mid.substring(0, 14)}...`);
			fetchDeployedFunctions();
			fetchStats();
		} catch (err: any) {
			toast.error(`Deploy failed: ${err.message}`);
		} finally {
			deploying = false;
		}
	}

	async function handleCreateKey() {
		if (!newKeyNameInput.trim()) return;
		generatingKey = true;
		try {
			await apiFetch('/keyring/gen', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name: newKeyNameInput.trim(), type: 'ed25519' })
			});
			toast.success(`Key "${newKeyNameInput}" created!`);
			await fetchKeys();
			deployKey = newKeyNameInput.trim();
			showNewKeyModal = false;
			newKeyNameInput = '';
		} catch (err: any) {
			toast.error(`Failed to create key: ${err.message}`);
		} finally {
			generatingKey = false;
		}
	}

	async function handleBindMemNS() {
		if (!bindModalFunction || !selectedBindKey) return;
		bindingKey = true;
		try {
			await apiFetch('/edge/bind', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					mid: bindModalFunction.mid,
					key_name: selectedBindKey,
					ttl: 86400
				})
			});
			toast.success(`Bound ${bindModalFunction.name} to MemNS (${selectedBindKey})`);
			bindModalFunction = null;
			fetchDeployedFunctions();
		} catch (err: any) {
			toast.error(`Failed to bind: ${err.message}`);
		} finally {
			bindingKey = false;
		}
	}

	async function handleDeleteFunction(func: DeployedFunction) {
		if (!confirm(`Are you sure you want to delete ${func.name}?`)) return;
		try {
			await apiFetch(`/edge/functions/${func.mid}`, { method: 'DELETE' });
			toast.success(`Removed ${func.name}`);
			fetchDeployedFunctions();
		} catch (err: any) {
			toast.error(`Delete failed: ${err.message}`);
		}
	}

	function openTestPlayground(func?: DeployedFunction) {
		if (func) {
			testTargetType = 'deployed';
			onSelectDeployedFunction(func.mid);
		} else if (lastDeployed) {
			testTargetType = 'deployed';
			onSelectDeployedFunction(lastDeployed.mid);
		}
		studioTab = 'test';
	}

	function addTestQueryParam() {
		testQueryParams = [...testQueryParams, { key: '', value: '' }];
	}

	function removeTestQueryParam(index: number) {
		testQueryParams = testQueryParams.filter((_, i) => i !== index);
	}

	function addTestHeader() {
		testHeaders = [...testHeaders, { key: '', value: '' }];
	}

	function removeTestHeader(index: number) {
		testHeaders = testHeaders.filter((_, i) => i !== index);
	}

	async function handleExecuteTest() {
		testing = true;
		testResponse = null;

		const qMap: Record<string, string> = {};
		for (const q of testQueryParams) {
			if (q.key.trim()) qMap[q.key.trim()] = q.value;
		}

		const hMap: Record<string, string> = {};
		for (const h of testHeaders) {
			if (h.key.trim()) hMap[h.key.trim()] = h.value;
		}

		const payload: any = {
			path: testPath,
			runtime: testRuntime,
			method: testMethod,
			query: qMap,
			headers: hMap,
			body: testBody
		};

		if (testTargetType === 'deployed') {
			if (testTargetMID) payload.mid = testTargetMID;
			if (testSelectedFunction?.memns_name) payload.memns = testSelectedFunction.memns_name;
		} else if (testTargetType === 'memns' && testTargetMemNS) {
			payload.memns = testTargetMemNS.replace(/^memns:\/\//, '');
		} else if (testTargetType === 'mid' && testTargetMID) {
			payload.mid = testTargetMID;
		} else {
			payload.code = testCode;
		}

		try {
			const res = await apiFetch('/edge/run', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(payload)
			});

			testResponse = res;
			if (res.status >= 200 && res.status < 400) {
				toast.success(`Executed in ${res.duration_ms?.toFixed(2) || 0}ms (${res.tier || 'edge'})`);
			} else {
				toast.error(`Edge returned status ${res.status}`);
			}

			// Add to history
			let targetLabel = 'Scratchpad';
			if (testTargetType === 'deployed') {
				targetLabel = testSelectedFunction?.name || (testTargetMID ? testTargetMID.substring(0, 14) + '...' : 'Deployed');
			} else if (testTargetType === 'memns') {
				targetLabel = `memns://${testTargetMemNS.replace(/^memns:\/\//, '')}`;
			} else if (testTargetType === 'mid') {
				targetLabel = testTargetMID.substring(0, 14) + '...';
			}

			testHistory = [
				{
					id: Math.random().toString(36).substring(2, 9),
					time: new Date().toLocaleTimeString(),
					targetType: testTargetType,
					target: targetLabel,
					path: testPath,
					method: testMethod,
					status: res.status,
					duration_ms: res.duration_ms || 0,
					tier: res.tier || 'gateway'
				},
				...testHistory.slice(0, 7)
			];

			fetchStats();
		} catch (err: any) {
			testResponse = {
				status: 500,
				headers: {},
				body: err.message || 'Execution error',
				duration_ms: 0,
				tier: 'local',
				error: err.message
			};
			toast.error('Execution failed');
		} finally {
			testing = false;
		}
	}

	onMount(() => {
		fetchKeys();
		fetchStats();
		fetchDeployedFunctions();
		const interval = setInterval(fetchStats, 5000);
		return () => clearInterval(interval);
	});
</script>

<svelte:head>
	<title>MemEdge Cloud Studio — Membuss Explorer</title>
</svelte:head>

<div class="px-6 md:px-12 py-8 max-w-7xl mx-auto space-y-8 animate-fade-in">
	<!-- Top Navigation Header -->
	<div class="flex flex-col md:flex-row md:items-center justify-between gap-4 pb-6 border-b border-slate-800">
		<div class="flex items-center gap-3">
			<div class="w-11 h-11 rounded-xl bg-cyan-500/10 border border-cyan-500/30 flex items-center justify-center text-cyan-400">
				<Icon icon="ph:lightning-fill" class="w-6 h-6" />
			</div>
			<div>
				<h1 class="text-2xl font-semibold text-slate-100 font-display flex items-center gap-2.5">
					MemEdge Cloud Studio
					<span class="text-xs px-2.5 py-0.5 rounded-full font-mono bg-cyan-950/60 border border-cyan-500/30 text-cyan-300 font-medium">3-Tier FCS</span>
				</h1>
				<p class="text-xs text-slate-400 mt-0.5">Deploy, manage, bind MemNS, and test content-addressed serverless edge functions</p>
			</div>
		</div>

		<!-- Telemetry Bar -->
		{#if stats}
			<div class="flex items-center gap-2">
				<div class="px-3.5 py-2 rounded-xl bg-slate-900/60 border border-slate-800 flex items-center gap-3 shadow-inner">
					<div class="flex items-center gap-2">
						<span class="w-2 h-2 rounded-full {stats.enabled ? 'bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.5)]' : 'bg-rose-400'}"></span>
						<span class="text-xs font-mono text-slate-300 uppercase">{stats.mode}</span>
					</div>
					<div class="h-4 w-[1px] bg-slate-800"></div>
					<div class="text-xs font-mono text-slate-400">
						<span class="text-slate-200 font-semibold">{stats.total_executions}</span> calls
					</div>
					<div class="h-4 w-[1px] bg-slate-800"></div>
					<div class="text-xs font-mono text-slate-400">
						<span class="text-cyan-400 font-semibold">{stats.avg_duration_ms?.toFixed(2) || '0.00'}ms</span> avg
					</div>
				</div>
			</div>
		{/if}
	</div>

	<!-- Navigation Tabs -->
	<div class="flex items-center gap-2 p-1.5 bg-slate-900/60 border border-slate-800 rounded-xl w-fit">
		<button
			onclick={() => (studioTab = 'deploy')}
			class="px-4 py-2 rounded-lg text-xs font-mono font-medium flex items-center gap-2 transition-all {studioTab === 'deploy'
				? 'bg-cyan-500 text-slate-950 shadow-[0_0_12px_rgba(6,182,212,0.3)] font-bold'
				: 'text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:cloud-arrow-up-fill" class="w-4 h-4" />
			🚀 Deploy Function
		</button>

		<button
			onclick={() => {
				studioTab = 'functions';
				fetchDeployedFunctions();
			}}
			class="px-4 py-2 rounded-lg text-xs font-mono font-medium flex items-center gap-2 transition-all {studioTab === 'functions'
				? 'bg-cyan-500 text-slate-950 shadow-[0_0_12px_rgba(6,182,212,0.3)] font-bold'
				: 'text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:stack-fill" class="w-4 h-4" />
			📦 Deployed Functions ({deployedFunctions.length})
		</button>

		<button
			onclick={() => (studioTab = 'test')}
			class="px-4 py-2 rounded-lg text-xs font-mono font-medium flex items-center gap-2 transition-all {studioTab === 'test'
				? 'bg-cyan-500 text-slate-950 shadow-[0_0_12px_rgba(6,182,212,0.3)] font-bold'
				: 'text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:play-circle-fill" class="w-4 h-4" />
			⚡ Test & Playground
		</button>
	</div>

	<!-- ========================================================================= -->
	<!-- TAB 1: DEPLOY & PUBLISH STUDIO -->
	<!-- ========================================================================= -->
	{#if studioTab === 'deploy'}
		<div class="space-y-6 animate-fade-in">
			<div class="grid grid-cols-1 lg:grid-cols-12 gap-6">
				<!-- Left: Code Editor (7 Cols) -->
				<div class="lg:col-span-7 space-y-4 flex flex-col">
					<!-- Preset & Config Bar -->
					<div class="p-4 rounded-xl bg-slate-900/60 border border-slate-800 flex flex-wrap items-center justify-between gap-3">
						<div class="flex items-center gap-2">
							<span class="text-xs font-mono text-slate-400">Template:</span>
							<select
								onchange={(e) => selectTemplate(e.currentTarget.value)}
								class="bg-slate-950 border border-slate-700 text-slate-200 text-xs rounded-lg px-3 py-1.5 font-mono focus:outline-none focus:border-cyan-400"
							>
								{#each templates as t}
									<option value={t.id}>{t.name}</option>
								{/each}
							</select>
						</div>

						<div class="flex items-center gap-2">
							<span class="text-xs font-mono text-slate-400">Runtime:</span>
							<select
								bind:value={deployRuntime}
								class="bg-slate-950 border border-slate-700 text-cyan-300 font-bold text-xs rounded-lg px-2.5 py-1.5 font-mono focus:outline-none focus:border-cyan-400"
							>
								<option value="js">JavaScript (Goja)</option>
								<option value="wasm">WebAssembly (WASI)</option>
							</select>
						</div>
					</div>

					<!-- Code Editor / WASM Binary Upload Card -->
					<div class="rounded-xl bg-slate-950 border border-slate-800 overflow-hidden flex flex-col flex-grow shadow-2xl">
						<div class="px-4 py-2.5 bg-slate-900/80 border-b border-slate-800 flex items-center justify-between">
							<div class="flex items-center gap-2">
								<span class="w-2.5 h-2.5 rounded-full bg-slate-700"></span>
								<span class="w-2.5 h-2.5 rounded-full bg-slate-700"></span>
								<span class="w-2.5 h-2.5 rounded-full bg-slate-700"></span>
								<input
									type="text"
									bind:value={deployName}
									class="bg-transparent text-xs font-mono text-cyan-300 ml-2 focus:outline-none border-b border-transparent focus:border-cyan-400"
									placeholder={deployRuntime === 'wasm' ? 'router.wasm' : 'api/my-func.js'}
								/>
							</div>
							<div class="text-[11px] font-mono text-slate-500">
								{deployRuntime === 'wasm' ? 'Wazero Pure-Go WASI' : 'Goja ECMAScript 5.1+'}
							</div>
						</div>

						{#if deployRuntime === 'wasm'}
							<!-- WASM Binary Drag-and-Drop Uploader -->
							<div class="p-4 space-y-4">
								<div
									role="button"
									tabindex="0"
									class="p-6 rounded-xl bg-slate-950/80 border-2 border-dashed {wasmDragOver
										? 'border-cyan-400 bg-cyan-950/20'
										: 'border-slate-800 hover:border-cyan-500/40'} flex flex-col items-center justify-center text-center gap-3 transition-all cursor-pointer relative group"
									ondragover={(e) => {
										e.preventDefault();
										wasmDragOver = true;
									}}
									ondragleave={() => (wasmDragOver = false)}
									ondrop={(e) => {
										e.preventDefault();
										wasmDragOver = false;
										if (e.dataTransfer?.files?.[0]) handleWasmFileUpload(e.dataTransfer.files[0]);
									}}
								>
									<input
										type="file"
										accept=".wasm"
										aria-label="Upload compiled WASM binary"
										onchange={(e) => {
											const target = e.target as HTMLInputElement;
											if (target.files?.[0]) handleWasmFileUpload(target.files[0]);
										}}
										class="absolute inset-0 opacity-0 cursor-pointer w-full h-full"
									/>
									<div class="p-3 rounded-2xl bg-cyan-500/10 border border-cyan-500/30 text-cyan-400 group-hover:scale-105 transition-transform">
										<Icon icon="ph:file-wasm" class="w-8 h-8" />
									</div>
									<div class="space-y-1">
										<p class="text-xs font-mono font-bold text-slate-200">
											{wasmFileName ? wasmFileName : 'Drag and drop compiled .wasm binary here'}
										</p>
										<p class="text-[11px] font-mono text-slate-400">
											{wasmFileName ? `${(wasmFileSize / 1024).toFixed(1)} KB loaded and ready to deploy` : 'or click to browse files (compiled with TinyGo, Go wasip1, or Rust)'}
										</p>
									</div>
									{#if wasmFileName}
										<span class="px-2.5 py-1 rounded-md text-[10px] font-mono bg-emerald-950 border border-emerald-500/30 text-emerald-400 font-semibold">
											✅ WASM Bytecode Validated & Ready
										</span>
									{/if}
								</div>

								<!-- Compilation Guides -->
								<div class="p-3.5 rounded-xl bg-slate-900/60 border border-slate-800 space-y-2 text-xs font-mono">
									<span class="text-slate-400 font-semibold flex items-center gap-1.5">
										<Icon icon="ph:terminal-window" class="w-3.5 h-3.5 text-cyan-400" />
										1-Line Build Commands:
									</span>
									<div class="space-y-1 text-[11px] text-slate-300">
										<div class="p-2 rounded bg-slate-950 border border-slate-800 text-cyan-300 select-all">
											GOOS=wasip1 GOARCH=wasm go build -o router.wasm main.go
										</div>
										<div class="p-2 rounded bg-slate-950 border border-slate-800 text-emerald-300 select-all">
											cargo build --target wasm32-wasip1 --release
										</div>
									</div>
								</div>

								<!-- Source Code Reference -->
								<div class="space-y-1">
									<span class="text-[11px] font-mono text-slate-400">Source Reference ({deployName}):</span>
									<textarea
										bind:value={deployCode}
										rows="10"
										class="w-full p-3 bg-slate-950 text-cyan-100 font-mono text-xs leading-relaxed resize-y focus:outline-none border border-slate-800 rounded-lg"
										spellcheck="false"
									></textarea>
								</div>
							</div>
						{:else}
							<!-- JavaScript Code Editor -->
							<textarea
								bind:value={deployCode}
								rows="18"
								class="w-full p-4 bg-slate-950 text-cyan-100 font-mono text-xs leading-relaxed resize-y focus:outline-none selection:bg-cyan-500/30 selection:text-white"
								spellcheck="false"
							></textarea>
						{/if}

						<div class="px-4 py-3 bg-slate-900/80 border-t border-slate-800 flex items-center justify-between gap-3">
							<button
								onclick={handleValidateCode}
								disabled={validatingDeploy || deploying}
								class="px-3.5 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-mono flex items-center gap-1.5 transition-colors disabled:opacity-50"
							>
								{#if validatingDeploy}
									<Icon icon="ph:spinner" class="w-3.5 h-3.5 animate-spin text-cyan-400" />
								{:else}
									<Icon icon="ph:check-circle" class="w-3.5 h-3.5 text-emerald-400" />
								{/if}
								Validate Syntax
							</button>
						</div>
					</div>
				</div>

				<!-- Right: MemNS Link & Deployment Card (5 Cols) -->
				<div class="lg:col-span-5 space-y-4 flex flex-col">
					<!-- MemNS Binding Configuration (Optional) -->
					<div class="p-5 rounded-xl bg-slate-900/60 border border-slate-800 space-y-4">
						<div class="flex items-center justify-between">
							<label class="flex items-center gap-2 cursor-pointer select-none">
								<input
									type="checkbox"
									bind:checked={attachMemNS}
									class="w-4 h-4 rounded bg-slate-950 border-slate-700 text-cyan-500 focus:ring-0 focus:ring-offset-0 cursor-pointer"
								/>
								<span class="text-xs font-mono uppercase tracking-wider text-slate-200 font-semibold flex items-center gap-1.5">
									<Icon icon="ph:identification-card-fill" class="w-4 h-4 text-cyan-400" />
									Attach MemNS Pointer (Optional)
								</span>
							</label>

							{#if attachMemNS}
								<button
									onclick={() => (showNewKeyModal = true)}
									class="text-[11px] font-mono text-cyan-400 hover:text-cyan-300 flex items-center gap-1"
								>
									+ New Key
								</button>
							{/if}
						</div>

						{#if !attachMemNS}
							<div class="p-3 rounded-lg bg-slate-950/60 border border-slate-800/80 text-xs font-mono text-slate-400 flex items-start gap-2.5">
								<Icon icon="ph:fingerprint-simple" class="w-4 h-4 text-cyan-400 flex-shrink-0 mt-0.5" />
								<div>
									<span class="text-slate-300 font-medium">Pure Content-Addressed:</span>
									Function will be immutable and identified directly by its cryptographic MID hash (<code class="text-cyan-300">mem1...</code>).
								</div>
							</div>
						{:else}
							<div class="space-y-3 pt-1 animate-fade-in">
								<p class="text-xs text-slate-400">Bind to a mutable Ed25519 identity key so callers can reach updated versions without changing URLs.</p>
								<div>
									<label for="select-key" class="text-[11px] font-mono text-slate-400 block mb-1">Identity Key:</label>
									<select
										id="select-key"
										bind:value={deployKey}
										class="w-full bg-slate-950 border border-slate-700 text-slate-200 text-xs rounded-lg px-3 py-2 font-mono focus:outline-none focus:border-cyan-400"
									>
										{#each keys as k}
											<option value={k.name}>{k.name} ({k.memns_name.substring(0, 16)}...)</option>
										{/each}
									</select>
								</div>

								<div>
									<label for="select-ttl" class="text-[11px] font-mono text-slate-400 block mb-1">Record TTL (seconds):</label>
									<input
										id="select-ttl"
										type="number"
										bind:value={deployTTL}
										class="w-full bg-slate-950 border border-slate-700 text-slate-200 text-xs rounded-lg px-3 py-2 font-mono focus:outline-none focus:border-cyan-400"
									/>
								</div>
							</div>
						{/if}
					</div>

					<!-- Deploy Action Button -->
					<div class="p-5 rounded-xl bg-slate-900/60 border border-slate-800 space-y-4">
						<button
							onclick={handleDeploy}
							disabled={deploying || validatingDeploy}
							class="w-full py-3 rounded-xl bg-cyan-500 hover:bg-cyan-400 text-slate-950 font-bold font-mono text-sm flex items-center justify-center gap-2.5 transition-all shadow-[0_0_20px_rgba(6,182,212,0.4)] disabled:opacity-50"
						>
							{#if deploying}
								<Icon icon="ph:spinner" class="w-5 h-5 animate-spin" />
								Deploying to Membuss DAG...
							{:else}
								<Icon icon="ph:rocket-launch-fill" class="w-5 h-5" />
								Deploy & Publish to Network
							{/if}
						</button>
					</div>

					<!-- Last Deployed Output Confirmation -->
					{#if lastDeployed}
						<div class="p-5 rounded-xl bg-slate-900/90 border border-cyan-500/40 space-y-4 shadow-2xl animate-fade-in">
							<div class="flex items-center justify-between">
								<div class="flex items-center gap-2 text-emerald-400 font-bold text-xs font-mono">
									<Icon icon="ph:check-circle-fill" class="w-4 h-4" />
									DEPLOYED & PUBLISHED
								</div>
								<span class="px-2 py-0.5 rounded text-[10px] font-mono bg-cyan-950 text-cyan-300 border border-cyan-500/30 uppercase font-semibold">
									{lastDeployed.runtime}
								</span>
							</div>

							<!-- 1. Content MID (Primary) -->
							<div class="p-3 bg-slate-950 rounded-lg border border-slate-800 space-y-1.5">
								<div class="flex items-center justify-between text-[11px] font-mono">
									<span class="text-slate-400 font-semibold flex items-center gap-1.5">
										<Icon icon="ph:hash" class="w-3.5 h-3.5 text-cyan-400" />
										CONTENT MID (Content-Addressed)
									</span>
									<button
										onclick={() => {
											navigator.clipboard.writeText(lastDeployed!.mid);
											toast.success('MID copied to clipboard!');
										}}
										class="text-cyan-400 hover:text-cyan-300 text-[10px] flex items-center gap-1"
									>
										<Icon icon="ph:copy" class="w-3 h-3" />
										Copy MID
									</button>
								</div>
								<a href={`${base}/mid/${lastDeployed.mid}`} class="text-xs font-mono text-cyan-300 hover:underline break-all block">
									{lastDeployed.mid}
								</a>
							</div>

							<!-- 2. Direct Execution URLs & Commands -->
							<div class="space-y-2.5 text-xs font-mono">
								<div>
									<div class="flex items-center justify-between text-[10px] text-slate-400 mb-1">
										<span>DIRECT GATEWAY EXECUTION URL:</span>
										<div class="flex items-center gap-2">
											<button
												onclick={() => {
													navigator.clipboard.writeText(getLocalOrigin() + lastDeployed!.gateway_url);
													toast.success('Local Gateway URL copied!');
												}}
												class="text-cyan-400 hover:text-cyan-300 text-[10px] flex items-center gap-1"
												title="Copy Local URL"
											>
												<Icon icon="ph:house" class="w-3 h-3" />
												Local
											</button>
											<button
												onclick={() => {
													navigator.clipboard.writeText(PUBLIC_GATEWAY + lastDeployed!.gateway_url);
													toast.success('Public Gateway URL copied!');
												}}
												class="text-emerald-400 hover:text-emerald-300 text-[10px] flex items-center gap-1"
												title="Copy Public URL"
											>
												<Icon icon="ph:globe-hemisphere-west" class="w-3 h-3" />
												Public
											</button>
										</div>
									</div>
									<code class="text-slate-200 select-all break-all bg-slate-950 p-2 rounded block border border-slate-800 text-[11px]">
										{lastDeployed.gateway_url}
									</code>
								</div>

								<div>
									<div class="flex items-center justify-between text-[10px] text-slate-400 mb-1">
										<span>CURL COMMAND:</span>
										<button
											onclick={() => {
												navigator.clipboard.writeText(`curl "${getLocalOrigin()}${lastDeployed!.gateway_url}"`);
												toast.success('cURL command copied!');
											}}
											class="text-cyan-400 hover:text-cyan-300 text-[10px] flex items-center gap-1"
										>
											<Icon icon="ph:copy" class="w-3 h-3" />
											Copy cURL
										</button>
									</div>
									<code class="text-cyan-300 select-all break-all bg-slate-950 p-2 rounded block border border-slate-800 text-[11px]">
										curl "{getLocalOrigin()}{lastDeployed.gateway_url}"
									</code>
								</div>

								<div>
									<div class="flex items-center justify-between text-[10px] text-slate-400 mb-1">
										<span>CLI COMMAND:</span>
										<button
											onclick={() => {
												navigator.clipboard.writeText(`membuss edge run ${lastDeployed!.mid}`);
												toast.success('CLI command copied!');
											}}
											class="text-cyan-400 hover:text-cyan-300 text-[10px] flex items-center gap-1"
										>
											<Icon icon="ph:copy" class="w-3 h-3" />
											Copy CLI
										</button>
									</div>
									<code class="text-emerald-300 select-all break-all bg-slate-950 p-2 rounded block border border-slate-800 text-[11px]">
										membuss edge run {lastDeployed.mid}
									</code>
								</div>

								{#if lastDeployed.memns_name}
									<div class="pt-1">
										<span class="text-slate-400 block text-[10px] mb-1">BOUND MEMNS URL:</span>
										<code class="text-cyan-300 select-all break-all bg-slate-950 p-2 rounded block border border-slate-800 text-[11px]">
											/memns/{lastDeployed.memns_name}/{lastDeployed.name}?exec=true
										</code>
									</div>
								{/if}
							</div>

							<div class="pt-2">
								<button
									onclick={() => openTestPlayground(lastDeployed!)}
									class="w-full py-2.5 rounded-xl bg-cyan-500 hover:bg-cyan-400 text-slate-950 text-xs font-mono font-bold flex items-center justify-center gap-2 transition-all shadow-[0_0_15px_rgba(6,182,212,0.3)]"
								>
									<Icon icon="ph:play-fill" class="w-4 h-4" />
									Test This Deployed Function Now
								</button>
							</div>
						</div>
					{/if}
				</div>
			</div>
		</div>
	{/if}

	<!-- ========================================================================= -->
	<!-- TAB 2: DEPLOYED FUNCTIONS DASHBOARD -->
	<!-- ========================================================================= -->
	{#if studioTab === 'functions'}
		<div class="space-y-4 animate-fade-in">
			<div class="flex items-center justify-between">
				<h2 class="text-sm font-mono text-slate-300 font-semibold flex items-center gap-2">
					<Icon icon="ph:stack" class="w-4 h-4 text-cyan-400" />
					All Stored Edge Functions on Local Node
				</h2>
				<button
					onclick={fetchDeployedFunctions}
					class="px-3 py-1 rounded-lg bg-slate-800 hover:bg-slate-700 text-xs font-mono text-slate-300 flex items-center gap-1.5"
				>
					<Icon icon="ph:arrows-clockwise" class="w-3.5 h-3.5" />
					Refresh
				</button>
			</div>

			{#if loadingFunctions}
				<div class="p-12 text-center text-slate-500 font-mono text-xs flex flex-col items-center gap-3">
					<Icon icon="ph:spinner" class="w-6 h-6 animate-spin text-cyan-400" />
					Loading deployed functions...
				</div>
			{:else if deployedFunctions.length === 0}
				<div class="p-12 text-center rounded-xl bg-slate-900/40 border border-slate-800 text-slate-500 font-mono text-xs space-y-3">
					<Icon icon="ph:code-block" class="w-8 h-8 mx-auto text-slate-600" />
					<div>No edge functions deployed yet.</div>
					<button
						onclick={() => (studioTab = 'deploy')}
						class="px-4 py-2 rounded-lg bg-cyan-500 text-slate-950 font-bold text-xs font-mono"
					>
						Deploy Your First Edge Function
					</button>
				</div>
			{:else}
				<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
					{#each deployedFunctions as func}
						<div class="p-5 rounded-2xl bg-slate-900/70 border {func.memns_name ? 'border-emerald-500/40 shadow-[0_0_20px_rgba(16,185,129,0.1)]' : 'border-slate-800'} hover:border-cyan-500/50 transition-all flex flex-col justify-between gap-4 shadow-lg group">
							<div class="space-y-3">
								<!-- Header: Function Name & Runtime -->
								<div class="flex items-center justify-between gap-2">
									<span class="text-xs font-mono font-bold text-slate-100 flex items-center gap-2 truncate">
										<Icon icon={func.runtime === 'wasm' ? 'ph:file-wasm' : 'ph:file-js'} class="w-4 h-4 {func.runtime === 'wasm' ? 'text-purple-400' : 'text-cyan-400'}" />
										<span class="truncate">{func.name}</span>
									</span>
									<div class="flex items-center gap-1.5 shrink-0">
										{#if func.memns_name}
											<span class="px-2 py-0.5 rounded text-[9px] font-mono uppercase bg-emerald-950/80 border border-emerald-500/40 text-emerald-400 font-bold flex items-center gap-1">
												<Icon icon="ph:check-circle-fill" class="w-2.5 h-2.5" />
												Bound
											</span>
										{/if}
										<span class="px-2 py-0.5 rounded text-[10px] font-mono uppercase bg-slate-800 text-cyan-300 font-semibold border border-slate-700">
											{func.runtime}
										</span>
									</div>
								</div>

								<!-- 1. Primary: MemNS Address (Prominent at top) -->
								{#if func.memns_name}
									<div class="p-2.5 rounded-xl bg-emerald-950/40 border border-emerald-500/30 space-y-1.5">
										<div class="flex items-center justify-between text-[10px] font-mono text-emerald-400 font-bold uppercase tracking-wider">
											<span class="flex items-center gap-1">
												<Icon icon="ph:identification-card-fill" class="w-3.5 h-3.5" />
												Active MemNS Address
											</span>
											<button
												onclick={() => {
													navigator.clipboard.writeText(`memns://${func.memns_name}`);
													toast.success('MemNS address copied!');
												}}
												class="text-emerald-400 hover:text-emerald-200 text-[10px] flex items-center gap-0.5"
												title="Copy MemNS Name"
											>
												<Icon icon="ph:copy" class="w-3 h-3" />
												Copy
											</button>
										</div>
										<a
											href={`${base}/memns/${func.memns_name}`}
											class="text-xs font-mono font-semibold text-emerald-300 hover:text-emerald-100 hover:underline break-all block"
										>
											memns://{func.memns_name}
										</a>
									</div>
								{:else}
									<div class="p-2.5 rounded-xl bg-slate-950/60 border border-slate-800 space-y-1">
										<div class="flex items-center justify-between text-[10px] font-mono text-slate-500 uppercase tracking-wider font-semibold">
											<span class="flex items-center gap-1">
												<Icon icon="ph:link-break" class="w-3 h-3 text-slate-500" />
												MemNS Pointer
											</span>
											<span class="px-1.5 py-0.5 rounded bg-slate-900 text-slate-400 text-[9px]">Unbound</span>
										</div>
										<div class="text-[11px] font-mono text-slate-400">
											Immutable MID only. Click link to bind mutable key.
										</div>
									</div>
								{/if}

								<!-- 2. Secondary: Immutable Content MID -->
								<div class="p-2.5 rounded-xl bg-slate-950/60 border border-slate-800/80 space-y-1">
									<div class="flex items-center justify-between text-[10px] font-mono text-slate-400">
										<span class="flex items-center gap-1 text-purple-400 font-semibold">
											<Icon icon="ph:fingerprint" class="w-3 h-3" />
											Content MID:
										</span>
										<button
											onclick={() => {
												navigator.clipboard.writeText(func.mid);
												toast.success('MID copied!');
											}}
											class="text-slate-400 hover:text-cyan-300 text-[10px] flex items-center gap-0.5"
											title="Copy Full MID"
										>
											<Icon icon="ph:copy" class="w-3 h-3" />
											Copy
										</button>
									</div>
									<a
										href={`${base}/mid/${func.mid}`}
										class="text-[11px] font-mono text-cyan-300/90 hover:text-cyan-200 hover:underline break-all block"
									>
										{func.mid.substring(0, 16)}...{func.mid.substring(func.mid.length - 8)}
									</a>
								</div>
							</div>

							<!-- Footer Action Toolbar -->
							<div class="pt-3 border-t border-slate-800/80 flex items-center justify-between gap-2">
								<button
									onclick={() => openTestPlayground(func)}
									class="px-3.5 py-1.5 rounded-xl bg-cyan-500/10 hover:bg-cyan-500/20 text-cyan-300 border border-cyan-500/30 text-xs font-mono font-semibold flex items-center gap-1.5 transition-all hover:scale-105 shadow-sm"
								>
									<Icon icon="ph:play-fill" class="w-3.5 h-3.5 text-cyan-400" />
									Test / Run
								</button>

								<div class="flex items-center gap-1.5">
									<!-- Link / Rebind Button -->
									<button
										onclick={() => {
											bindModalFunction = func;
											selectedBindKey = func.memns_key || (keys[0]?.name ?? '');
										}}
										class="p-2 rounded-xl transition-all {func.memns_name
											? 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/40 hover:bg-emerald-500/30 shadow-[0_0_10px_rgba(16,185,129,0.25)]'
											: 'bg-slate-800 hover:bg-slate-700 text-slate-300'}"
										title={func.memns_name ? `Bound to memns://${func.memns_name} (Click to rebind)` : 'Bind MemNS Pointer'}
									>
										<Icon icon="ph:link" class="w-4 h-4" />
									</button>

									<!-- Copy Direct URL -->
									<button
										onclick={() => {
											const url = func.memns_name ? `/memns/${func.memns_name}?exec=true` : `/mem/${func.mid}?exec=true`;
											navigator.clipboard.writeText(window.location.origin + url);
											toast.success('Gateway URL copied!');
										}}
										class="p-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 transition-colors"
										title="Copy Execution URL"
									>
										<Icon icon="ph:share-network" class="w-4 h-4" />
									</button>

									<!-- Delete / Unseal -->
									<button
										onclick={() => handleDeleteFunction(func)}
										class="p-2 rounded-xl bg-slate-800 hover:bg-rose-500/20 text-slate-400 hover:text-rose-400 transition-colors"
										title="Delete / Unseal"
									>
										<Icon icon="ph:trash" class="w-4 h-4" />
									</button>
								</div>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	{/if}

	<!-- ========================================================================= -->
	<!-- TAB 3: TEST & PLAYGROUND (OMNIBOX & INTERACTIVE API STUDIO) -->
	<!-- ========================================================================= -->
	{#if studioTab === 'test'}
		<div class="space-y-6 animate-fade-in">
			<!-- 1. Postman-Style Invocation Omnibar -->
			<div class="p-5 rounded-2xl bg-slate-900/80 border border-slate-800 shadow-2xl space-y-4">
				<!-- Target Type Selector -->
				<div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800/80 pb-3">
					<div class="flex items-center gap-2">
						<span class="text-xs font-mono text-slate-400 font-semibold flex items-center gap-1.5">
							<Icon icon="ph:target" class="w-3.5 h-3.5 text-cyan-400" />
							Target Mode:
						</span>
						<div class="flex items-center bg-slate-950 p-1 rounded-xl border border-slate-800 text-xs font-mono">
							<button
								onclick={() => (testTargetType = 'deployed')}
								class="px-3 py-1 rounded-lg transition-all {testTargetType === 'deployed'
									? 'bg-cyan-500 text-slate-950 font-bold shadow-md'
									: 'text-slate-400 hover:text-slate-200'}"
							>
								📦 Deployed Code
							</button>
							<button
								onclick={() => (testTargetType = 'memns')}
								class="px-3 py-1 rounded-lg transition-all {testTargetType === 'memns'
									? 'bg-emerald-500 text-slate-950 font-bold shadow-md'
									: 'text-slate-400 hover:text-slate-200'}"
							>
								🔑 MemNS Name
							</button>
							<button
								onclick={() => (testTargetType = 'mid')}
								class="px-3 py-1 rounded-lg transition-all {testTargetType === 'mid'
									? 'bg-purple-500 text-slate-950 font-bold shadow-md'
									: 'text-slate-400 hover:text-slate-200'}"
							>
								🔗 Content MID
							</button>
							<button
								onclick={() => (testTargetType = 'scratch')}
								class="px-3 py-1 rounded-lg transition-all {testTargetType === 'scratch'
									? 'bg-amber-500 text-slate-950 font-bold shadow-md'
									: 'text-slate-400 hover:text-slate-200'}"
							>
								⚡ Scratchpad
							</button>
						</div>
					</div>

					<!-- Quick Info Badge -->
					<div class="text-xs font-mono text-slate-400 flex items-center gap-2">
						{#if testTargetType === 'deployed' && testSelectedFunction}
							<span class="px-2.5 py-1 rounded-lg bg-cyan-950/60 border border-cyan-500/30 text-cyan-300 font-semibold flex items-center gap-1.5">
								<Icon icon="ph:check-circle" class="w-3.5 h-3.5 text-cyan-400" />
								{testSelectedFunction.name} ({testSelectedFunction.runtime.toUpperCase()})
							</span>
							{#if testSelectedFunction.memns_name}
								<span class="px-2 py-1 rounded-lg bg-emerald-950/60 border border-emerald-500/30 text-emerald-300 font-semibold flex items-center gap-1">
									<Icon icon="ph:identification-card" class="w-3.5 h-3.5" />
									memns://{testSelectedFunction.memns_name}
								</span>
							{/if}
						{:else if testTargetType === 'memns'}
							<span class="text-emerald-400 font-semibold flex items-center gap-1">
								<Icon icon="ph:globe" class="w-3.5 h-3.5" />
								Resolving via MemNS KeyRing
							</span>
						{:else if testTargetType === 'mid'}
							<span class="text-purple-400 font-semibold flex items-center gap-1">
								<Icon icon="ph:fingerprint" class="w-3.5 h-3.5" />
								Direct Merkle DAG Fetch
							</span>
						{/if}
					</div>
				</div>

				<!-- Target Specific Input Controls -->
				<div class="grid grid-cols-1 md:grid-cols-12 gap-3 items-center">
					<!-- Method -->
					<div class="md:col-span-2">
						<select
							bind:value={testMethod}
							class="w-full bg-slate-950 border border-slate-700 text-cyan-300 text-xs font-mono font-bold rounded-xl px-3 py-2.5 focus:outline-none focus:border-cyan-400 shadow-inner"
						>
							<option value="GET">GET</option>
							<option value="POST">POST</option>
							<option value="PUT">PUT</option>
							<option value="DELETE">DELETE</option>
							<option value="PATCH">PATCH</option>
						</select>
					</div>

					<!-- Target Selector / Input -->
					<div class="md:col-span-5">
						{#if testTargetType === 'deployed'}
							{#if deployedFunctions.length > 0}
								<select
									onchange={(e) => onSelectDeployedFunction((e.target as HTMLSelectElement).value)}
									class="w-full bg-slate-950 border border-slate-700 text-slate-200 text-xs font-mono rounded-xl px-3 py-2.5 focus:outline-none focus:border-cyan-400"
								>
									{#each deployedFunctions as func}
										<option value={func.mid} selected={testSelectedFunction?.mid === func.mid}>
											{func.name} ({func.runtime.toUpperCase()}) — {func.mid.substring(0, 12)}... {func.memns_name ? '· [memns://' + func.memns_name.substring(0, 10) + '...]' : ''}
										</option>
									{/each}
								</select>
							{:else}
								<div class="text-xs font-mono text-slate-500 p-2 border border-slate-800 rounded-xl bg-slate-950">
									No deployed functions found. Switch to Scratchpad or Content MID.
								</div>
							{/if}
						{:else if testTargetType === 'memns'}
							<div class="relative">
								<input
									type="text"
									bind:value={testTargetMemNS}
									placeholder="e.g. alice, production-api, or memns://alice"
									class="w-full bg-slate-950 border border-emerald-500/40 text-emerald-300 text-xs font-mono rounded-xl px-3 py-2.5 focus:outline-none focus:border-emerald-400 pl-8"
								/>
								<Icon icon="ph:identification-card" class="w-4 h-4 text-emerald-400 absolute left-2.5 top-3" />
							</div>
						{:else if testTargetType === 'mid'}
							<div class="relative">
								<input
									type="text"
									bind:value={testTargetMID}
									placeholder="Paste content MID (e.g. membafz...)"
									class="w-full bg-slate-950 border border-purple-500/40 text-purple-200 text-xs font-mono rounded-xl px-3 py-2.5 focus:outline-none focus:border-purple-400 pl-8"
								/>
								<Icon icon="ph:fingerprint" class="w-4 h-4 text-purple-400 absolute left-2.5 top-3" />
							</div>
						{:else}
							<div class="px-3 py-2 bg-slate-950 border border-slate-800 rounded-xl text-xs font-mono text-amber-300 flex items-center gap-2">
								<Icon icon="ph:code" class="w-4 h-4" />
								<span>Scratchpad Inline Mode ({testRuntime.toUpperCase()})</span>
							</div>
						{/if}
					</div>

					<!-- Subpath Route -->
					<div class="md:col-span-3">
						<input
							type="text"
							value={testPath}
							oninput={(e) => handlePathInput((e.target as HTMLInputElement).value)}
							placeholder="/sub/route (e.g. /products?min_price=50)"
							class="w-full bg-slate-950 border border-slate-700 text-slate-200 text-xs font-mono rounded-xl px-3 py-2.5 focus:outline-none focus:border-cyan-400"
							title="Route subpath. Typing ?key=value will automatically extract query params!"
						/>
					</div>

					<!-- Execute Button -->
					<div class="md:col-span-2">
						<button
							onclick={handleExecuteTest}
							disabled={testing}
							class="w-full py-2.5 rounded-xl bg-cyan-500 hover:bg-cyan-400 text-slate-950 font-bold font-mono text-xs flex items-center justify-center gap-2 transition-all shadow-[0_0_20px_rgba(6,182,212,0.35)] disabled:opacity-50"
						>
							{#if testing}
								<Icon icon="ph:spinner" class="w-4 h-4 animate-spin" />
								<span>Running...</span>
							{:else}
								<Icon icon="ph:play-fill" class="w-4 h-4" />
								<span>Execute</span>
							{/if}
						</button>
					</div>
				</div>

				<!-- Live Gateway URL Preview & 1-Click Action Bar -->
				<div class="flex flex-col sm:flex-row sm:items-center justify-between gap-2 pt-2 border-t border-slate-800/80 text-xs font-mono">
					<div class="flex items-center gap-2 overflow-hidden text-slate-400">
						<span class="text-[10px] text-slate-500 uppercase tracking-wider shrink-0">Live Gateway URL:</span>
						<code class="text-cyan-300 bg-slate-950 px-2.5 py-1 rounded-lg border border-slate-800/80 truncate select-all">
							{liveGatewayURL}
						</code>
					</div>

					<div class="flex items-center gap-2 shrink-0 relative">
						<button
							onclick={() => {
								navigator.clipboard.writeText(`curl -X ${testMethod} "${getLocalOrigin()}${liveGatewayURL}"`);
								toast.success('cURL command copied!');
							}}
							class="px-2.5 py-1 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-cyan-300 text-[11px] flex items-center gap-1.5 transition-colors"
							title="Copy cURL command"
						>
							<Icon icon="ph:terminal" class="w-3.5 h-3.5" />
							<span>Copy cURL</span>
						</button>

						<button
							onclick={() => {
								navigator.clipboard.writeText(getLocalOrigin() + liveGatewayURL);
								toast.success('Local Gateway URL copied!');
							}}
							class="px-2.5 py-1 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-cyan-300 text-[11px] flex items-center gap-1.5 transition-colors"
							title="Copy Local Gateway URL"
						>
							<Icon icon="ph:copy" class="w-3.5 h-3.5" />
							<span>Copy URL</span>
						</button>

						<!-- Share Menu Target Selector Dropdown -->
						<div class="relative">
							<button
								onclick={() => showShareMenu = !showShareMenu}
								class="px-2.5 py-1 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-cyan-300 text-[11px] flex items-center gap-1.5 transition-colors"
								title="Share and Copy options (Local, Public, cURL)"
							>
								<Icon icon="ph:share-network" class="w-3.5 h-3.5 text-cyan-400" />
								<span>Share</span>
								<Icon icon="ph:caret-down" class="w-3 h-3 text-slate-400" />
							</button>

							{#if showShareMenu}
								<div class="absolute right-0 bottom-full mb-2 w-72 p-2 rounded-xl bg-slate-900 border border-slate-750 shadow-2xl z-50 space-y-1 text-left font-mono animate-fade-in">
									<div class="px-2 py-1 text-[10px] text-slate-400 font-bold uppercase tracking-wider border-b border-slate-800 flex justify-between items-center">
										<span>Copy Execution Target</span>
										<button onclick={() => showShareMenu = false} class="text-slate-500 hover:text-slate-300">✕</button>
									</div>
									<button
										onclick={() => {
											navigator.clipboard.writeText(getLocalOrigin() + liveGatewayURL);
											toast.success('Local Gateway URL copied!');
											showShareMenu = false;
										}}
										class="w-full px-2.5 py-2 rounded-lg hover:bg-slate-800 text-slate-200 text-xs flex items-center gap-2 transition-colors text-left"
									>
										<Icon icon="ph:house" class="w-4 h-4 text-cyan-400 shrink-0" />
										<div class="truncate">
											<div class="font-bold text-[11px]">Local Node URL</div>
											<div class="text-[10px] text-slate-400 truncate">{getLocalOrigin()}{liveGatewayURL}</div>
										</div>
									</button>
									<button
										onclick={() => {
											navigator.clipboard.writeText(PUBLIC_GATEWAY + liveGatewayURL);
											toast.success('Public Gateway URL copied!');
											showShareMenu = false;
										}}
										class="w-full px-2.5 py-2 rounded-lg hover:bg-slate-800 text-slate-200 text-xs flex items-center gap-2 transition-colors text-left"
									>
										<Icon icon="ph:globe-hemisphere-west" class="w-4 h-4 text-emerald-400 shrink-0" />
										<div class="truncate">
											<div class="font-bold text-[11px]">Public Gateway URL</div>
											<div class="text-[10px] text-slate-400 truncate">{PUBLIC_GATEWAY}{liveGatewayURL}</div>
										</div>
									</button>
									<button
										onclick={() => {
											navigator.clipboard.writeText(`curl -X ${testMethod} "${getLocalOrigin()}${liveGatewayURL}"`);
											toast.success('cURL command copied!');
											showShareMenu = false;
										}}
										class="w-full px-2.5 py-2 rounded-lg hover:bg-slate-800 text-slate-200 text-xs flex items-center gap-2 transition-colors text-left"
									>
										<Icon icon="ph:terminal" class="w-4 h-4 text-amber-400 shrink-0" />
										<div class="truncate">
											<div class="font-bold text-[11px]">cURL Command</div>
											<div class="text-[10px] text-slate-400 truncate">curl -X {testMethod} "{getLocalOrigin()}{liveGatewayURL}"</div>
										</div>
									</button>
									<button
										onclick={() => {
											navigator.clipboard.writeText(liveGatewayURL);
											toast.success('Relative path copied!');
											showShareMenu = false;
										}}
										class="w-full px-2.5 py-2 rounded-lg hover:bg-slate-800 text-slate-200 text-xs flex items-center gap-2 transition-colors text-left"
									>
										<Icon icon="ph:fingerprint" class="w-4 h-4 text-purple-400 shrink-0" />
										<div class="truncate">
											<div class="font-bold text-[11px]">Relative Path</div>
											<div class="text-[10px] text-slate-400 truncate">{liveGatewayURL}</div>
										</div>
									</button>
								</div>
							{/if}
						</div>

						{#if testTargetType !== 'scratch'}
							<a
								href={liveGatewayURL}
								target="_blank"
								rel="noreferrer"
								class="px-2.5 py-1 rounded-lg bg-cyan-950/60 hover:bg-cyan-900/60 border border-cyan-500/30 text-cyan-400 text-[11px] flex items-center gap-1 transition-colors"
								title="Open in new browser tab"
							>
								<Icon icon="ph:arrow-square-out" class="w-3.5 h-3.5" />
								<span>Open ↗</span>
							</a>
						{/if}
					</div>
				</div>

				<!-- Smart Adaptive Route Suggestions & Auto-Discovered Manifest Chips -->
				<div class="space-y-2 pt-2 border-t border-slate-800/80">
					{#if discoveringRoutes}
						<div class="flex items-center gap-2 text-[11px] font-mono text-cyan-400">
							<Icon icon="ph:spinner" class="w-3.5 h-3.5 animate-spin" />
							<span>Probing function for self-describing route manifest...</span>
						</div>
					{:else if contextualSuggestions.length > 0}
						<div class="flex flex-wrap items-center gap-2">
							<span class="text-[10px] font-mono text-slate-400 uppercase tracking-wider font-semibold flex items-center gap-1">
								<Icon icon="ph:sparkle-fill" class="w-3 h-3 text-cyan-400" />
								{detectedRoutes.length > 0 ? `Discovered Routes (${testRuntime.toUpperCase()}):` : 'Contextual Route Suggestions:'}
							</span>
							{#each contextualSuggestions as s}
								<button
									onclick={() => applySuggestion(s)}
									class="px-2.5 py-1 rounded-lg bg-slate-950 hover:bg-cyan-950/50 border border-slate-800 hover:border-cyan-500/40 text-slate-200 hover:text-cyan-300 text-[11px] font-mono font-semibold transition-all hover:scale-105 shadow-sm"
								>
									{s.label}
								</button>
							{/each}
						</div>
					{/if}
				</div>
			</div>

			<!-- 2. Dual Panel: Request Configurator + Response Inspector -->
			<div class="grid grid-cols-1 lg:grid-cols-12 gap-6">
				<!-- Left: Request Configuration (6 Cols) -->
				<div class="lg:col-span-6 space-y-4 flex flex-col">
					<!-- Scratchpad Code Editor (when in scratch mode) -->
					{#if testTargetType === 'scratch'}
						<div class="rounded-2xl bg-slate-950 border border-slate-800 overflow-hidden flex flex-col shadow-xl">
							<div class="px-4 py-2.5 bg-slate-900/80 border-b border-slate-800 text-[11px] font-mono text-slate-400 flex items-center justify-between">
								<span>Scratchpad Inline Code</span>
								<div class="flex items-center gap-2">
									<label class="flex items-center gap-1 cursor-pointer">
										<input type="radio" bind:group={testRuntime} value="js" class="text-cyan-500" />
										<span>JavaScript</span>
									</label>
									<label class="flex items-center gap-1 cursor-pointer">
										<input type="radio" bind:group={testRuntime} value="wasm" class="text-cyan-500" />
										<span>WASI</span>
									</label>
								</div>
							</div>
							<textarea
								bind:value={testCode}
								rows="12"
								class="w-full p-4 bg-slate-950 text-cyan-100 font-mono text-xs leading-relaxed resize-y focus:outline-none"
								spellcheck="false"
							></textarea>
						</div>
					{/if}

					<!-- Query Parameters & Headers -->
					<div class="p-5 rounded-2xl bg-slate-900/60 border border-slate-800 space-y-5 shadow-lg">
						<!-- Query Parameters -->
						<div class="space-y-2.5">
							<div class="flex items-center justify-between">
								<span class="text-xs font-mono text-slate-400 font-semibold flex items-center gap-1.5">
									<Icon icon="ph:magnifying-glass" class="w-3.5 h-3.5 text-cyan-400" />
									Query Parameters
								</span>
								<button onclick={addTestQueryParam} class="text-xs font-mono text-cyan-400 hover:text-cyan-300 font-semibold">+ Add Param</button>
							</div>
							{#if testQueryParams.length === 0}
								<div class="text-[11px] font-mono text-slate-500 italic p-3 rounded-lg bg-slate-950/60 border border-slate-800/60">
									No query parameters set. Type <span class="text-cyan-300">?key=value</span> in the route input to auto-detect, or click <strong>+ Add Param</strong>.
								</div>
							{/if}
							{#each testQueryParams as q, i}
								<div class="flex items-center gap-2">
									<input
										type="text"
										bind:value={q.key}
										placeholder="param_key"
										class="w-1/2 bg-slate-950 border border-slate-800 text-slate-200 text-xs font-mono rounded-lg px-3 py-1.5 focus:outline-none focus:border-cyan-400"
									/>
									<input
										type="text"
										bind:value={q.value}
										placeholder="value"
										class="w-1/2 bg-slate-950 border border-slate-800 text-slate-200 text-xs font-mono rounded-lg px-3 py-1.5 focus:outline-none focus:border-cyan-400"
									/>
									<button onclick={() => removeTestQueryParam(i)} class="p-1.5 rounded text-slate-500 hover:text-rose-400 transition-colors">
										<Icon icon="ph:trash" class="w-4 h-4" />
									</button>
								</div>
							{/each}
						</div>

						<!-- Custom Headers -->
						<div class="space-y-2.5 pt-3 border-t border-slate-800/80">
							<div class="flex items-center justify-between">
								<span class="text-xs font-mono text-slate-400 font-semibold flex items-center gap-1.5">
									<Icon icon="ph:sliders" class="w-3.5 h-3.5 text-cyan-400" />
									HTTP Headers
								</span>
								<button onclick={addTestHeader} class="text-xs font-mono text-cyan-400 hover:text-cyan-300 font-semibold">+ Add Header</button>
							</div>
							{#each testHeaders as h, i}
								<div class="flex items-center gap-2">
									<input
										type="text"
										bind:value={h.key}
										placeholder="Header-Name"
										class="w-1/2 bg-slate-950 border border-slate-800 text-slate-200 text-xs font-mono rounded-lg px-3 py-1.5 focus:outline-none focus:border-cyan-400"
									/>
									<input
										type="text"
										bind:value={h.value}
										placeholder="Header Value"
										class="w-1/2 bg-slate-950 border border-slate-800 text-slate-200 text-xs font-mono rounded-lg px-3 py-1.5 focus:outline-none focus:border-cyan-400"
									/>
									<button onclick={() => removeTestHeader(i)} class="p-1.5 rounded text-slate-500 hover:text-rose-400 transition-colors">
										<Icon icon="ph:trash" class="w-4 h-4" />
									</button>
								</div>
							{/each}
						</div>

						<!-- Request Body (POST / PUT / PATCH) -->
						{#if testMethod !== 'GET'}
							<div class="space-y-2 pt-3 border-t border-slate-800/80">
								<div class="flex items-center justify-between">
									<span class="text-xs font-mono text-slate-400 font-semibold">Request Body (JSON)</span>
									<button
										onclick={() => {
											try {
												testBody = JSON.stringify(JSON.parse(testBody), null, 2);
											} catch (_) {}
										}}
										class="text-[11px] font-mono text-slate-400 hover:text-cyan-300"
									>
										Format JSON
									</button>
								</div>
								<textarea
									bind:value={testBody}
									rows="4"
									placeholder={'{\n  "key": "value"\n}'}
									class="w-full p-3 bg-slate-950 border border-slate-800 text-cyan-200 text-xs font-mono rounded-xl focus:outline-none focus:border-cyan-400"
								></textarea>
							</div>
						{/if}
					</div>

					<!-- Recent Invocations Log -->
					{#if testHistory.length > 0}
						<div class="p-5 rounded-2xl bg-slate-900/60 border border-slate-800 space-y-3 shadow-lg">
							<div class="flex items-center justify-between text-xs font-mono text-slate-400 font-semibold">
								<span class="flex items-center gap-1.5">
									<Icon icon="ph:clock-counter-clockwise" class="w-3.5 h-3.5 text-cyan-400" />
									Recent Invocations
								</span>
								<button onclick={() => (testHistory = [])} class="text-[11px] text-slate-500 hover:text-slate-300">Clear</button>
							</div>
							<div class="space-y-2 max-h-48 overflow-y-auto pr-1">
								{#each testHistory as h}
									<div class="p-2.5 rounded-xl bg-slate-950 border border-slate-800/80 flex items-center justify-between text-xs font-mono">
										<div class="flex items-center gap-2.5 overflow-hidden">
											<span
												class="px-1.5 py-0.5 rounded text-[10px] font-bold {h.status >= 200 && h.status < 300
													? 'bg-emerald-950 text-emerald-400 border border-emerald-500/30'
													: 'bg-rose-950 text-rose-400 border border-rose-500/30'}"
											>
												{h.status}
											</span>
											<span class="font-bold text-cyan-300">{h.method}</span>
											<span class="text-slate-300 truncate">{h.path}</span>
											<span class="text-[10px] text-slate-500">({h.target})</span>
										</div>
										<div class="flex items-center gap-2 shrink-0">
											<span class="text-[10px] text-slate-400">{h.duration_ms.toFixed(1)}ms</span>
											<button
												onclick={() => {
													testMethod = h.method;
													testPath = h.path;
													handleExecuteTest();
												}}
												class="p-1 rounded bg-slate-800 hover:bg-cyan-500/20 text-slate-400 hover:text-cyan-300 transition-colors"
												title="Re-run"
											>
												<Icon icon="ph:arrow-clockwise" class="w-3 h-3" />
											</button>
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}
				</div>

				<!-- Right: Execution Response Output (6 Cols) -->
				<div class="lg:col-span-6 space-y-4 flex flex-col">
					<div class="rounded-2xl bg-slate-950 border border-slate-800 overflow-hidden flex flex-col flex-grow shadow-2xl min-h-[480px]">
						<!-- Tabs & Response Metadata Bar -->
						<div class="px-4 py-3 bg-slate-900/90 border-b border-slate-800 flex items-center justify-between">
							<div class="flex items-center gap-1.5">
								<button
									onclick={() => (testActiveTab = 'body')}
									class="text-xs font-mono px-3 py-1.5 rounded-lg transition-colors {testActiveTab === 'body'
										? 'bg-cyan-500/20 text-cyan-300 font-bold border border-cyan-500/30'
										: 'text-slate-400 hover:text-slate-200'}"
								>
									Response Body
								</button>
								<button
									onclick={() => (testActiveTab = 'headers')}
									class="text-xs font-mono px-3 py-1.5 rounded-lg transition-colors {testActiveTab === 'headers'
										? 'bg-cyan-500/20 text-cyan-300 font-bold border border-cyan-500/30'
										: 'text-slate-400 hover:text-slate-200'}"
								>
									Headers
								</button>
								{#if testResponse?.logs && testResponse.logs.length > 0}
									<button
										onclick={() => (testActiveTab = 'logs')}
										class="text-xs font-mono px-3 py-1.5 rounded-lg transition-colors {testActiveTab === 'logs'
											? 'bg-cyan-500/20 text-cyan-300 font-bold border border-cyan-500/30'
											: 'text-slate-400 hover:text-slate-200'}"
									>
										Console Logs ({testResponse.logs.length})
									</button>
								{/if}
							</div>

							{#if testResponse}
								<div class="flex items-center gap-2 font-mono text-xs">
									<span
										class="px-2.5 py-0.5 rounded-md text-[11px] font-bold {testResponse.status >= 200 && testResponse.status < 300
											? 'bg-emerald-950 border border-emerald-500/40 text-emerald-400'
											: 'bg-rose-950 border border-rose-500/40 text-rose-400'}"
									>
										{testResponse.status}
									</span>
									<span class="px-2 py-0.5 rounded-md text-[10px] uppercase bg-slate-800 text-slate-300 border border-slate-700">
										{testResponse.tier || 'edge'}
									</span>
									<span class="text-cyan-400 font-semibold">
										{testResponse.duration_ms?.toFixed(2) || '0.00'}ms
									</span>
								</div>
							{/if}
						</div>

						<!-- Content Output Box -->
						<div class="p-5 flex-grow font-mono text-xs overflow-auto max-h-[580px]">
							{#if testing}
								<div class="h-64 flex flex-col items-center justify-center gap-3 text-slate-400">
									<Icon icon="ph:spinner" class="w-8 h-8 animate-spin text-cyan-400" />
									<span class="font-mono text-xs text-slate-300">Dispatching to P2P Edge Sandbox...</span>
								</div>
							{:else if testResponse}
								{#if testActiveTab === 'body'}
									<pre class="text-cyan-100 whitespace-pre-wrap leading-relaxed select-all">{testResponse.body}</pre>
								{:else if testActiveTab === 'headers'}
									<div class="space-y-2">
										{#each Object.entries(testResponse.headers || {}) as [k, v]}
											<div class="flex items-center justify-between p-2 rounded-lg bg-slate-900/60 border border-slate-800/80 text-slate-300">
												<span class="text-cyan-400 font-semibold">{k}:</span>
												<span class="text-slate-200 select-all">{v}</span>
											</div>
										{/each}
									</div>
								{:else if testActiveTab === 'logs'}
									<div class="space-y-1.5">
										{#each testResponse.logs || [] as log}
											<div class="text-slate-300 flex items-start gap-2 p-2 rounded-lg bg-slate-900/60 border border-slate-800/80">
												<span class="text-cyan-400 select-none font-bold">&gt;</span>
												<span class="select-all">{log}</span>
											</div>
										{/each}
									</div>
								{/if}
							{:else}
								<div class="h-64 flex flex-col items-center justify-center gap-3 text-slate-600">
									<Icon icon="ph:play-circle" class="w-12 h-12 text-slate-700" />
									<span class="font-mono text-xs">Execute a request or pick a Quick Route Chip to inspect live output</span>
								</div>
							{/if}
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}

	<!-- Modal: Generate New Key -->
	{#if showNewKeyModal}
		<div class="fixed inset-0 z-50 bg-slate-950/80 backdrop-blur-sm flex items-center justify-center p-4">
			<div class="bg-slate-900 border border-slate-800 rounded-xl max-w-md w-full p-6 space-y-4 shadow-2xl">
				<h3 class="text-base font-semibold text-slate-100 font-display">Generate New MemNS Key</h3>
				<p class="text-xs text-slate-400">Create a new Ed25519 cryptographic key for naming your edge function.</p>
				<input
					type="text"
					bind:value={newKeyNameInput}
					placeholder="Key name (e.g. my-api)"
					class="w-full bg-slate-950 border border-slate-700 text-slate-200 text-xs font-mono rounded-lg px-3 py-2 focus:outline-none focus:border-cyan-400"
				/>
				<div class="flex justify-end gap-2 pt-2">
					<button
						onclick={() => (showNewKeyModal = false)}
						class="px-4 py-2 rounded-lg bg-slate-800 hover:bg-slate-700 text-xs font-mono text-slate-300"
					>
						Cancel
					</button>
					<button
						onclick={handleCreateKey}
						disabled={generatingKey || !newKeyNameInput.trim()}
						class="px-4 py-2 rounded-lg bg-cyan-500 hover:bg-cyan-400 text-slate-950 text-xs font-bold font-mono disabled:opacity-50"
					>
						{generatingKey ? 'Creating...' : 'Create Key'}
					</button>
				</div>
			</div>
		</div>
	{/if}

	<!-- Modal: Bind MemNS to Function -->
	{#if bindModalFunction}
		<div class="fixed inset-0 z-50 bg-slate-950/80 backdrop-blur-sm flex items-center justify-center p-4">
			<div class="bg-slate-900 border border-slate-800 rounded-xl max-w-md w-full p-6 space-y-4 shadow-2xl">
				<h3 class="text-base font-semibold text-slate-100 font-display">Bind MemNS Pointer</h3>
				<p class="text-xs text-slate-400">
					Select the KeyRing identity to associate with <span class="text-cyan-300 font-mono">{bindModalFunction.name}</span>.
				</p>
				<select
					bind:value={selectedBindKey}
					class="w-full bg-slate-950 border border-slate-700 text-slate-200 text-xs rounded-lg px-3 py-2 font-mono focus:outline-none focus:border-cyan-400"
				>
					{#each keys as k}
						<option value={k.name}>{k.name} ({k.memns_name.substring(0, 16)}...)</option>
					{/each}
				</select>
				<div class="flex justify-end gap-2 pt-2">
					<button
						onclick={() => (bindModalFunction = null)}
						class="px-4 py-2 rounded-lg bg-slate-800 hover:bg-slate-700 text-xs font-mono text-slate-300"
					>
						Cancel
					</button>
					<button
						onclick={handleBindMemNS}
						disabled={bindingKey || !selectedBindKey}
						class="px-4 py-2 rounded-lg bg-cyan-500 hover:bg-cyan-400 text-slate-950 text-xs font-bold font-mono disabled:opacity-50"
					>
						{bindingKey ? 'Binding...' : 'Bind Pointer'}
					</button>
				</div>
			</div>
		</div>
	{/if}
</div>
