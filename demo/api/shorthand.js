// ==========================================
// API: SHORTHAND DEFINTION
// ==========================================
// Chỉ cần 2 dòng để tạo API

// Trả về trực tiếp kết quả DB mà không cần bọc json()
return work("UserAPI")
    .router("GET", "/api/users")
    .handle(() => {
        return db().from("user").limit(5).get();
    });