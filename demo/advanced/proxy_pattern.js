// ==========================================
// ADVANCED: GENERIC PROXY PATTERN
// ==========================================
// Minh họa sức mạnh của Generic Proxy: 
// Cùng một cú pháp nhưng chạy 2 chế độ khác nhau.

// 1. Mock External Service
let notifyService = (email) => {
    log("📧 Queuing email for:", email);
    return true;
};

// 2. Direct Execution Mode (Chạy thật trên RAM)
let users = db().table("user").limit(3).get();
log("--- DIRECT EXEC ---");
users.map(u => {
    if (u.is_active) {
        notifyService(u.email);
    }
});

// 3. Symbolic Execution Mode (Dịch sang SQL)
log("--- SYMBOLIC EXEC ---");
let activeUsers = db().table("user")
    .where(u => u.is_active == true) // Dịch thành: WHERE "is_active" = true
    .get();

return {
    processed: users.len(),
    active_in_db: activeUsers.len()
};
