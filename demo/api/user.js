// ==========================================
// API: SHORTHAND DEFINTION
// ==========================================
// Chỉ cần 2 dòng để tạo API

// Trả về trực tiếp kết quả DB mà không cần bọc json()

const w = work("UserAPI").router("GET", "/api/users")

return db().table("user")
    .where(u => u.is_active == true) // Dịch thành: WHERE "is_active" = true
    .get();
