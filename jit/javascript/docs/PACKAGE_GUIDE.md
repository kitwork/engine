# KitJS Package Documentation & Developer Guide

> **Runtime Version:** `0.9.0-next.12`  
> **Package Architecture:** Standalone HTML-First Browser Runtime & Go JIT Staged Delivery  
> **Profiles:** `kit.js` (Base Profile) & `hydrate.kit.js` (Hydrate Profile)  
> **Security Model:** Zero-eval Closed AST Sandbox · Fail-Closed · Zero Runtime Dependencies

---

## 1. Tong quan ve Goi (Package Overview)

**KitJS** la mot browser runtime cố tinh nhỏ, chủ quyền, HTML-first:
- **Zero Virtual DOM:** Tác động trực tiếp lên Real DOM (Direct Mutation), không tốn RAM/CPU tạo cây VDOM.
- **Zero `eval()` / Zero `new Function()`:** Ngôn ngữ biểu thức đóng (Closed Expression Language), an toàn tuyệt đối trước lỗ hổng XSS/RCE.
- **Single Delegated Event Listener:** Toàn bộ sự kiện trên trang được lắng nghe qua 1 listener duy nhất ở `document`, 0% rò rỉ bộ nhớ (Memory Leak Free).
- **Go JIT Staged Delivery:** Đóng gói và giao hàng theo nhu cầu trang web qua Go Engine (`router.jitjs()`) dưới dạng artifact content-addressed bất biến có mã SHA-256 và Subresource Integrity (SRI).

### Hai Profile Giao hang (Delivery Profiles)

| Profile | File Mac dinh | Thuat toan / Tinh nang mở rộng | Nhu cau su dung |
|---|---|---|---|
| **Kit** | `kit.js` | Scope, component, expression parser, directives, events, dirty boundary scheduler. | Trang web tĩnh hoặc SPA mini cần tương tác local state. |
| **Hydrate** | `hydrate.kit.js` | Bao gồm 100% Kit Profile + private Idiomorph MorphDOM & Drive navigation. | Trang web SSR/SPA cần chuyển trang không nháy (No White Flash) và giữ nguyên state/focus/scroll. |

> ⚠️ **Quy tắc bất biến:** Chỉ nhúng **1 trong 2 file** trên cùng một trang HTML. Không nạp song song cả hai file.

---

## 2. Public API Surface & Component Model

Runtime chỉ công khai **duy nhất 1 frozen global object** trên `globalThis.kit`:

```javascript
kit.version                     // Return exact SemVer string (e.g. "0.9.0-next.12")
kit.component(name, plainObject) // Register a plain-object component definition
```

### Component Definition Contract

Mọi component là một plain object được snapshot và đóng gói vào `Object.create(null)`:

```javascript
kit.component("counter", {
  // State khoi tao (Plain Data)
  count: 0,
  min: 0,
  max: 10,

  // Directives Method
  increment() {
    if (this.count < this.max) {
      this.count += 1;
    }
  },

  decrement() {
    if (this.count > this.min) {
      this.count -= 1;
    }
  },

  // Lifecycle Hook & Disposer
  init() {
    // Thuc thi khi component duoc mount vao DOM
    var timer = setInterval(() => {
      // Periodic check
    }, 1000);

    // Return a Teardown Disposer to cleanup when component unmounts
    return function dispose() {
      clearInterval(timer);
    };
  }
});
```

### Quy tac Reactivity Nông (Shallow Reactivity Rules)
KitJS sử dụng mô hình **Shallow Dirty-Bit Boundary Scheduler**:
- Cập nhật biến top-level (như `this.count = 5`) sẽ đánh dấu dirty-bit cho component boundary và gộp render qua `queueMicrotask`.
- **Đột biến sâu (Deep Mutation)** như `this.items.push(item)` **KHÔNG KÍCH HOẠT RE-RENDER**.
- Khuyến nghị kỷ luật gán nông (Shallow Assignment):
  ```javascript
  // CHUẨN: Gán nông để trigger render
  this.items = [...this.items, newItem];
  ```

---

## 3. Master Reference: 13 Directives (`data-kit-*`)

### A. Component Boundary & Metadata

