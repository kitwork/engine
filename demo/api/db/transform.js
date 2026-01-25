work("db_transform")
    .router("GET", "/api/db/transform");

// SQL generated: SELECT * FROM "user"
let results = db().table("user").get();

// Dùng map để transform kết quả từ DB
let formatted = results.map(u => ({
    display: u.username.upper(),
    is_active: u.status == "active"
}));

return {
    raw_count: results.length,
    formatted: formatted
};
