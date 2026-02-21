const w = work("OrderProcessor")
    .router("POST", "/v1/process")
    .version("1.5.0");

let input = payload();
log("🚀 Starting process for user:", input.user_id);

// 1. Kiểm tra database
let user = db().table("user").where("id", input.user_id).get();

if (user.len() == 0) {
    return { status: 404, error: "User not found" };
}

// 2. Gọi API tỷ giá ngoại tệ
let fx = http().get("https://api.exchangerate.host/latest");
log("💹 FX Status:", fx.status);

// 3. Tính toán và lưu trữ
// Giả sử FX = 25000, vì Mock HTTP chưa trả về tỷ giá thực
let rate = 25000;
let total_vnd = input.amount * rate;

// Mock transaction table insert
db().transactions.insert({ user_id: input.user_id, amount: total_vnd });

log("✅ Done!");
return { order_id: now().text(), total: total_vnd };