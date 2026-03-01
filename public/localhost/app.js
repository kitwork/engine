const { router } = kitwork();

const api = router.base("/api");

api.get("/hello").handle(() => {
    return "Hello from Kitwork!";
});

router.get("/user/:id").handle((req, res) => {
    const id = req.param("id");
    const q = req.query("q");
    return res.json({
        id_val: id,
        q_val: q
    });
});

router.get("/").handle((req, res) => "Welcome home");