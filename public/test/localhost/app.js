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

router.get("/test-fetch").handle((ctx) => {
    const res = fetch("https://jsonplaceholder.typicode.com/todos/1");
    const data = res.json();
    return ctx.json({
        outside: data,
        status: res.status,
        ok: res.ok
    });
});