work("HomeView").get("/", () => {
    return {
        name: "Huỳnh Nhân Quốc",
        age: 24,
        city: "Ho Chi Minh City"
    };
}).layout({ navbar: "demo/view/navbar.html", footer: "demo/view/footer.html" }).render("demo/view/work.html");
