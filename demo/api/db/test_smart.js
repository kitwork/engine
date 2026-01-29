// Verification of the "Ultra-Smart" Where logic
log("=== STARTING SMART WHERE VERIFICATION ===");

// TEST 1: Auto-LIKE qua dấu %
log("Test 1: Auto-LIKE via %");
db().table("user")
    .where((u) => u.username == "Apple%")
    .get();

// TEST 2: Auto-IN qua Array
log("Test 2: Auto-IN via Array");
db().table("user")
    .where((u) => u.id == [100, 200, 300])
    .get();

// TEST 3: Mix (Vẫn hỗ trợ kiểu cũ cho chắc)
log("Test 3: Normal equals");
db().table("user")
    .where((u) => u.role == "admin")
    .get();

log("=== SMART WHERE VERIFICATION COMPLETE ===");

work("SmartDB").get("/api/test/smart", () => {
    return { ok: true };
});
