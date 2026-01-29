work("TestDB")
    .get("/test-db-short", () => {
        // Test Entity style: db.user instead of db.from("user")
        const users = db.user.take(2);
        return {
            success: true,
            syntax: "db.user.take",
            data: users
        };
    })
    .get("/test-db-find", () => {
        // Test EF style: db.user.find()
        const user = db.user.find(u => u.username == "alice");
        return {
            success: true,
            syntax: "db.user.find",
            data: user
        };
    })
    .get("/test-db-multi", () => {
        // Test multi-db + entity style: db("conn").user
        const users = db("main").user.limit(1).get();
        return {
            success: true,
            syntax: "db('main').user",
            data: users
        };
    })
    .get("/test-db-from", () => {
        // Test traditional style: db.from()
        const users = db.from("user").limit(1).get();
        return {
            success: true,
            syntax: "db.from",
            data: users
        };
    })
    .get("/test-db-any", () => {
        // Test any() method
        const hasAlice = db.user.where(u => u.username == "alice").any();
        const hasZard = db.user.where(u => u.username == "zard").any();
        return {
            success: true,
            hasAlice,
            hasZard
        };
    });
