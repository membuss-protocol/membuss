// MemEdge Multi-Route REST API Microservice
// Demonstrates: Sub-route dispatching, HTTP methods (GET/POST), Regex path parameters, query filtering.

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
            routes: [
                "GET  /healthz",
                "GET  /products",
                "GET  /products/:id",
                "POST /products",
                "GET  /stats"
            ]
        });
    }

    // 2. List products with optional query filtering: GET /products?min_price=50
    if (path === "/products" && method === "GET") {
        let result = products;
        if (query.min_price) {
            const min = parseFloat(query.min_price);
            result = result.filter(p => p.price >= min);
        }
        return json(200, { total: result.length, products: result });
    }

    // 3. Dynamic path parameter: GET /products/:id
    const match = path.match(/^\/products\/(\d+)$/);
    if (match && method === "GET") {
        const id = parseInt(match[1]);
        const item = products.find(p => p.id === id);
        if (!item) {
            return json(404, { error: `Product #${id} not found` });
        }
        return json(200, { product: item });
    }

    // 4. Create new product: POST /products
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

    // 404 Route Not Found
    return json(404, {
        error: "Route Not Found",
        requested_path: path,
        requested_method: method
    });
}

function json(status, data) {
    return {
        status: status,
        headers: {
            "Content-Type": "application/json",
            "X-Edge-Router": "MemEdge-MultiRoute-v2"
        },
        body: JSON.stringify(data, null, 2)
    };
}