| Directive | Dynamic Expression | Mô tả Contract |
|---|---|---|
| `data-kit-scope="..."` | State Map | Tạo một anonymous shallow store hoặc truyền state khởi tạo cho component trên cùng phần tử. Cú pháp: `count: 0; open: false;`. |
| `data-kit-component="..."` | Component Name | Đăng ký một component host đã được định nghĩa qua `kit.component(name, ...)`. |
| `data-kit-version="..."` | SemVer String | Tùy chọn xác thực phiên bản SemVer chính xác với sealed artifact manifest. |
| `data-kit-as="..."` | `$aliasName` | Gán một bí danh action-only cho Component Instance (ví dụ `data-kit-as="$theme"` $\rightarrow$ `$theme.toggle()`). |
| `data-kit-alias="..."` | `$aliasName` | Gán một bí danh action-only cho DOM Element (ví dụ `data-kit-alias="$searchInput"` $\rightarrow$ `$searchInput.focus()`). |
| `data-kit-retain="..."` | Retain Key | Trong Hydrate profile, giữ nguyên component host và live store hiện tại qua các nhịp Morph. Key là duy nhất và khác với HTML `id`. |
| `data-kit-ignore` | Static Marker | Bỏ qua phần tử và toàn bộ cây con. Cả Kit scanner lẫn Hydrate Morph đều không đụng vào cây con này. |

### B. State Bindings & Presentation

| Directive | Dynamic Expression | Mô tả Contract |
|---|---|---|
| `data-kit-text="..."` | Expression | Cập nhật an toàn qua `textContent`. |
| `data-kit-show="..."` | Boolean Expression | Bật/tắt thuộc tính `hidden` của phần tử mà không xóa khỏi DOM. |
| `data-kit-bind="..."` | `attr: expr;` | Gán các thuộc tính HTML an toàn (như `disabled`, `aria-expanded`, `href`). Bỏ qua các URL scheme nguy hiểm (`javascript:`, `data:`). |
| `data-kit-class="..."` | Class Expression | Quản lý danh sách class động dựa trên điều kiện, giữ nguyên các class static có sẵn. |
| `data-kit-style="..."` | `prop: expr;` | Quản lý các giá trị CSS liên tục an toàn (ví dụ: `width: progress + '%';`). Bị giới hạn 128 entries. |
| `data-kit-model="..."` | Field Name | Binding 2 chiều cho form control (`<input>`, `<select>`, `<textarea>`). Chỉ nhận tên field top-level khớp `/^[A-Za-z_][A-Za-z0-9_]*$/`. |

### C. Control Flow & Structural Directives

| Directive | Dynamic Expression | Mô tả Contract |
|---|---|---|
| `data-kit-if="..."` | Boolean Expression | Thêm hoặc xóa phần tử khỏi DOM dựa trên kết quả điều kiện. Dùng `<template data-kit-if="...">`. |
| `data-kit-for="..."` | `item in list` | Lặp mảng dữ liệu. Khuyến khích dùng kèm `data-kit-key="item.id"` để giữ nguyên DOM identity khi danh sách thay đổi. |

### D. Event Modifiers (11 Modifiers)

Event directive dùng cú pháp `data-kit-<event>[.modifier]="action()"`:

| Modifier | Hành vi Kỹ thuật |
|---|---|
| `:self` | Chỉ chạy action khi `event.target === $element` (Dùng cho Modal Backdrop). |
| `:prevent` | Tự động gọi `event.preventDefault()`. |
| `:stop` | Tự động gọi `event.stopPropagation()`. |
| `:once` | Chỉ thực thi action 1 lần duy nhất rồi hủy binding. |
| `:outside` | Chạy action khi nhấp chuột bên ngoài phần tử (Dùng cho Dropdown / Popover). |
| `:enter` | Chỉ kích hoạt khi nhấn phím `Enter` (KeyCode 13). |
| `:escape` | Chỉ kích hoạt khi nhấn phím `Escape` (KeyCode 27). |
| `:window` | Lắng nghe sự kiện trên `window`. |
| `:document` | Lắng nghe sự kiện trên `document`. |
| `:debounce(ms)`| Hoãn thực thi action trong N mili-giây kể từ lần kích hoạt cuối. |
| `:throttle(ms)`| Giới hạn tần suất thực thi action tối đa 1 lần trong mỗi N mili-giây. |

