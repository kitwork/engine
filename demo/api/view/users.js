
work("UserView")
    .get("/users", (ctx) => {
        var allUsers = db.from("user").get();
        // Mock tags for demo nested loop
        // Since db might not have tags, let's map it
        var enrichedUsers = allUsers.map(u => {
            u.tags = ["TagA_" + u.id, "TagB_" + u.id];

            return u;
        });

        return {
            currentUser: "SuperAdmin", // Global variable
            users: enrichedUsers
        };
    })
    .render("demo/view/users.html");
