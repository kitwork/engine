import { app, db, logger, http, smtp, chrome } from "../core.js"

const router = app.router().rateLimit(10).bodyLimit(1024 * 1024 * 10)

// ==========================================
// API ĐƠN GIẢN KHÔNG CẦN CHUỖI LAYER
// ==========================================
app.get("/send-mail").handle((request, response) => {
    smtp.send("[EMAIL_ADDRESS]").template("test.html").bind({
        name: "Quốc",
        avatar: "/assets/logo.png"
    })
    return response.json({ message: "Email sent successfully" })
})

app.get("/screenshot").handle((request, response) => {
    let url = request.query("url") || "https://github.com/kitwork"
    let imageBuffer = chrome.goto(url).viewport(1920, 1080).wait("2s").screenshot()
    return response.image(imageBuffer)
})

// ==========================================
// CHUYỂN HƯỚNG VÀ REVERSE PROXY ĐỘNG
// ==========================================
router.get("/api/users").redirect("/users").status(301);

router.get("/api/profile/:id").forward("/account/:id");

router.get("/api/avatars/:id").forward((request) => {
    return "https://i.imgur.com/" + request.params("id") + ".gif"
});


// ==========================================
// API VỚI CACHING & LIFECYCLES
// ==========================================
router.get("/api/gold").cache("5s")
    .fail(() => logger.error("Failed to fetch gold price"))
    .done(() => logger.info("Gold price fetched successfully"))
    .handle((response) => {
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
    if (!request.headers["authorization"]) {
        return response.status(401).json({ message: "Unauthorized" })
    }
});

apiUser.get("/").handle((request, response) => {
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
