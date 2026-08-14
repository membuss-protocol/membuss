// examples/edge/js/hello.js
// MemEdge JavaScript Edge Function
export default function handler(req) {
    const name = req.query.name || "Membuss User";
    const greeting = req.query.greeting || "Welcome to MemEdge";

    console.log(`[Edge Log] Processing request for name='${name}' from IP='${req.client_ip}'`);

    return {
        status: 200,
        headers: {
            "Content-Type": "application/json",
            "X-Membuss-Runtime": "Goja-ECMAScript",
            "X-Serverless-Engine": "MemEdge-v2"
        },
        body: JSON.stringify({
            message: `${greeting}, ${name}!`,
            path: req.path,
            method: req.method,
            client_ip: req.client_ip,
            timestamp: new Date().toISOString(),
            engine: "MemEdge Decentralized Serverless"
        }, null, 2)
    };
}