---

## 4. Catalog: 10 Sealed Infrastructure Services (`kit.*`)

Các service là những module hạ tầng độc lập được Go Engine đóng gói niêm phong vào file artifact:

1. **`kit.drive`**: Động cơ SPA Navigation, Hover Prefetching, Link Interception & Idiomorph DOM Morphing.
2. **`kit.storage`**: Adapter lưu trữ Async Key-Value an toàn (LocalStorage / IndexedDB / Chrome Extension Storage).
3. **`kit.request`**: Client HTTP Request Engine tích hợp CSRF header, Timeout và Retry.
4. **`kit.cookie`**: Adapter đọc/ghi Cookie an toàn.
5. **`kit.clipboard`**: Helper sao chép và đọc dữ liệu Clipboard khép kín (`kit.clipboard.copy(text)`).
6. **`kit.fullscreen`**: Động cơ quản lý chế độ Toàn màn hình (Fullscreen API).
7. **`kit.network`**: Theo dõi trạng thái kết nối mạng Online/Offline (`kit.network.online`).
8. **`kit.navigation`**: Trình quản lý lịch sử duyệt trang và URL query params.
9. **`kit.share`**: Integration API cho Web Share Native của thiết bị di động.
10. **`kit.announce`**: Service phát bản tin hỗ trợ bộ đọc màn hình Accessibility (ARIA Live Region Announcer).

---

## 5. An toan & Rao chan Sandbox (Security Boundaries)

KitJS được thiết kế với tư duy **Security by Construction (Bảo mật bằng Cấu trúc)**:

- **Zero-Eval Sandbox:** Không bao giờ gọi `eval()`, `new Function()`, hay `setTimeout("string")`.
- **Closed AST Parser:** Mọi biểu thức HTML chỉ được phân tích qua bộ parser đóng 2 pass (`parser.js` $\rightarrow$ `evaluator.js`).
- **Danh sách Chặn Prototype Pollution (`BLOCKED` & `FORBIDDEN`):**
  - Chặn triệt để: `constructor`, `prototype`, `__proto__`, `ownerDocument`, `defaultView`, `contentWindow`, `window`, `globalThis`, `top`, `parent`, `self`, `document`, `location`, `navigator`, `Function`, `eval`.
- **Execution Budget:**
  - Giới hạn tối đa **10.000 AST Node Visits** per evaluation.
  - Giới hạn tối đa **64 Call Depth** lồng nhau.
- **Rollback Transaction:** Nếu một action phức tạp gặp lỗi ở giữa, toàn bộ gán state trước đó trong nhịp đó sẽ được **Rollback 100%**, giữ cho trạng thái ứng dụng luôn nhất quán.

---

## 6. Mo hinh Giao hang Go JIT Staged Delivery

Trong hệ sinh thái Kitwork Engine, KitJS không nạp qua npm hay CDN công cộng, mà được giao hàng qua đường JIT Server:

```javascript
// router.kitwork.js trong tenant site
router.jitjs({
  components: {
    counter: {
      version: "1.0.0",
      source: "./components/counter.js"
    }
  }
});
```

Go Engine sẽ tự động:
1. Quét cây DOM HTML đã chuẩn bị.
2. Phân tích đồ thị phụ thuộc (Dependency Graph) của các component và service.
3. Đóng gói các script với chuẩn **Content-Addressed SHA-256 Hash**:
   ```html
   <script data-kitwork-jit="runtime" src="/jit/<hash>.runtime.js" integrity="sha256-..." defer></script>
   <script data-kitwork-jit="hydrate" src="/jit/<hash>.hydrate.js" integrity="sha256-..." defer></script>
   <script data-kitwork-jit="graph" src="/jit/<hash>.graph.js" integrity="sha256-..." defer></script>
   <script data-kitwork-jit="service" src="/jit/<hash>.progress.js" integrity="sha256-..." defer></script>
   <script data-kitwork-jit="component" src="/jit/<hash>.counter.js" integrity="sha256-..." defer></script>
   ```
4. Bảo đảm thứ tự nạp thẻ `defer` chính xác 100%, có mã hóa kiểm tra toàn vẹn SRI (`integrity`), ngăn chặn mọi nguy cơ can thiệp mã nguồn trên đường truyền.
