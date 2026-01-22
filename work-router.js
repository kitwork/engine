const w = work({ name: "OrderSystem" });

// DISCOVERY: Infrastructure declaration
w.router("POST", "/v1/order/process");
w.retry(3, "1s");
w.version("v1.2.0");

// EXECUTION: Logical body
if (payload.id.is_nil()) {
    return { status: 400, error: "Missing Order ID" };
}

// Shorthand: db() instead of w.db()
let order = db().from("orders").where(id == payload.id).get();

// Complex logic simulation
let total = order.price * (1 + order.tax);

if (total > 1000) {
    order.is_premium = true;
    order.processed_at = now();
}

// The engine will automatically detect this object and return it as JSON
return order;