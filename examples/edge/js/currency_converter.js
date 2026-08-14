// examples/edge/js/currency_converter.js
// Practical REST Edge Function with Math & JSON Body Handling
export default function handler(req) {
    let payload = {};
    if (req.method === "POST" && req.body) {
        try {
            payload = req.json();
        } catch (e) {
            return {
                status: 400,
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ error: "Malformed JSON payload: " + e.message })
            };
        }
    }

    const rates = {
        USD: 1.0,
        EUR: 0.92,
        GBP: 0.79,
        JPY: 154.20,
        BTC: 0.0000105
    };

    const amount = parseFloat(req.query.amount || payload.amount || "100");
    const from = (req.query.from || payload.from || "USD").toUpperCase();
    const to = (req.query.to || payload.to || "EUR").toUpperCase();

    if (isNaN(amount) || amount <= 0) {
        return {
            status: 400,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ error: "Invalid amount. Must be a positive number." })
        };
    }

    if (!rates[from] || !rates[to]) {
        return {
            status: 400,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
                error: `Unsupported currency pair: ${from}/${to}`,
                supported: Object.keys(rates)
            })
        };
    }

    const inUSD = amount / rates[from];
    const converted = inUSD * rates[to];

    console.log(`[Currency] Converted ${amount} ${from} -> ${converted.toFixed(4)} ${to}`);

    return {
        status: 200,
        headers: {
            "Content-Type": "application/json",
            "X-Rate-Source": "Membuss-Edge-FX"
        },
        body: JSON.stringify({
            from,
            to,
            original_amount: amount,
            converted_amount: parseFloat(converted.toFixed(4)),
            rate: parseFloat((rates[to] / rates[from]).toFixed(6)),
            converted_at: new Date().toISOString()
        }, null, 2)
    };
}
