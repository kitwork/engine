work("UserAPI")
    .router("GET", "/api/db/users");

return db().table("user")
    .where(u => u.is_active == true)
    .get();
