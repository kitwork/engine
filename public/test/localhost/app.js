const { router, log, render, http } = kitwork();

router.get("/favicon.ico").file("/assets/favicon.ico");

const global = {
    name: "kitwork1",
    logo: "/assets/logo.png",
    favicon: "/assets/favicon.ico",
    title: "Chào mừng tới kitwork",
}
const home = render("/pages/home").global(global).layout("/layouts/home")
const notfound = render("/pages/home/notfound.html").layout("/layouts/home")
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

router.get("/users/:id").handle((request, response) => {
    const view = home.page(request.page())
    const id = request.params("id");
    if (!id) {
        view.bind({ users: db.users.all() });
    } else {
        view.bind({ user: db.users.find({ id: id }) });
    }
    return response.html(view);
});

router.get("/*").handle((request, response) => {
    const page = notfound.bind({
        name: "kitwork1",
        logo: "/assets/logo.png",
        favicon: "/assets/favicon.ico",
        title: "Chào mừng tới kitwork",

    })

    return response.html(page);
});
