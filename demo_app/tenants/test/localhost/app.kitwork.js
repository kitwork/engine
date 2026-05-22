const { router, log, render, http, go } = kitwork();

// Global metadata shared across all pages
const globalMeta = {
    title: "Kitwork Showcase Portal",
    description: "Industrial-grade stack-based VM server demo",
    logo: "/assets/logo.png",
    currentYear: "2026",
};

// Layout components mapping
const mainLayout = {
    navbar: "_navbar_",
    footer: "_footer_",
    head: "_head_",
};

// Configure view rendering directory and dynamic layout bindings
const views = render.directory("views");
const homeTemplate = views.path("/").global(globalMeta).layout(mainLayout).notfound("notfound");

// 1. Static asset routes
router.get("/assets/*").directory("./assets");

// 2. HTML Page routes
router.get("/").handle((request, response) => {
    const binding = {
        activeTab: "home",
        pageTitle: "Sovereign Bytecode Execution",
    };
    const view = homeTemplate.page("/").bind(binding);
    return response.html(view);
});

router.get("/dashboard").handle((request, response) => {
    const binding = {
        activeTab: "dashboard",
        pageTitle: "Metrics & Performance Dashboard",
        metrics: {
            vmClock: "14.1M ops/s",
            latency: "70ns",
            dbQuery: "230ns",
            energyUsage: "Minimal (Near-Zero GC)",
        }
    };
    const view = homeTemplate.page("dashboard").bind(binding);
    return response.html(view);
});

router.get("/about").handle((request, response) => {
    const binding = {
        activeTab: "about",
        pageTitle: "About the Philosophy",
    };
    const view = homeTemplate.page("about").bind(binding);
    return response.html(view);
});

// 3. API endpoints
router.get("/api/time").handle((response) => {
    return response.json({
        success: true,
        timestamp: now(),
        message: "Hello from Kitwork VM!",
    });
});

// Demonstrating external API proxy fetch
router.get("/api/quote").cache("10s").handle((response) => {
    const fetchRes = http.get("https://dummyjson.com/quotes/random");
    if (fetchRes.status != 200) {
        return response.status(500).json({
            success: false,
            error: "Failed to fetch remote quote"
        });
    }
    const body = fetchRes.json();
    return response.json({
        success: true,
        quote: body.quote,
        author: body.author
    });
});

// Demonstrating asynchronous background processing using Go routines
router.get("/api/trigger-sync").handle((response) => {
    log.Print("[VM] Received API request to trigger sync");
    
    go(() => {
        log.Print("[VM] Background routine started successfully!");
        // Simulate background sync work
        log.Print("[VM] Background routine complete!");
    });
    
    return response.json({
        success: true,
        message: "Background execution routine spawned successfully!"
    });
});

// 4. Wildcard fallback route to handle 404
router.get("/*").handle((request, response) => {
    const view = homeTemplate.page("notfound").bind({ path: request.path() });
    return response.status(404).html(view);
});
