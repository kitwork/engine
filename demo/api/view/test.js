work("TestRenderView").
    get("/test", () => {
        return {
            message: "Hello from Kitwork Render Engine!",
            version: "v2.0.0 (Industrial Edition)",
            isAdmin: true,
            status: "active",
            users: [
                { name: "Alice", role: "Manager" },
                { name: "Bob", role: "Developer" },
                { name: "Charlie", role: "Designer" }
            ],
            metadata: {
                "Server": "Kitwork Go",
                "Uptime": "99.99%",
                "Region": "Asia/Ho_Chi_Minh"
            }
        };
    }).
    render("demo/view/test.html");
