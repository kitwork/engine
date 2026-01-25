// Run immediately at load time to verify SQL generation in logs
log("=== STARTING MAGIC SQL VERIFICATION ===");

// TEST 1: .like(u => u.name == 'Apple%')
db().table("user")
    .like(u => u.username == "Apple%")
    .get();

// TEST 2: .in(u => u.id == [10, 20])
db().table("user")
    .in(u => u.id == [10, 20])
    .get();

// TEST 3: .where(u => u.username.like('Admin%'))
db().table("user")
    .where(u => u.username.like("Admin%"))
    .get();

log("=== MAGIC SQL VERIFICATION COMPLETE ===");

work("MagicDB").get("/api/test/magic", () => {
    return { ok: true };
});
