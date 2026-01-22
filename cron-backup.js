const w = work("DailyBackup");

// DISCOVERY: Define a schedule organism
w.daily("02:00");
w.timeout("1h");

print("Backup sequence engaged...");

let tables = ["users", "orders", "config"];

tables.each((name) => {
    let data = db().from(name).get();

    // Assume storage is a global capability
    storage.save("s3://backups/" + name + "/" + now().text() + ".dat", data);
});

print("Logic DNA execution complete. Organism going back to sleep.");
