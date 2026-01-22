const w = work("SalesAnalytics");

w.router("GET", "/stats/daily");

let sales = db().from("transactions").get();
let revenue = sales.sum("amount");

// Chaining prototypes for formatting
let formattedRevenue = revenue.float().string();

return {
    day: now().text(),
    total_sales: sales.len(),
    revenue_usd: formattedRevenue,
    is_target_met: revenue >= 5000
};
