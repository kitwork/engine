# KITJS V2 — Master Architecture Specification Matrix

> **Tài liệu Ma trận Quy chuẩn Kiến trúc Toàn cảnh:** `ideaship-master.md`  
> **Dự án:** KitJS Runtime (`@kitwork/kitjs`)  
> **Hệ sinh thái:** Kitwork Engine  
> **Tác giả & Kiến trúc sư:** Huỳnh Nhân Quốc  
> **Trạng thái:** **FROZEN SPECIFICATION FOR IMPLEMENTATION**

---

## 1. BẢNG CÚ PHÁP THUỘC TÍNH NATIVE HTML (CORE DIRECTIVES)

| Thuộc tính (Directive) | Mục đích sử dụng | Cú pháp ví dụ | Ghi chú & Semantics Runtime |
| :--- | :--- | :--- | :--- |
| **`data-kit-component`** | Khai báo phần tử là một Component | `data-kit-component="modal"` | Gắn hành vi có tên và khả năng tái sử dụng |
| **`data-kit-scope`** | Khởi tạo phạm vi dữ liệu | `data-kit-scope="qty: 1; price: 250"` | Hỗ trợ CSS-Style shorthand & Object Literal `{}` |
| **`data-kit-alias`** | Đặt tên truy cập trực tiếp | `data-kit-alias="$paymentModal"` | Nằm ở `$` global namespace. Không trùng Alias khác |
| **`data-kit-ref`** | Đặt tên đăng ký trong `$refs` | `data-kit-ref="paymentModal"` | Nằm ở `$refs` registry namespace. `$paymentModal === $refs.paymentModal` |
| **`data-kit-show`** | Điều khiển ẩn/hiển phần tử | `data-kit-show="isOpen"` | Bật/tắt thuộc tính native `hidden` W3C (giữ CSS Flex/Grid) |
| **`data-kit-text`** | Binding văn bản phản ứng | `data-kit-text="qty * price"` | Ghi bằng `textContent`, không quét text node, không dùng `${}` |
| **`data-kit-bind:<prop>`** | Binding thuộc tính (Canonical) | `data-kit-bind:disabled="loading"` | Cú pháp chuẩn. Cập nhật DOM Property hoặc Attribute theo Metadata |
| **`data-kit-attr:<name>`** | Binding HTML Attribute thuần | `data-kit-attr:aria-expanded="open"` | Dành cho ARIA và custom attribute (`setAttribute`) |
| **`data-kit-model`** | Binding dữ liệu 2 chiều | `data-kit-model="user.name"` | Two-way binding cho `<input>`, `<select>`, `<textarea>` |
| **`data-kit-for`** | Khai báo danh sách (Source) | `data-kit-for="item, index of items"` | Chỉ xuất hiện ở Source code HTML tác giả viết |
| **`data-kit-item`** | Đánh dấu item đã SSR | `data-kit-item="items-1"` | Chỉ xuất hiện ở HTML sau khi Go Engine render ra browser |
| **`data-kit-key`** | Định danh dữ liệu ổn định | `data-kit-key="item.id"` | Giữ phần tử DOM ổn định khi Insert/Remove/Reorder |
| **`data-kit-if`** | Bật/tắt subtree trong DOM | `data-kit-if="isEditing"` | Mount/Unmount phần tử khỏi DOM (dùng chung Block Engine) |
| **`data-kit-error`** | Xử lý lỗi (Error Boundary) | `data-kit-error="handleError($error)"` | Bắt exception của Component, truyền vào đối tượng `$error` |

---

## 2. BẢNG BỘ BIẾN HỆ THỐNG CỐ ĐỊNH (MAGIC SYSTEM VARIABLES)

| Biến Hệ Thống | Ý Nghĩa Kỹ Thuật | Phạm Vi Tiếp Cận (Target) | Phương Thức Trợ Lý |
| :--- | :--- | :--- | :--- |
| **`$this`** 🏆 | Thẻ sở hữu handler (`directive owner`) | Thẻ HTML chứa chỉ thị (ví dụ `<button>`) | Tương đương `event.currentTarget` trong direct listener |
| **`$host`** 🏆 | Boundary ranh giới Scope gần nhất | Thẻ bọc Scope gần nhất (`<section>`) | Giúp truy cập DOM container của Scope hiện tại |
| **`$event`** | Native DOM Event | Sự kiện Native DOM (`click`, `input`...) | `$event.target`, `$event.preventDefault()`, `$event.submitter` |
| **`$error`** | Đối tượng Error Boundary | Khối chứa directive `data-kit-error` | `$error.cause`, `$error.directive`, `$error.element`, **`$error.recover()`** |
| **`$refs`** | Scoped Reference Registry | Bộ sưu tập Component Refs trong app | `$refs.modalName.open = true` |
| **`$app`** | Host Capability Bridge Portal | Cầu nối Native Bridge / Phần cứng | `$app.camera()`, `$app.qrcode()`, `$app.clipboard()`, `$app.storage()` |
| **`$` / `$root`** | Application / Page Root State | Trạng thái gốc phản ứng của toàn trang | `$.cartCount++` hoặc `$root.cartCount++` |

