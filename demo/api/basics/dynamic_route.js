work("DynamicAPI")
    .get("/users/:id", () => {
        const id = params("id"); // URL Path Param
        const source = query("source") || "unknown"; // URL Query Param (?source=...)

        return {
            msg: "User Detail",
            user_id: id,
            request_source: source
        };
    })
    .post("/users/:id/update", () => {
        const id = params("id");
        const { name, email } = body(); // Destructure from POST JSON Body

        log("Updating user:", id, "with data:", name, email);

        return {
            status: "success",
            updated_id: id,
            new_data: { name, email }
        };
    });
