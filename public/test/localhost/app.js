const { router, log, render, http, database, go } = kitwork();

go(() => {
    log.Print("Hello from HUB!");
})

const db = database({
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

const global = {
    name: "kitwork1",
    logo: "/assets/logo.png",
    favicon: "/assets/favicon.ico",
    title: "Chào mừng tới kitwork",
}
const home = render("/pages/home").global(global).layout("/layouts/home")
const notfound = render("/pages/home/notfound.html").global(global).layout("/layouts/home")
const api = router.group("/api");

api.get("/").handle((context) => {


    log.Print("Hello from HUB!");
    return context.response.json({ message: "Hello from HUB!" });
});

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

router.get("/users/:id?").handle((request, response) => {
    const page = home.page(request.page())

    const id = request.params("id");
    const binding = {}
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


router.get("/*").handle((request, response) => {
    const view = notfound.bind(null)
    return response.html(view);
});

