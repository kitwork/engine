// ==========================================
// KHỞI TẠO VÀ THIẾT LẬP TÀI NGUYÊN (RESOURCES)
// ==========================================
const app = kitwork()

// GIAO DIỆN & SSR (SERVER-SIDE RENDERING)
// ==========================================
// Quản lý tài nguyên Tĩnh
router.get("/assets").folder("/assets");
router.get("/favicon.ico").file("/assets/favicon.ico");

const api = router.base("/api");

api.get("/test").handle((response) => {
    return response.json({ message: "Test" })
});