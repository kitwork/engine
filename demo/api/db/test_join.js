// Verification of the "Parameter-based Magic Join" logic
log("=== STARTING SUPER MAGIC JOIN VERIFICATION ===");

// TEST 1: Join với tham số đặt tên theo bảng
log("Test 1: Single lambda join");
db().from("users")
    .join(orders => orders.user_id == users.id)
    .take();


// TEST 3: Group & Having kết hợp
log("Test 3: Group and Having");
db().from("orders")
    .group("user_id")
    .having(o => o.total_price in [1000, 2000])
    .take();

log("=== SUPER MAGIC JOIN VERIFICATION COMPLETE ===");

work("JoinDB").get("/api/test/join", () => {
    return { ok: true };
});
