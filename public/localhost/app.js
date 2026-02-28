const { router } = kitwork; // Dùng Destructuring để kích hoạt Getter tự động của chúng ta

router.get("/assets").folder("/assets");
router.get("/favicon.ico").file("/assets/favicon.ico");

router.get("/hello").handle((req, res) => {
    return res.json({ message: "hello" })
});

router.get("/print").handle((req, res) => {
    const name = req.query("name")
    print(name);
    return res.json({ message: name })
});

router.get("/").redirect("/welcome");