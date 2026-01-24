work("UserSystem")
    .version("1.0.0")
    .router("GET", "/api/user")
    .handle((req) => {
        return {
            id: 1,
            name: "Kitwork User",
            role: "Developer",
            context: req
        };
    });