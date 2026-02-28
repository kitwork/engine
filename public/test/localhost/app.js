// ==========================================
// KHỞI TẠO VÀ THIẾT LẬP TÀI NGUYÊN (RESOURCES)
// ==========================================



const entity = kitwork.entity()
const logger = kitwork.log({ folder: "/logs" })

const http = kitwork.http({ timeout: "10s", headers: { "User-Agent": "kitwork" } })
const smtp = kitwork.smtp({
    host: "localhost",
    port: 25,
    user: "[EMAIL_ADDRESS]",
    password: "[PASSWORD]"
})

const chrome = kitwork.chrome({
    headless: true,
    sandbox: false,
    timeout: "10s"
})

const db = kitwork.database({
    host: "localhost",
    user: "root",
    password: "123456",
    database: "test"
})

// State tĩnh chia sẻ chung
const globalState = {
    name: "kitwork",
    logo: "/assets/logo.png",
    favicon: "/assets/favicon.ico"
}

// Khai báo sẵn các block render (Compiler parse template lúc boot)
const homeRender = kitwork.render("/view/pages/index.html").layout("/view/layout")
const dashboardRender = kitwork.render("/view/pages/dashboard/index.html").layout({
    navbar: "/view/layout/navbar.html",
    footer: "/view/layout/footer.html",
    sidebar: "/view/layout/sidebar.html",
    main: "/view/layout/main.html"
})

// Mở rộng DB theo Tenant (Nâng cao)
const dbPgl = kitwork.postgres(entity.database)
const dbRedis = kitwork.redis(entity.redis)


const task = kitwork.cron().fail(() => logger.error("Failed to fetch gold price"))

// ==========================================
// JOBS & SCHEDULER
// ==========================================
// Tác vụ dọn dẹp hàng ngày
task.schedule(() => {
    db.users.delete()
    logger.info("Daily cleanup completed.")
}).daily("13:00")

// Tác vụ ping định kỳ
task.schedule(() => {
    logger.info("Ping từ Cron Job!")
}).every("5s")



// Khởi tạo router chính với rate limit và body limit
const router = kitwork.router().rateLimit(10).bodyLimit(1024 * 1024 * 10)


// ==========================================
// GIAO DIỆN & SSR (SERVER-SIDE RENDERING)
// ==========================================
// Quản lý tài nguyên Tĩnh
router.get("/assets").folder("/assets");
router.get("/favicon.ico").file("/assets/favicon.ico");



// Bơm dữ liệu vào trang chủ thông qua Method GET và Handle
router.get("/").handle((request, response) => {

    // Sử dụng thẳng engine render để nạp dữ liệu vào template
    return response.html(homeRender.bind({
        name: globalState.name,
        logo: globalState.logo,
        favicon: globalState.favicon,
        title: "Chào mừng tới kitwork",
        users: db.users.list()
    }))
});

// Bơm dữ liệu vào trang Dashboard
router.get("/dashboard").handle((request, response) => {
    let state = {
        name: globalState.name,
        logo: globalState.logo,
        favicon: globalState.favicon,
        title: "Quản trị viên",
        users: db.users.list()
    }

    // Engine có thể map layout cụ thể tuỳ vào route nếu cần
    return response.html(dashboardRender.bind(state))
});


// ==========================================
// API HỖ TRỢ CẢ VIẾT NGẮN (Context: context) VÀ DÀI (request, response)
// ==========================================

// Ví dụ viết ngắn với Context (context)
kitwork.get("/send-mail").handle((context) => {
    smtp.send("[EMAIL_ADDRESS]").template("test.html").bind({
        name: "Quốc",
        avatar: "/assets/logo.png"
    })
    return context.response.json({ message: "Email sent successfully" })
})

// Ví dụ viết dài truyền 2 tham số (request, response)
kitwork.get("/screenshot").handle((request, response) => {
    let url = request.query("url") || "https://github.com/kitwork"
    let imageBuffer = chrome.goto(url).viewport(1920, 1080).wait("2s").screenshot()
    return response.image(imageBuffer)
})


// ==========================================
// CHUYỂN HƯỚNG VÀ REVERSE PROXY ĐỘNG
// ==========================================
router.get("/api/users").redirect("/users").status(301);

router.get("/api/profile/:id").forward("/account/:id");

// Router này hỗ trợ ngữ cảnh ngắn 1 tham số 'context'
router.get("/api/avatars/:id").forward((context) => {
    return "https://i.imgur.com/" + context.request.params("id") + ".gif"
});


// ==========================================
// API VỚI CACHING & LIFECYCLES
// ==========================================
router.get("/api/gold").cache("5s")
    .fail(() => logger.error("Failed to fetch gold price"))
    .done(() => logger.info("Gold price fetched successfully"))
    .handle((request, response) => {
        let fetch = http.get("https://edge-api.pnj.io/ecom-frontend/v1/get-gold-price?zone=11");

        if (fetch.status != 200) {
            return response.status(500).json({ status: fetch.status, error: "Failed to fetch gold price" })
        }

        const data = fetch.body.data.map(item => ({
            name: item.tensp,
            buy: item.giamua,
            sell: item.giaban
        })).filter(item => item.sell > 10000);

        return { success: true, count: data.length, data: data };
    });


// ==========================================
// API CÓ MIDDLEWARE BẢO VỆ GỘP CHUNG (GUARDS)
// ==========================================
const api = router.base("/api");
const apiUser = api.group("/users").guard((request, response) => {
    if (!request.headers("authorization")) {
        return response.status(401).json({ message: "Unauthorized" })
    }
});

apiUser.get("/").handle((response) => {
    return response.json({
        currentUser: "Admin User",
        status: "Online",
        users: db.users.list()
    })
});

apiUser.get("/:id").handle((request, response) => {
    let id = request.params("id")
    let user = db.users.find(id)

    if (!user || !user.id) {
        return response.status(404).json({ message: "User not found" })
    }
    return response.status(200).json(user)
});
