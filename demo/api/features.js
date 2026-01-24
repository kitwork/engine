work("feature_test")
    .router()
    .get("/api/features")
    .handle(() => {
        log("Main handler started");

        defer(() => {
            log("Deferred cleanup 1 executed");
        });

        go(() => {
            log("Background task 1 running...");
        });

        const results = parallel({
            user: () => {
                log("Parallel task: user");
                return { id: 1, name: "Admin" };
            },
            stats: () => {
                log("Parallel task: stats");
                return { visits: 100 };
            }
        });

        log("Parallel results fetched");

        defer(() => {
            log("Deferred cleanup 2 executed (should be first in reverse order)");
        });

        return {
            success: true,
            data: results
        };
    });
