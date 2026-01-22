const work = worker({ name: "DatabaseBackup" });

// CHỈ THỊ: Engine tự nhận diện đây là CRON vì không có router
work.daily("01:00");
work.timeout("30m"); // Backup có thể tốn thời gian

print("--- Bắt đầu sao lưu hệ thống ---");

let tables = ["users", "orders", "transactions"];

tables.each((table) => {
    let data = db().from(table).get();
    storage.save("backups/" + table + "_" + time.now() + ".json", data);
});

print("--- Sao lưu hoàn tất ---");
// Cron không cần return vì không có client nhận kết quả