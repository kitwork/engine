work("TestLogicView").
    get("/logic", () => {
        return {
            title: "Advanced Logic Test",
            items: [
                { name: "Cheap Item", price: 50 },
                { name: "Standard Item", price: 100 },
                { name: "Premium Item", price: 200 }
            ],
            user: {
                level: 2, // 1: New, 2: Regular, 3: VIP
                score: 85.5
            },
            malicious: "<script>alert('HACKED')</script>",
            htmlInjection: "<h1 style='color: red'>I am HUGE</h1>",
            legacySafe: "<span style='color: green'>Trusted Content via .html()</span>".html()
        };
    }).
    render("demo/view/logic_test.html");
