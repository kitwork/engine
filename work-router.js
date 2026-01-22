const w = work({ name: "OrderSystem" });

// CHỈ THỊ: Engine tự nhận diện đây là ROUTER
w.router("POST", "/users");


return db().from("users").take(10);