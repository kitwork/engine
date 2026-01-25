# 🚀 Kitwork Engine - Ultra-Smart Query Builder

Kitwork Engine cung cấp một bộ SDK truy vấn cơ sở dữ liệu (Query Builder) mạnh mẽ, tối giản và thông minh bậc nhất, cho phép bạn viết các câu lệnh SQL phức tạp bằng cú pháp JavaScript thuần túy.

## 🌟 Tính năng nổi bật: "The Power of ONE"

Triết lý của Kitwork là sự tối giản. Bạn chỉ cần sử dụng hàm `.where()` cho hầu hết mọi nhu cầu truy vấn. Engine sẽ tự động suy luận (Inference) toán tử SQL phù hợp dựa trên dữ liệu bạn cung cấp.

### 1. Magic Lambda Syntax
Thay vì viết chuỗi văn bản dễ sai sót, Kitwork sử dụng hàm mũi tên (Lambda) để tương tác với các cột trong Database.

```javascript
// Tối giản, an toàn và hỗ trợ gợi ý code (Autocomplete)
db().table("user").where(u => u.username == "boss").get();
```

### 2. Thông minh hóa toán tử (Smart Detection)
Kitwork Engine tự động "dịch" mã JavaScript sang SQL dựa trên giá trị dữ liệu:

*   **Tự động nhận diện `LIKE`**: Khi giá trị chuỗi chứa ký tự `%`.
    ```javascript
    // Dịch thành: WHERE "username" LIKE 'Apple%'
    db().table("user").where(u => u.username == "Apple%").get();
    ```
*   **Tự động nhận diện `IN`**: Khi giá trị là một Mảng (Array).
    ```javascript
    // Dịch thành: WHERE "id" IN (10, 20, 30)
    db().table("user").where(u => u.id == [10, 20, 30]).get();
    ```

---

## 🛠 Hướng dẫn sử dụng chi tiết

### Truy vấn cơ bản
| Tính năng | Cú pháp | Kết quả SQL dự kiến |
| :--- | :--- | :--- |
| Tìm theo ID | `.find(1)` | `WHERE "id" = 1` |
| Lấy 1 bản ghi | `.first()` | `LIMIT 1` |
| Sắp xếp | `.orderBy("age", "DESC")` | `ORDER BY "age" DESC` |
| Phân trang | `.limit(10).offset(20)` | `LIMIT 10 OFFSET 20` |

### Các bộ lọc (Filters)
Ngoài toán tử `==` thông minh, Kitwork hỗ trợ đầy đủ các phép so sánh khác:

```javascript
db().table("products")
    .where(p => p.price > 1000)
    .where(p => p.stock <= 5)
    .where(p => p.status != "hidden")
    .get();
```

### Thống kê (Aggregates)
Hỗ trợ các phép tính toán ngay trên tầng Database:
```javascript
let stats = {
    total: db().table("orders").sum("amount"),
    average: db().table("products").avg("price"),
    max_score: db().table("players").max("score")
};
```

---

## 🔒 Bảo mật & Hiệu năng

*   **SQL Injection-Proof**: Kitwork sử dụng *Prepared Statements* ($1, $2, ...) cho mọi giá trị truyền vào. Dữ liệu của bạn luôn được tách biệt khỏi câu lệnh thực thi.
*   **Reflection-Powered**: Engine sử dụng kỹ thuật soi chiếu (Reflection) ở tầng Go để bóc tách dữ liệu JavaScript một cách chính xác nhất, đảm bảo tính ổn định tuyệt đối.
*   **Zero-Overhead**: Cú pháp Lambda được biên dịch trực tiếp sang cấu trúc query ở tầng Go, gần như không gây trễ cho VM.

---

## 🚀 Bắt đầu ngay

Định nghĩa API của bạn trong thư mục `demo/api` và tận hưởng trải nghiệm lập trình hiện đại:

```javascript
work("UserAPI")
    .get("/api/users", () => {
        const minAge = query("age") || 18;
        
        return db().table("user")
            .where(u => u.age >= minAge)
            .orderBy("age", "ASC")
            .get();
    });
```

---
*Kitwork Engine - Simple is the new Smart.*
