const work = worker(); // Không name, không router, không cron

// THỰC THI: Chạy ngay lập tức khi file được nạp vào VM
// Ví dụ: Sửa lỗi dữ liệu hàng loạt trong một lần duy nhất

print("--- Đang thực hiện bản vá khẩn cấp ---");

let corrupted = db().from("users").where(status == "unknown").get();

corrupted.each((user) => {
    db().from("users")
        .where(id == user.id)
        .update({ status: "active" });
});

print("Đã vá " + corrupted.len() + " người dùng.");

// Tự động giải phóng ID và thoát sau khi dòng cuối cùng kết thúc