const { router, log, render, http } = kitwork();

router.get("/favicon.ico").file("/assets/favicon.ico");

const api = router.group("/api");

api.get("/hello").handle((context) => {
    log.Print("Hello from HUB!");
    return context.response.json({ message: "Hello from HUB!" });
});

router.get("/user/:id").handle((request, response) => {
    // Test Named Request/Response style

    const id = request.param("id");
    const q = request.query("q");
    return response.json({
        id_val: id,
        q_val: q,
        style: "named"
    });
});

router.get("/").handle((request, response) => {
    const name = request.query("name") || "Kitwork";
    return response.status(200).html("<h1>Welcome to " + name + "</h1>");
});

router.get("/api/gold").cache("5s")
    .catch(() => log.Print("Failed to fetch gold price"))
    .then(() => log.Print("Gold price fetched successfully"))
    .handle((response) => {

        const fetch = http.get("https://edge-api.pnj.io/ecom-frontend/v1/get-gold-price?zone=11");


        if (fetch.status != 200) {
            return response.status(500).json({ status: fetch.status, error: "Failed to fetch gold price" })
        }
        const data = fetch.json().data.map(item => ({
            name: item.tensp,
            buy: item.giamua,
            sell: item.giaban
        })).filter(item => item.sell > 10000);

        return response.status(200).json({ success: true, count: data.length, data: data });
    });