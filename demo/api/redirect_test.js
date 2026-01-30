work("RedirectSystem")
    .get("/old-gateway/*").redirect("/api/hello") // Wildcard: redirects everything under /old-gateway/
    .get("/external").redirect("https://google.com", 301)
    .get("/dynamic-redirect", (req) => {
        // This uses the built-in redirect() in logic
        log("Logic-based redirect triggered");
        redirect("/api/features");
    });
