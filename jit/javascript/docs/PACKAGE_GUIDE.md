# KitJS Package Documentation & Developer Guide

> **Source Candidate Runtime:** `1.0.0-rc.2` (unpublished)
> **Package Architecture:** Standalone HTML-First Browser Runtime & Go JIT Staged Delivery  
> **Profiles:** `kit.js` (Base Profile) & `hydrate.kit.js` (Hydrate Profile)  
> **Security Model:** Zero-eval Closed AST Sandbox · Fail-Closed · Zero Runtime Dependencies

---

## 1. Tong quan ve Goi (Package Overview)

**KitJS** la mot browser runtime cố tinh nhỏ, chủ quyền, HTML-first:
- **Không dùng Virtual DOM:** Binding và directive cập nhật trực tiếp Real DOM, không tạo một cây VDOM trung gian.
- **Không dùng `eval()` / `new Function()` cho authored expression:** Biểu thức được parse và evaluate bằng một ngôn ngữ đóng, fail-closed khi gặp cú pháp hoặc quyền không được hỗ trợ. Đây không phải cam kết rằng ứng dụng không thể có XSS hay lỗi logic.
- **Delegated events theo loại:** Runtime cài một listener ở `document` cho mỗi event type được hỗ trợ và giải quyết action từ DOM đang kết nối. Trusted component có thể dùng `init(context).listen(...)` để gắn listener được dispose cùng boundary.
- **Go JIT Staged Delivery:** Đóng gói và giao hàng theo nhu cầu trang web qua Go Engine (`router.jitjs()`) dưới dạng artifact content-addressed bất biến có mã SHA-256 và Subresource Integrity (SRI).

### Hai Profile Giao hang (Delivery Profiles)

| Profile | File Mac dinh | Thuat toan / Tinh nang mở rộng | Nhu cau su dung |
|---|---|---|---|
| **Kit** | `kit.js` | Scope, component, expression parser, directives, events, dirty boundary scheduler. | Trang web tĩnh hoặc SPA mini cần tương tác local state. |
| **Hydrate** | `hydrate.kit.js` | Bao gồm toàn bộ Kit Profile cùng private Morph và Drive document continuity. | Link cùng origin và form GET đủ điều kiện có thể chuyển trang bằng Drive/Morph; trường hợp không tương thích quay về normal navigation. |

> ⚠️ **Quy tắc bất biến:** Chỉ nhúng **1 trong 2 file** trên cùng một trang HTML. Không nạp song song cả hai file.

---

## 2. Public API Surface & Component Model

Hai standalone profile chỉ công khai **một frozen global object** trên `globalThis.kit`:

