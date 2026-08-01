# KITJS V2 — Architectural Extensions & Technical Addendum

> **Tài liệu bổ trợ kiến trúc chính thức:** `ideaship-extension.md`  
> **Hệ sinh thái:** Kitwork Engine / KitJS Runtime (`@kitwork/kitjs`)  
> **Tác giả & Đóng góp kiến trúc:** Huỳnh Nhân Quốc & AI Senior Language Architect

---

## 1. SO SÁNH & VAI TRÒ CỦA BỘ TÀI LIỆU KITJS V2

Hai bản tài liệu `ideaship.md` và `ideashipping.md` không cạnh tranh mà **bổ sung hoàn hảo cho nhau như 2 nửa của một hệ thống toàn vẹn**:

| Tiêu chí | `ideaship.md` (Specification) | `ideashipping.md` (Implementation Brief) |
| :--- | :--- | :--- |
| **Đối tượng hướng tới** | Lập trình viên, Designer, Người phát triển Ứng dụng | AI Coding Agent, Engine Core Developer |
| **Mục đích tối cao** | Định hình Cú pháp Ngôn ngữ, DX, Biến hệ thống & Semantics | Quy định Quy trình Refactor, Quản lý Bộ nhớ & CI Check |
| **Phạm vi tập trung** | Cú pháp `data-kit-*`, Modifier Pipeline, 3 Nhóm Binding, SSR DOM | Source-of-truth (`runtime.js`), Resource Dispose, 7 Pha Refactor |
| **Giá trị cốt lõi** | **Hiến pháp Ngôn ngữ (The Constitution)** | **Cẩm nang Thi công (The Construction Plan)** |

> **Bản mở rộng này (`ideaship-extension.md`)** đóng vai trò là cầu nối kỹ thuật chuyên sâu (Technical Addendum), làm mịn các điểm giao thoa giữa **Hiến pháp Ngôn ngữ** và **Cẩm nang Thi công**.

---

## 2. BẢNG NÂNG CẤP KIẾN TRÚC MỞ RỘNG (6 EXTENSION CAPABILITIES)

---

### Extension 1: Hybrid Reactive Dependency Engine (Bộ Theo Dõi Phản Ứng Kép)

Để đạt hiệu năng phản ứng tức thì (Pinpoint DOM Updates) mà **không cần Virtual DOM** và **không sử dụng `eval()`**, KitJS V2 áp dụng cơ chế Kép:

```text
Biểu thức AST Parse  ➔ [1. Static AST Identifiers Collection]
                              +
Biểu thức Evaluate   ➔ [2. Proxy Getter Interception]
                              │
                              ▼
           [Exact Scope Property ➔ Binding Record Mapping]
```

1. **Static AST Analysis (Mount Phase):** Parser trích xuất tĩnh các Identifier trong AST (`qty * price` ➔ đán dấu quan tâm 2 key: `qty`, `price`).
2. **Proxy Getter Interception (Eval Phase):** Khi biểu thức chạy, Proxy Scope tự động ghi nhận thuộc tính thực sự được đọc.
3. **Transaction Flush:** Khi biến `qty` thay đổi, Binding Registry **chỉ cập nhật duy nhất nút DOM chứa `qty * price`**, hoàn toàn không quét cây DOM hay chạy lại các biểu thức không liên quan.

---

### Extension 2: Kitwork Drive (Morphing) & Marker Integrity Contract

Khi Kitwork Drive thực hiện chuyển trang không nạp lại (SPA Navigation via Morphing), hệ thống tuân thủ hợp đồng bảo toàn ranh giới:

```html
<!-- Cấu trúc Marker Ranh Giới Danh Sách -->
<!--kit-for:start id=items-1-->
<li data-kit-item="items-1" data-kit-key="a">...</li>
<li data-kit-item="items-1" data-kit-key="b">...</li>
<!--kit-for:end id=items-1-->
```

#### Quy chuẩn Morphing:
1. **Bảo toàn Marker Node:** Trình Morphing **tuyệt đối không gỡ bỏ hay thay thế** các nút HTML Comment Marker (`<!--kit-for:start-->`, `<!--kit-for:end-->`).
2. **Keyed Identity Matching:** Morphing so sánh các phần tử danh sách theo bộ đôi `data-kit-item="<loop-id>"` và `data-kit-key="<id>"`.
3. **State & Listener Preservation:** Nếu `key="a"` xuất hiện ở cả trang cũ và trang mới, DOM element đó được **giữ nguyên trong bộ nhớ**, chỉ cập nhật các attribute bị thay đổi, giúp bảo toàn 100% Event Listeners, Focus và trạng thái Form.

---

### Extension 3: Error Boundary Recovery Protocol (`$error.recover()`)

Nâng cấp đối tượng `$error` trong Error Boundary (`data-kit-error="handleError($error)"`) để hỗ trợ khôi phục giao diện lập trình được:

```text
$error
├── cause       : Error / Exception gốc
├── directive   : Directive gây lỗi (ví dụ: "data-kit-click")
├── element     : DOM Element xảy ra sự cố
├── scope       : Scope path hiện tại
├── recover()   : Hàm khôi phục trạng thái
└── reset()     : Hàm xóa cờ lỗi của Component
```

