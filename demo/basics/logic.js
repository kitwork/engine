// ==========================================
// BASICS: OBJECT & MATH
// ==========================================

log("🧮 Doing strict math...");

let price = 100.0;
let tax = 0.1;
let total = price * (1 + tax);

let summary = {
    original: price,
    tax_rate: "10%",
    final_price: total
};

if (total > 100) {
    log("⚠️ High value order!");
    summary.is_expensive = true;
} else {
    summary.is_expensive = false;
}

return summary;
