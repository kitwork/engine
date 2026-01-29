work("HomeView")
    .get("/", () => {
        let page = readfile("demo/view/work.html");
        return html(page);
    });
