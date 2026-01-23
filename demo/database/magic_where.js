// ==========================================
// DATABASE: MAGIC LAMBDA FILTER
// ==========================================
// Sử dụng Lambda để tạo SQL Where an toàn và tự nhiên

log("🔍 Searching for user 'bob'...");

// SQL generated: SELECT * FROM "user" WHERE "username" = $1
let users = db().table("user")
    .where(u => u.username == "bob")
    .limit(10)
    .get();

return {
    query: "Magic Lambda Where",
    result: users
};
