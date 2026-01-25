work("UserAPI")
    .router("GET", "/api/db/users").handle(() => {

        const id = query("id")


        return db().table("user")
            .where(u => u.id == id)
            .get();

    });
