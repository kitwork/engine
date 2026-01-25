work("HeaderTest")
    .get("/api/headers", () => {
        const ua = header("User-Agent");
        const all = header();

        return {
            userAgent: ua,
            allHeaders: all,
            custom: header("X-Custom-Header") || "not-set"
        };
    });
