const { router, log, render } = kitwork();

router.get("/favicon.ico").file("/assets/favicon.ico");

const api = router.base("/api");

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