---

## 3. BẢNG ĐƯỜNG ỐNG SỰ KIỆN (DETERMINISTIC MODIFIER PIPELINE ORDER)

Mọi modifier đính kèm sau chỉ thị sự kiện (ví dụ `data-kit-click:outside:stop:prevent="open()"`) luôn thực thi theo **thứ tự đường ống cố định**:

```text
  [ Native DOM Event Triggered ]
               │
               ▼
   1. Target Modifier          (:window, :document)   ➔ Gán listener vào Window hoặc Document
               │
               ▼
   2. Filter Modifier          (:outside, :escape)    ➔ Kiểm tra điều kiện lọc. Nếu FAIL ➔ THỦY PIPELINE
               │
               ▼
   3. Prevent Default          (:prevent)             ➔ Gọi event.preventDefault()
               │
               ▼
   4. Propagation Control      (:stop)                ➔ Gọi event.stopPropagation()
               │
               ▼
   5. Timing Control           (:debounce, :throttle) ➔ Kiểm tra Timer / Hoãn nhịp gõ
               │
               ▼
   6. Lifecycle Control        (:once)                ➔ Đánh dấu tự gỡ Listener sau 1 lần
               │
               ▼
   7. Execute Expression                              ➔ Thực thi biểu thức JS / Component Method
```

---

## 4. BẢNG HỢP ĐỒNG BINDING THUỘC TÍNH (3 BINDING GROUPS)

| Nhóm Chiến Lược Binding | Thuộc tính HTML áp dụng | Cơ chế cập nhật DOM bên dưới |
| :--- | :--- | :--- |
| **1. Reflected Boolean** | `disabled`, `required`, `readonly`, `multiple`, `hidden`, `open` | Ghi đồng thời cả DOM Property + HTML Attribute (`element.toggleAttribute(...)`) |
| **2. Live State Property** | `checked`, `selected`, `value`, `indeterminate` | Chỉ cập nhật DOM Property hiện tại (`element.checked = val`), giữ nguyên Form Reset Attribute |
| **3. Attribute-Only** | `data-kit-attr:aria-expanded`, `data-kit-attr:data-state` | Chỉ cập nhật HTML Attribute (`element.setAttribute(...)`) |

---

## 5. BẢNG HỢP ĐỒNG DANH SÁCH (AUTHORED VS SSR OUTPUT)

```text
                             HTML TÁC GIẢ VIẾT (SOURCE)
                                         │
                                         ▼
                            [ Go Engine SSR Compiler ]
                                         │
                                         ▼
                          HTML SSR KẾT QUẢ (BROWSER RENDER)
```

```html
<!-- SOURCE CODE (Code tác giả viết) -->
<ul>
  <li data-kit-for="item, index of items" data-kit-key="item.id">
    <span data-kit-text="item.name"></span>
  </li>
</ul>

<!-- SSR OUTPUT (Kết quả Go Engine render ra trình duyệt) -->
<ul>
  <!--kit-for:start id=items-1-->
  <li data-kit-item="items-1" data-kit-key="a">
    <span data-kit-text="item.name">Item A</span>
  </li>
  <li data-kit-item="items-1" data-kit-key="b">
    <span data-kit-text="item.name">Item B</span>
  </li>
  <!--kit-for:end id=items-1-->
</ul>
```

---

## 6. BẢNG ĐIỀU KIỆN KỸ THUẬT VÀ QUY CHUẨN BUILD (ENGINEERING CONSTRAINTS)

| Tiêu chuẩn Kỹ thuật | Quy định chính thức | Mục đích & Lợi ích |
| :--- | :--- | :--- |
| **Security Execution** | Không `eval()`, không `new Function()` | Tuân thủ 100% môi trường Strict CSP, chống Injection |
| **Safe Whitelist Globals** | Chỉ cho phép 14 đối tượng JS cơ bản (`Math`, `Date`, `JSON`...) | Chặn đứt Prototype Pollution & truy cập `window` tự do |
| **Bundle Size Budget** | Core Kernel <= 12KB gzip (Kiểm tra bằng CI) | Tải trang siêu tốc, tiết kiệm băng thông |
| **Source of Truth** | Duy nhất tại `engine/jit/hydrate/runtime.js` | Không sửa file generated `dist/kitjs.js`, build qua `cmd/kitjs-dist` |
| **Server Twin Conformance** | Go Evaluator & JS Evaluator chạy chung Conformance Suite | Đảm bảo kết quả tính toán giữa Server và Client trùng khớp 100% |
| **Hydration Contract** | *"Server Content First, Client Reactive State Ownership"* | Giữ DOM tĩnh SSR ban đầu, tiếp quản Reactivity từ mutation đầu tiên |
