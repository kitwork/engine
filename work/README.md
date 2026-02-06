# Kitwork Work: The Engine of Creation

> **"Simplicity is the ultimate sophistication."**

**Kitwork Work** is designed for the **Flow State**.
We removed the boilerplate, the configuration hell, and the type bureaucracy. What remains is pure Intent.

Here, you don't write "infrastructure code". You write **Action**.

---

## ⚡ The Developer Experience (DX)

### 1. Database: Native Functionality
Forget SQL strings. Forget ORM migrations. Just talk to your data.

#### **Find & Read**
```javascript
// Get one admin user
var admin = db.users.find(u => u.role == "admin");

// Get active members, sorted
var members = db.users
    .where(u => u.status == "active" && u.points > 100)
    .orderBy("points", "desc")
    .limit(20)
    .list();
    
// Search / Like
var results = db.products.where(p => p.name.like("iPhone%")).list();
```

#### **Write & Create**
```javascript
// Create a new order
var order = db.orders.create({
    user_id: user.id,
    total: 99.00,
    status: "pending"
});

// Update specific fields
db.orders
    .where("id", order.id)
    .update({ status: "paid" });
    
// Soft Delete (Safe)
db.users.find(1).delete();
```

### 2. Networking: The World is an Object
Interacting with external APIs should be as simple as calling a function.

```javascript
// Simple Fetch
var data = http.get("https://api.github.com/users/kitwork");

// Posting JSON
var resp = http.post("https://slack.com/api/chat", {
    channel: "#alerts",
    text: "Server is healthy!"
});

// Handling Errors naturally
if (resp.status != 200) {
    print("Failed to notify Slack: " + resp.body);
}
```

### 3. Logic & Response: Minimalist
```javascript
// Return JSON API response
return {
    success: true,
    data: members,
    meta: { count: members.length }
};

// Or Render HTML View
return view("dashboard/index", { user: admin });
```

---

## 🏛 The Philosophy of Simplicity (Sự Đơn Giản)

Tại sao code lại ngắn gọn như vậy?

1.  **Implicit Context**: Bạn không cần truyền `ctx context.Context` hay `db *sql.DB` vào mọi hàm. Engine tự biết bạn là ai, bạn đang ở Tenant nào, và bạn có quyền truy cập DB nào.
2.  **Smart Proxy**: `u => u.role == "admin"` không phải là filter trên RAM. Nó được biên dịch thành `SELECT * FROM users WHERE role = 'admin'`. Hiệu năng Native SQL với cú pháp JS.
3.  **Human-Centric Names**: `.find()`, `.create()`, `.update()`. Không phải `.SelectOne()`, `.InsertStatement()`. Chúng ta dùng từ ngữ của con người.

> *"Code ít hơn, làm được nhiều hơn, và hạnh phúc hơn."*

---

## 🛡 Safety & Responsibility (Trách Nhiệm)

Đơn giản không có nghĩa là lỏng lẻo. Mỗi dòng code đơn giản trên đều được bao bọc bởi lớp bảo vệ nghiêm ngặt nhất:

*   **Auto-Sanitization**: Không bao giờ bị SQL Injection.
*   **Energy Metering**: Mỗi lệnh `.list()` hay `.fetch()` đều bị tính phí Energy. Code ngắn gọn giúp bạn dễ dàng nhìn thấy mình đang tiêu tốn tài nguyên ở đâu.
*   **Isolation**: Bạn chỉ thấy data của mình. Bạn không thể vô tình query nhầm sang bảng của Tenant khác.

**Kitwork Work** giúp bạn trở thành một Lập trình viên có trách nhiệm mà không cần phải nỗ lực quản lý hạ tầng.

---
*© Kitwork Project - The Standard Library of Action*
