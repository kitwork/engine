// Ultimate Shorthand API
work("UserAPI").router("GET", "/users/top");

// Chaining database result directly as a JSON response
return db().from("users").take(10);
