// ==========================================
// DATABASE: TRANSFORMATION
// ==========================================

log("🔄 Fetching and transforming data...");

// 1. Get raw data
let raw = db().table("user").limit(5).get();

// 2. Transform in-memory using Lambda
let viewModels = raw.map(u => {
    return {
        id: u.id,
        display: u.username.upper(),
        contact: u.email
    };
});

return viewModels;
