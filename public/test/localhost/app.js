const { router, log, render, http, database } = kitwork();



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
        list: db.table("user").list(10),
        find: db.table("user").find((user) => user.id == 1),
        first: db.table("user").first(),
        count: db.table("user").count(),
        sum: db.table("user").sum("id"),
        avg: db.table("user").avg("id"),
        min: db.table("user").min("id"),
        max: db.table("user").max("id"),
        raw: db.table("user").where(user => user.id > 10).orderBy("id", "desc").offset(5).limit(10).list()
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
        binding.users = db.table("user").list();
    } else {
        // TRANG CHI TIẾT (vì có id)
        binding.user = db.table("user").where(user => user.id == id).first();
    }
    const view = page.bind(binding)
    return response.html(view);
});

router.get("/*").handle((request, response) => {
    const view = notfound.bind(null)
    return response.html(view);
});
