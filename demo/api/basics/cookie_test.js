work("CookieTest")
    .get("/api/cookie/set", () => {
        cookie("session_id", "xyz-123", { httpOnly: true, maxAge: 3600 });
        cookie("theme", "dark");
        return { status: "ok", message: "Cookies set" };
    })
    .get("/api/cookie/get", () => {
        return {
            session: cookie("session_id"),
            theme: cookie("theme"),
            missing: cookie("non_existent")
        };
    });
