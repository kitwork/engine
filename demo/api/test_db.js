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
        // Use db().from() to ensure a fresh query context is created and correctly bound
        const users = db().from("user").take(1);
        return {
            success: true,
            syntax: "db.from",
            data: users
        };
    })
    .get("/test-db-any", () => {
        // Test any() method
        const hasAlice = db.user.where(u => u.username == "alice").get();
        const hasZard1 = db().from("user").take(1);
        const hasZard = db.from("user").take(1);
        return {
            success: true,
            hasZard1,
            hasAlice,
            hasZard
        };
    });
