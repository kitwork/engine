const { router, log, render, http, database, go, napas, qrcode } = kitwork();

const global = {
    name: "kitwork",
    logo: "/assets/logo.png",
    favicon: "/assets/favicon.ico",
    title: "Chào mừng tới kitwork",
}

const layout = {
    navbar: "_navbar_",
    footer: "_footer_",
    head: "_head_",
}

const layoutDocs = {
    ...layout,
    sidebar: "/docs/_sidebar_",
    toolbar: "/docs/_toolbar_"
}

const site = render.directory("views");

const home = site.path("/").global(global).layout(layout).notfound("notfound");
const docs = site.path("/docs").global(global).layout(layoutDocs).notfound("notfound");

const db = database.connection({
    type: "postgres",
    user: "postgres",
    password: "db.kitwork.io@03122025",
    name: "postgres",
    host: "152.42.253.164",
    port: 5432,
    ssl: "require",
    timezone: "Asia/Ho_Chi_Minh",
    timeout: 5,
    max_open: 50,
    max_idle: 10,
    lifetime: 12,
    max_limit: 60,
})


router.get("/favicon.ico").file("/assets/favicon.ico");
router.get("/taiwindcss.js").file("/assets/js/taiwindcss.js");
// Serve absolute sovereign assets from demo/public relative to project root
router.get("/public/*").directory("./demo/public");
router.get("/assets/*").directory("./assets/*");

router.get("/hello").handle((response) => {
    return response.text("hello world");
});


router.get("/test-query").handle((response) => {
    return response.json({
        // 1. Scoping & Shortcuts
        find: db.table("user").find("username", "grace"),
        find_short: db.find((user) => user.username == "grace"),
        find_complex: db.table("user").find("id", ">", 5),

        first: db.table("user").first(),
        first_short: db.first((user) => user.id == 5),

        list: db.table("user").list(2),
        list_short: db.limit(3).list((user) => user.id > 5),

        exists: db.exists((user) => user.username == "grace1"),

        count: db.table("user").count(),
        count_active: db.count((user) => user.is_active == true),

        create: db.table("user").create({
            username: "test2",
            email: "test@gmail.com",
            is_active: true,
        }),

        update: db.table("user").where(user => user.id == 53).update({
            username: "test14",
            email: "test@gmail.com",
            is_active: true,
        }),

        delete: db.where(user => user.id == 52).remove(),

        join: db.join((order, user) => order.id == user.id).list(),
        join_where: db.where((order, user) => order.id == user.id).list(),
        raw_test: db.table("user").where(u => u.username == "alice%").Raw(),
        last_user: db.table("user").Last(),
    });
});


const api = router.group("/api");



api.get("/").handle((context) => {


    log.Print("Hello from HUB!");
    return context.response.json({ message: "Hello from HUB!" });
});

api.get("/testcache").cache("1h").handle((response) => {
    log.Print("testcache");
    return response.text("testcache");
})

api.get("/gold").cache("5s")
    .catch(() => log.Print("Failed to fetch gold price"))
    .then(() => log.Print("Gold price fetched successfully"))
    .handle((response) => {

        const fetch = http.get("https://edge-api.pnj.io/ecom-frontend/v1/get-gold-price?zone=11");


        if (fetch.status != 200) {
            return response.status(500).json({ status: fetch.status, error: "Failed to fetch gold price" })
        }

        const body = fetch.json()
        const data = body.data.map(item => ({
            name: item.tensp,
            buy: item.giamua,
            sell: item.giaban
        }));

        return response.status(200).json({ success: true, count: data.length, data: data });
    });


router.get("/docs/:site?").handle((request, response) => {
    const siteParam = request.params("site");



    const binding = { request: request }
    // Dùng "/" làm mặc định để nạp views/docs/page.kitwork.html
    const view = docs.page(siteParam || "/").bind(binding);
    return response.html(view);
});

router.get("/users/:id?").handle((request, response) => {
    const page = home.page(request.page())

    const id = request.params("id");
    const binding = { request: request }
    if (!id) {
        // TRANG DANH SÁCH (vì không có id)
        binding.users = db.table("user").list(5);
    } else {
        // TRANG CHI TIẾT (vì có id)
        binding.user = db.where(user => user.id == id).first();
    }
    const view = page.bind(binding)
    return response.html(view);
});

router.get("/background").handle((response) => {
    log.Print("Request received, starting background task...");

    go(() => {
        log.Print("Background task is running...");
        // Simulate some work
        log.Print("Background task completed successfully!");
    });

    return response.text("Background task started!");
});

router.get("/background-db").handle((response) => {
    go(() => {
        log.Print("Fetching users in background...");
        const users = db.table("user").list(3);
        log.Print("Users fetched in background: " + users.length);
    });
    return response.text("DB Background task started!");
});

router.get("/dashboard").handle((request, response) => {
    const binding = {}
    const view = dashboard.page(request.page()).bind(binding);
    return response.html(view);
});


router.get("/test-vietqr").handle((request, response) => {
    // 1. Tạo payload VietQR chuẩn Napas
    const myNapas = napas
        .bank("970415", "1234567890") // BIN VietinBank & Số tài khoản
        .amount(150000)               // Số tiền: 150,000 VND
        .receiver("NGUYEN VAN A")     // Tên chủ tài khoản
        .info("Thanh toan nuoc");

    // 2. Vẽ mã QR nghệ thuật Premium (PNG) bằng Smart API
    const pngBytes = qrcode
        .napas(myNapas)       // Truyền trực tiếp đối tượng Napas vào
        .template("circular")
        .logo("vietqr")
        .cell("#005ba1")      // Cấu hình màu cell nhanh bằng chuỗi màu
        .finder("#005ba1")    // Cấu hình tất cả finder nhanh bằng màu
        .size(400)
        .png();

    return response.image(pngBytes);
});

router.get("/test-vietqr-svg").handle((request, response) => {
    // 1. Tạo payload VietQR chuẩn Napas
    const myNapas = napas
        .bank("970415", "1234567890")
        .amount(200000)
        .receiver("NGUYEN VAN B")
        .info("Thanh toan dien");

    // 2. Sinh mã QR dạng SVG bằng Smart API với cấu hình Gradient phức tạp
    const svgString = qrcode
        .napas(myNapas)
        .template("circular")
        .logo("vietqr")
        .cell({
            gradient: { type: "linear", colors: ["#0f172a", "#38bdf8"], angle: 45 }
        })
        // Mắt Top-Left: Gradient Đỏ sang Hồng
        .finder("tl", {
            stroke: "#be123c",
            rounded: 3.5,
            gradient: { type: "linear", colors: ["#e11d48", "#f43f5e"], angle: 90 }
        })
        // Mắt Top-Right: Gradient Xanh lá sang Ngọc
        .finder("tr", {
            stroke: "#065f46",
            rounded: 1.5,
            gradient: { type: "linear", colors: ["#047857", "#10b981"], angle: 45 }
        })
        // Mắt Bottom-Left: Màu Tím đậm, viền Tím nhạt, vuông truyền thống
        .finder("bl", {
            color: "#6d28d9",
            stroke: "#5b21b6",
            rounded: 0
        })
        .svg();

    return response.svg(svgString);
});

router.get("/*").handle((request, response) => {
    const requestPath = request.path();

    const binding = { path: requestPath }
    const view = home.page(requestPath).bind(binding);
    return response.html(view);
});

