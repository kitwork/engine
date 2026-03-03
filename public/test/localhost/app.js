const { router, log, render, http } = kitwork();

router.get("/favicon.ico").file("/assets/favicon.ico");

const home = render("/pages/home").layout("/layouts/home")
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
        const data = fetch.json().data.map(item => ({
            name: item.tensp,
            buy: item.giamua,
            sell: item.giaban
        })).filter(item => item.sell > 10000);

        return response.status(200).json({ success: true, count: data.length, data: data });
    });

router.get("/*").handle((request, response) => {
    const page = home.page(request.path).bind({
        name: "kitwork1",
        logo: "/assets/logo.png",
        favicon: "/assets/favicon.ico",
        title: "Chào mừng tới kitwork",
        users: db.users.list()
    })

<<<<<<< HEAD
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
=======
    return response.html(page);
});
>>>>>>> 02e7701 (work 46 - render handle)
