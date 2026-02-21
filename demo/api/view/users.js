
work("UserView")
    .get("/users", (ctx) => {
        return { users: db.user.get() };

    })
    .render("demo/view/users.html");

