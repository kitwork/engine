// ==========================================
// API: DYNAMIC GATEWAY
// ==========================================
// Đăng ký route động và xử lý logic phức tạp

const r = router();

r.get("/api/dynamic/users", (req) => {
    let name = req.query.name;
    // Tách if để tránh operator || chưa được parser hỗ trợ
    if (name == nil) {
        return { error: "Missing name parameter" };
    }
    if (name == "") {
        return { error: "Missing name parameter" };
    }

    let found = db().table("user")
        .where(u => u.username == name)
        .get();

    return {
        success: true,
        data: found
    };
});

log("✅ Dynamic routes registered!");
