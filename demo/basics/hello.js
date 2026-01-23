// ==========================================
// BASICS: HELLO WORLD
// ==========================================

log("👋 Hello from Kitwork Engine!");

let user = {
    name: "NewUser",
    role: "admin",
    active: true
};

log("📝 User Info:", user);

// Return simple JSON
return {
    message: "Success",
    data: user
};
