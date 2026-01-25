work("each_demo")
    .router()
    .get("/api/each")
    .handle(() => {
        const list = [1, 2, 3];
        log("Testing each on list:");

        list.each(n => {
            log("Number: " + n);
        });

        const c = list.len();
        log("Count is: " + c);

        return {
            message: "Done",
            count: c
        };
    });