```javascript
kit.version                     // Return exact SemVer string (e.g. "1.0.0-rc.2")
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

## 3. Directive Contract (`data-kit-*`)

### A. Component Boundary & Metadata

| Directive | Dynamic Expression | Mô tả Contract |
|---|---|---|
| `data-kit-scope="..."` | State Map | Tạo một anonymous shallow store hoặc truyền state khởi tạo cho component trên cùng phần tử. Cú pháp: `count: 0; open: false;`. |
| `data-kit-component="name"` | Component Name | Tạo host cho component đăng ký trực tiếp bằng `kit.component(name, ...)`. |
| `data-kit-component="name@exact-semver"` | Managed Identity | Khẳng định identity của managed closed-graph component; authored HTML không tự tải package. |
| `data-kit-as="..."` | `$aliasName` | Gán một bí danh action-only cho Component Instance (ví dụ `data-kit-as="$theme"` $\rightarrow$ `$theme.toggle()`). |
| `data-kit-retain="..."` | Retain Key | Trong Hydrate profile, giữ component host và live store khi phía incoming có cùng key cùng namespace, tag, component identity, version và alias. Key phải duy nhất; host không được lồng nhau hoặc nằm trong template/structural region. |
| `data-kit-ignore` | Static Marker | Kit scanner không mount cây con. Morph chỉ giữ nguyên boundary khi cả node hiện tại và incoming tương ứng đều có marker; thêm/bỏ marker sẽ thay boundary, còn thiếu counterpart vẫn bị remove bình thường. |

Dạng tách `data-kit-version` đã bị loại bỏ và fail closed ngoài vùng
`data-kit-ignore`. Managed component phải đặt exact version trực tiếp trong
`data-kit-component`; direct client component dùng tên không version và không
cần marker riêng.

### B. State Bindings & Presentation

| Directive | Dynamic Expression | Mô tả Contract |
|---|---|---|
| `data-kit-text="..."` | Expression | Cập nhật an toàn qua `textContent`. |
| `data-kit-show="..."` | Boolean Expression | Bật/tắt thuộc tính `hidden` của phần tử mà không xóa khỏi DOM. |
| `data-kit-bind:<name>="expr"` | một biểu thức | Một ràng buộc, một đích ghi ngay trong tên thuộc tính: `data-kit-bind:disabled="busy"`, `data-kit-bind:aria-expanded="open"`. Ba nhóm theo tên: reflected boolean (`disabled` `required` `readonly` `multiple` `hidden` `open`) ghi cả property lẫn attribute; live property (`checked` `selected` `value` `indeterminate`) chỉ ghi property để form reset còn giá trị tác giả; tên có gạch nối hoặc phần tử không có property → attribute, `aria-*` ra "true"/"false", boolean khác có/không có. URL-valued binding từ chối tiền tố `javascript:`, `vbscript:` và `data:text/html` sau khi loại bỏ các ký tự U+0000–U+0020. |
| `data-kit-class="..."` | Class Expression | Quản lý danh sách class động dựa trên điều kiện, giữ nguyên các class static có sẵn. |
| `data-kit-style="..."` | `prop: expr;` | Quản lý các giá trị CSS liên tục an toàn (ví dụ: `width: progress + '%';`). Bị giới hạn 128 entries. |
| `data-kit-model="..."` | Field Name | Binding 2 chiều cho form control (`<input>`, `<select>`, `<textarea>`). Chỉ nhận tên field top-level khớp `/^[A-Za-z_][A-Za-z0-9_]*$/`. |

### C. Control Flow & Structural Directives

| Directive | Dynamic Expression | Mô tả Contract |
|---|---|---|
| `data-kit-if="..."` | Boolean Expression | Mount/unmount một direct host một-root, hoặc materialize fragment của `<template>`. |
| `data-kit-for="..."` | `item, index of items` | Reconcile các nhóm clone của `<template>`; dạng `item of items` cũng hợp lệ. |
| `data-kit-key="..."` | Expression | Cung cấp identity string hoặc finite number duy nhất cho một row. |

`data-kit-if` không bắt buộc phải nằm trên `<template>`:

```html
<section data-kit-scope="open: true">
  <div data-kit-if="open">Một conditional root</div>
