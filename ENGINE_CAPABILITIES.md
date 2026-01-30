# 🚀 Kitwork Engine: Advanced Capabilities Guide

Tài liệu này hướng dẫn các tính năng cao cấp của Kitwork Engine - Hệ thống thực thi workflow ưu tiên hiệu năng và tận dụng tối đa sức mạnh của Hệ điều hành.

## 1. Kiểm soát luồng thực thi (Execution Control)

### Done & Fail Hooks (Lifecycle)
Quản lý trạng thái kết thúc của task một cách tường minh, tách biệt logic nghiệp vụ và logic hậu mãi.

*   **`done(callback)`**: Chạy khi task hoàn tất thành công.
*   **`fail(callback)`**: Chạy khi task gặp lỗi hoặc gọi hàm `fail()`.

```javascript
work("user.create")
    .handle((req) => {
        if (!req.body().name) fail("Missing Name");
        return db.user.insert(req.body());
    })
    .done((res) => log(`Thành công: ${res.id}`))
    .fail((err) => log(`Lỗi hệ thống: ${err}`));
```

---

## 2. Hệ thống Cache & Stacking

### Cache (RAM-based)
Lưu kết quả trong bộ nhớ đệm LRU. Thích hợp cho dữ liệu nhỏ, cần tốc độ cực cao.
*   **Cú pháp**: `.cache("5s")` hoặc `.cache(60)` (giây).

### Static (Disk-based Snapshot)
"Tĩnh hóa" kết quả của Script ra đĩa cứng dưới dạng file tĩnh. Tận dụng **Metadata (ModTime)** của OS.
*   **Cú pháp**: `.static("1h")`.
*   **An toàn**: `.static({ duration: "1h", check: true })` - Bật tính năng **Checksum (Sha256)** để đảm bảo tính toàn vẹn của dữ liệu trên đĩa.

---

## 3. Tài nguyên Tĩnh (Unified Assets Serving)

Tự động nhận diện và phục vụ tài nguyên từ đĩa cứng với tốc độ **Zero-VM** (không chạy Script).

### Assets (Smart Resource Mapping)
Hàm `.assets()` là hàm đa năng, tự động nhận diện đường dẫn là File hay Thư mục.

*   **Single File**:
    ```javascript
    work("logo").router("GET", "/logo.png").assets("./public/img/logo.png");
    ```
*   **Directory (Kho tài nguyên)**:
    ```javascript
    work("static").router("GET", "/static/*").assets("./dist/static");
    ```

*Lưu ý: Bạn cũng có thể dùng `.file()` như một bí danh (alias) của `.assets()` nếu muốn code rõ nghĩa hơn khi trỏ tới 1 file duy nhất.*

---

## 4. Xử lý dữ liệu Functional

Kitwork hỗ trợ các hàm biến đổi dữ liệu bậc cao ngay trong Core, chạy với hiệu năng Bytecode tối ưu.

*   **`.map(item => ({...}))`**: Biến đổi danh sách.
*   **`.filter(item => condition)`**: Lọc danh sách.
*   **`.find(item => condition)`**: Tìm kiếm phần tử.

**QUAN TRỌNG**: Khi trả về Object trong Arrow Function, bắt buộc dùng ngoặc đơn: `item => ({ id: item.id })`.

---

## 5. Triết lý Thiết kế (Developer Mindset)

1.  **Fast-Path First**: Nếu có thể dùng `.assets()` hoặc `.static()`, hãy dùng chúng để bypass bộ máy Script.
2.  **Explicit Exit**: Dùng `done()` và `fail()` thay vì lồng `if-else`.
3.  **OS-Native Integrity**: Tận dụng File Metadata của OS là cách tốt nhất để quản lý Cache bền bỉ và hiệu quả.

---
*Tài liệu này được biên soạn cho Kitwork Engine v1.5.0+*
