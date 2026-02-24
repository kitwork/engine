import { app, db } from "../core.js"

const router = app.router()

// Quản lý tài nguyên Tĩnh
router.get("/assets").folder("/assets");
router.get("/favicon.ico").file("/assets/favicon.ico");

// GIAO DIỆN & SSR (SERVER-SIDE RENDERING)
const home = router.render("index.html").layout("/layout");
const dashboard = router.render("dashboard/index.html").layout({
    navbar: "/layout/navbar.html",
    footer: "/layout/footer.html",
    sidebar: "/layout/sidebar.html",
    main: "/layout/main.html"
});

// State tĩnh chia sẻ chung
const globalState = {
    name: "kitwork",
    logo: "/assets/logo.png",
    favicon: "/assets/favicon.ico"
}

// Bơm dữ liệu vào trang chủ
home.page("/").bind(() => {
    globalState.title = "Chào mừng tới kitwork"
    globalState.users = db.users.list()
    return globalState
});

// Bơm dữ liệu vào trang Dashboard
dashboard.page("/dashboard").bind(() => {
    return {
        ...globalState,
        title: "Quản trị viên",
        users: db.users.list()
    }
});
