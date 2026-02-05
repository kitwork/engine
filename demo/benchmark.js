work("OrderAPI")
    .get("/benchmark", (ctx) => {

        return "hello world";
    })
    .benchmark(1000000)
    .cache("3s")
    .render("demo/view/render.html");