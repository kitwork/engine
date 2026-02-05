work("OrderAPI")
    .get("/benchmark", (ctx) => {

        return "hello world";
    })
    .cache("3s")
    .render("demo/view/render.html");