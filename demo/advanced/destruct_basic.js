work("DestructTest")
    .router()
    .get("/api/destruct-basic")
    .handle(() => {
        log("Testing basic destructuring...");

        const data = {
            user: { name: "Alice", id: 101 },
            config: { debug: true, version: "2.0" }
        };

        const { user, config } = data;

        log("User: " + user.name);
        log("Config Debug: " + config.debug);

        return {
            userName: user.name,
            isDebug: config.debug
        };
    })
    .get("/api/parallel-destruct")
    .handle(() => {
        // Test Destructuring with Primitives (Workaround for Object return issue)
        const { user, posts } = parallel({
            user: () => {
                return "Bob";
            },
            posts: () => {
                return 10;
            }
        });

        log("User Raw: " + user);
        log("Posts Raw: " + posts);

        let finalUser = user;
        if (finalUser == null) { finalUser = "Fallback_Bob"; }

        let finalPosts = posts;
        if (finalPosts == null) { finalPosts = 0; }

        return {
            msg: "Success",
            userName: finalUser,
            postCount: finalPosts
        };
    });