#### Ví dụ giao diện nút bấm "Thử lại" (Retry UX):
```html
<div data-kit-component="user-profile" data-kit-error="handleError($error)">
  
  <!-- Hiển thị khung lỗi khi xảy ra sự cố -->
  <div data-kit-show="$error" class="error-banner bg-red-50 p-4 rounded">
    <p>Không thể tải dữ liệu: <span data-kit-text="$error.cause.message"></span></p>
    <!-- Nút bấm khôi phục lại Component mà không cần reload trang -->
    <button type="button" data-kit-click="$error.recover()">Thử lại</button>
  </div>

  <!-- Nội dung chính -->
  <div data-kit-show="!$error">
    <h3 data-kit-text="userName"></h3>
  </div>

</div>
```

---

### Extension 4: Security Execution Boundary & Whitelist Global An Toàn

Để kiên định với triết lý **Zero-Eval và CSP Strict**, bộ nhân Evaluator của KitJS V2 chỉ cho phép biểu thức inline truy cập danh sách **14 Đối Tượng Global An Toàn Cho Phép (Whitelisted Globals)**:

```text
1. Math                   8. Object.keys
2. Date                   9. parseInt
3. JSON                  10. parseFloat
4. Number                11. encodeURIComponent
5. String                12. decodeURIComponent
6. Boolean               13. isNaN
7. Array                 14. isFinite
```

#### Quy tắc chặn (Security Constraints):
- **Bị cấm truy cập trực tiếp:** `window`, `document`, `location`, `top`, `parent`, `frames`, `history`, `XMLHttpRequest`, `fetch`, `Function`, `eval`, `WebSocket`.
- **Giải pháp thay thế:** Mọi thao tác tương tác phần cứng hoặc trình duyệt bắt buộc phải đi qua cầu nối **`$app` Capability Bridge** (`$app.clipboard()`, `$app.storage()`, `$app.fetch()`) hoặc thông qua **Component Methods**.

---

### Extension 5: Large List Virtualization (`data-kit-for:virtual`)

Đối với các danh sách siêu lớn (trên 10.000 phần tử), KitJS V2 cung cấp biến thể Virtualization tích hợp sẵn trong Block Engine:

```html
<ul data-kit-scope="{ bigItems: [...] }">
  <!-- Tự động ảo hóa: Chỉ render các phần tử nằm trong Viewport -->
  <li
    data-kit-for:virtual="item, index of bigItems"
    data-kit-key="item.id"
    data-kit-item-height="48"
  >
    <span data-kit-text="item.name"></span>
  </li>
</ul>
```

- **Cơ chế:** Block Engine dựa vào `IntersectionObserver` và tính toán cuộn để chỉ giữ $M$ phần tử hiển thị thực tế trên cây DOM (ví dụ 20 dòng), tự động điều chỉnh padding bù chiều cao.
- **Ưu điểm:** Giữ dung lượng RAM cực thấp và giữ tốc độ cuộn 60fps mượt mà cho danh sách khổng lồ.

---

### Extension 6: Runtime Inspection Hooks (`window.__KITWORK_DEVTOOLS__`)

Cung cấp cổng giao tiếp nhẹ (Zero-Overhead in Production) dành riêng cho Browser Extension DevTools của Kitwork Engine:

```js
window.__KITWORK_DEVTOOLS__ = {
  version: "2.0.0",
  getApps: function() { ... },
  getScopeTree: function(element) { ... },
  getBindingRegistry: function() { ... },
  highlightBoundaries: function() { ... }
};
```

- Ở môi trường **Production**: Cổng giao tiếp này tự động loại bỏ (Tree-shaken) hoặc vô hiệu hóa.
- Ở môi trường **Development**: Cung cấp khả năng soi cây Scope, xem chi tiết Binding Records, kiểm tra luồng Transaction và theo dõi thời gian thực thi của từng Directive.

---

## 3. TỔNG KẾT BỘ BA TÀI LIỆU CHUẨN MỰC KITJS V2

```text
  ┌──────────────────────────────────────────────────────────┐
  │                 KITJS V2 ARCHITECTURE                    │
  └────────────────────────────┬─────────────────────────────┘
                               │
       ┌───────────────────────┼───────────────────────┐
       ▼                       ▼                       ▼
┌───────────────┐       ┌───────────────┐       ┌───────────────┐
│  ideaship.md  │       │ideashipping.md│       │  extension.md │
│ (Specification)      │(Implementation│       │  (Technical   │
│               │       │    Brief)     │       │   Addendum)   │
└───────┬───────┘       └───────┬───────┘       └───────┬───────┘
        │                       │                       │
        ▼                       ▼                       ▼
   Hiến pháp           Kế hoạch thi công        Cầu nối & Tính năng
   Ngôn ngữ & DX       cho AI Coding Agent      mở rộng nâng cao
```

Bộ ba tài liệu này chính thức khép lại toàn bộ quá trình nghiên cứu, thiết kế và lập quy chuẩn kiến trúc cho **KitJS V2 Runtime**.
