const router = kitwork.router;

router.get("/hello").handle((ctx) => {
    return "Hello from Kitwork!";
});

router.get("/user/:id").handle((ctx) => {
    return {
        message: "User detail",
        id: ctx.params.id,
        query: ctx.query
    };
});
