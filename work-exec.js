const w = work(); // Anonymous, immediate task

// Task for data migration or emergency patching
print("--- [", now().text(), "] Starting Emergency Patch ---");

let corrupted = db().from("users").where(status == "unknown").take(100);

// Chaining to get length and format
print("Detected", corrupted.len().string(), "corrupted records.");

corrupted.get().each((user) => {
    db().from("users")
        .where(id == user.id)
        .update({ status: "active", patched: true });
});

print("Patching complete.");
// Work organism will die automatically after this line execution