</section>
```

Direct element cần một scope hoặc component boundary bao ngoài, và biểu thức
đọc boundary cha gần nhất đó. `data-kit-scope` hoặc `data-kit-component` trên
chính host chỉ thuộc nhánh sau khi mount và không thể cung cấp điều kiện cho sự
tồn tại của chính nó. Authored host là fallback SSR
và no-JavaScript: truthy ở lần render đầu giữ host đó, falsy unmount nó, còn lần
mở lại tạo host mới. Trong standalone runtime, biểu thức sai phát diagnostic
nhưng giữ fallback nguyên vẹn; Kitwork preflight từ chối source sai trước khi
publish generation.

Dùng `<template data-kit-if>` cho fragment nhiều top-level node hoặc khi nội
dung phải inert trước lúc KitJS boot. Direct host vẫn tuân theo browser loading
bình thường, vì vậy ảnh, iframe, media hoặc resource descendant có thể bắt đầu
tải trước khi điều kiện false được đánh giá. Script executable không hợp lệ
trong mọi structural region. `data-kit-for` và `data-kit-key` vẫn template-only;
retain bị cấm trên hoặc bên trong cả hai dạng conditional branch.

### D. Event và modifier được hỗ trợ

Event directive dùng modifier phân tách bằng dấu hai chấm, ví dụ `data-kit-click:once="save()"`. Event set hiện tại là:

```text
click dblclick submit input change keydown keyup
pointerdown pointerup focusin focusout
```

| Modifier | Hành vi Kỹ thuật |
|---|---|
| `:self` | Chỉ chạy action khi `event.target === $element` (Dùng cho Modal Backdrop). |
| `:prevent` | Tự động gọi `event.preventDefault()`. |
| `:stop` | Tự động gọi `event.stopPropagation()`. |
| `:once` | Chỉ thực thi action 1 lần duy nhất rồi hủy binding. |
| `:outside` | Chạy action khi event hợp lệ xảy ra bên ngoài phần tử; chỉ hỗ trợ `click`, `dblclick`, `pointerdown`, `pointerup` và `focusin`. |
| `:enter` | Chỉ kích hoạt `keydown`/`keyup` khi phím là `Enter`. |
| `:escape` | Chỉ kích hoạt `keydown`/`keyup` khi phím là `Escape`. |
| `:debounce(ms)` | Hoãn action từ 1 đến 60.000 mili-giây kể từ event cuối. |

Danh sách trong bảng trên là toàn bộ modifier public của contract hiện tại.

---

## 4. Sealed Service Catalog

Standalone npm profiles chỉ có `kit.version` và `kit.component`; chúng không chứa service hoặc navigation object. Một staged closed artifact có thể chọn các namespace service dưới đây cho trusted component JavaScript:

| Service | Public namespace members trong trusted JavaScript |
|---|---|
| `announce@1.0.0` | `say`, `polite`, `assertive`, `clear` |
| `appearance@1.0.0` | `mode`, `resolved`, `snapshot`, `subscribe`, `set`, `toggle`, `system` |
| `clipboard@1.0.0` | `writeText`, `readText` |
| `cookie@1.0.0` | `get`, `set`, `remove`, `has` |
| `fullscreen@1.0.0` | `request`, `exit`, `active` |
| `navigation@1.0.0` | `back`, `forward`, `reload` |
| `network@1.0.0` | `online`, `snapshot`, `subscribe` |
| `progress@1.0.0` | `snapshot`, `subscribe`, `start`, `update`, `finish` |
| `request@1.0.0` | `send`, `get`, `post`, `abort` |
| `share@1.0.0` | `open`, `canShare` |
| `storage@1.0.0` | `get`, `set`, `remove`, `has`, `clear` |

Mỗi selected namespace được freeze và có một exact `version` không enumerable. Trusted JavaScript dùng noun-first API, ví dụ `kit.clipboard.writeText(text)`. Authored action không nhận raw `kit`; canonical `app@1.1.0` chỉ chiếu các lệnh được grant qua dạng tĩnh `$app.<service>.<method>(...)`, ví dụ `$app.clipboard.writeText(text)`. Morph và Drive là phần private của Hydrate và không thêm navigation control vào public API.

---

## 5. An toan & Rao chan Sandbox (Security Boundaries)

Các ranh giới chính của authored expression là:

- **Zero-Eval:** Authored source không chạy qua `eval()` hoặc `Function`.
- **Closed expression language:** Parser/evaluator từ chối cú pháp không hỗ trợ, browser globals và các tên có thể thoát qua prototype chain.
- **Execution Budget:**
  - Giới hạn tối đa **10.000 AST Node Visits** per evaluation.
  - Giới hạn tối đa **64 Call Depth** lồng nhau.
- **Action Transaction:** Các phép gán của authored action được stage và chỉ commit khi toàn bộ action đồng bộ thành công; action lỗi sẽ bỏ các staged assignment. Trusted component methods là JavaScript thông thường và nằm ngoài transaction này.

Trusted component JavaScript vẫn có đầy đủ quyền JavaScript của trang và thuộc trusted computing base. `data-kit-ignore` là ownership/Morph boundary, không phải sanitizer hay security boundary.

---

## 6. Mo hinh Giao hang Go JIT Staged Delivery

Trong staged mode của Kitwork Engine, runtime được giao qua JIT Server thay vì nạp standalone profile từ npm hoặc CDN:

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
   <script data-kitwork-jit="runtime" data-kitwork-hash="<hash>" src="/jit/<hash>.runtime.js" integrity="sha256-..." crossorigin="anonymous" defer></script>
   <script data-kitwork-jit="hydrate" data-kitwork-hash="<hash>" src="/jit/<hash>.hydrate.js" integrity="sha256-..." crossorigin="anonymous" defer></script>
   <script data-kitwork-jit="graph" data-kitwork-hash="<hash>" src="/jit/<hash>.graph.js" integrity="sha256-..." crossorigin="anonymous" defer></script>
   <script data-kitwork-jit="service" data-kitwork-hash="<hash>" src="/jit/<hash>.progress.js" integrity="sha256-..." crossorigin="anonymous" defer></script>
   <script data-kitwork-jit="component" data-kitwork-hash="<hash>" src="/jit/<hash>.counter.js" integrity="sha256-..." crossorigin="anonymous" defer></script>
   ```
4. Phát các classic script có `defer` theo thứ tự runtime → Hydrate → graph → services → components; URL content-addressed và SRI cho phép browser xác minh đúng bytes đã chuẩn bị.
