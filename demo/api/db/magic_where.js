work("db_magic")
    .router("GET", "/api/db/magic");

// SQL generated: SELECT * FROM "user" WHERE "username" = $1
let users = db().table("user")
    .where(u => u.username == "bob")
    .limit(10)
    .get();

return {
    query: "Magic Lambda Where",
    result: users
};
