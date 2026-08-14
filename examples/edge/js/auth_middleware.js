// MemEdge Auth & Token Verification Edge Gateway
// Demonstrates: Header inspection, Bearer token extraction, Role-Based Access Control (RBAC).

export default function handler(req) {
    const { method, path, headers } = req;

    // Public route: GET /public/info
    if (path === "/public/info" || path === "/info") {
        return json(200, {
            status: "public",
            service: "MemEdge Security Gateway",
            description: "Public edge endpoint accessible without credentials."
        });
    }

    // Protected routes require Authorization: Bearer <token>
    const authHeader = headers["Authorization"] || headers["authorization"] || "";
    if (!authHeader.startsWith("Bearer ")) {
        return json(401, {
            error: "Unauthorized",
            message: "Missing or malformed Authorization header. Expected: 'Bearer <token>'"
        });
    }

    const token = authHeader.substring(7).trim();

    // Mock token verification & role assignment
    const tokenDb = {
        "admin-secret-token-999": { uid: "u_adm01", role: "admin", name: "Alice Admin" },
        "dev-secret-token-123": { uid: "u_dev42", role: "developer", name: "Bob Developer" },
        "viewer-secret-token-456": { uid: "u_view7", role: "viewer", name: "Carol Viewer" }
    };

    const session = tokenDb[token];
    if (!session) {
        return json(403, {
            error: "Forbidden",
            message: "Invalid or expired access token."
        });
    }

    // Admin-only route: GET /admin/dashboard
    if (path === "/admin/dashboard") {
        if (session.role !== "admin") {
            return json(403, {
                error: "Forbidden",
                message: "Admin privileges required for this route."
            });
        }
        return json(200, {
            view: "Admin Dashboard",
            user: session,
            system: { total_nodes: 48, network_health: "99.98%" }
        });
    }

    // Profile route: GET /user/profile
    if (path === "/user/profile") {
        return json(200, {
            user: session,
            client_ip: req.client_ip,
            authenticated_at: new Date().toISOString()
        });
    }

    return json(404, { error: "Route not found", path: path });
}

function json(status, data) {
    return {
        status: status,
        headers: { "Content-Type": "application/json", "X-Auth-Engine": "MemEdge-RBAC" },
        body: JSON.stringify(data, null, 2)
    };
}
