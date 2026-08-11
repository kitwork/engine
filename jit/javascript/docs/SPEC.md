# 🏛️ KitJS Specification — Clean 1.0 Standard

> **Specification Version:** `1.0.0-canonical`  
> **Target Engine:** Kitwork Engine  
> **Canonical Namespace:** `window.kit`  
> **Directive Prefix:** `data-kit-*`  

---

## 1. Triết lý Cốt lõi & Nguyên tắc Bất biến

> **“The page already exists. KitJS makes it alive.”**

1. **HTML First & Progressive Enhancement:**  
   Server render toàn bộ HTML có nội dung và cấu trúc sử dụng được ngay. Nếu JavaScript bị tắt trên trình duyệt, các liên kết (`<a>`) và form cơ bản (`<form>`) vẫn phải chuyển trang và gửi dữ liệu thành công.
2. **Browser-Primitive First:**  
   Ưu tiên tuyệt đối các tính năng gốc của HTML5/DOM API (Modal dùng `<dialog>`, Popup dùng Popover API, Accordion dùng `<details>`). KitJS chỉ đóng vai trò quản lý state và chuyển đổi thuộc tính, không xây dựng lại UI Widget cồng kềnh bằng JS.
3. **Delegated-Only & Memory-Safe under Drive:**  
   Toàn bộ sự kiện trên trang được quản lý qua **đúng một Event Listener duy nhất ở gốc `document`**. Khi trang được di chuyển hoặc DOM bị Morph bởi Kitwork Drive, bộ nhớ tự động giải phóng mà không rò rỉ (Zero Memory Leaks).
4. **Closed Zero-Eval Expression Engine:**  
   Tuyệt đối không sử dụng `eval()` hay `new Function()`. Biểu thức `data-kit-*` được phân tích thành AST đóng (sandboxed expression AST) phía client với ngân sách thực thi nghiêm ngặt, cách ly hoàn toàn với `window`, `document`, hay `globalThis`.
5. **Two Delivery Modes, One Runtime:**  
   - **Standalone Export (Script tĩnh CDN):** `<script src="./kitjs.min.js" defer></script>` (Tự động kích hoạt, không cần metadata/app root).
   - **Kitwork Server JIT:** Go Engine quét HTML và inject 1 script duy nhất có hash quản lý: `<script data-kitwork-runtime data-kitwork-plan="<hash>" src="..." defer></script>`.

---

## 2. Bề mặt JS API tối giản (Public JS Surface)

Mã nguồn JavaScript công khai chỉ tương tác qua 2 thành phần chính trên `window.kit`:

```js
kit.version                           // Read-only version string
kit.component(name, plainObject)     // Đăng ký component (luôn đúng 2 tham số, không getter)
```

Nếu trang nạp thêm các gói service (như storage hay request), các service này sẽ xuất hiện dưới dạng **Plain Objects** trực tiếp trên `kit`:

```js
kit.storage = { get(), set(), remove() }
kit.request = { get(), post() }
```

### ❌ KHÔNG CÓ TRONG HỆ THỐNG:
> Không có `kit.start()`, `kit.destroy()`, `kit.use()`, `kit.service()`, `mount()`, `unmount()`, `kit.component("name")` (no getter), `app`, `ctx`, `provider`, `store`, `watch`, `effect`.

* **Auto-boot:** Runtime tự động kích hoạt ngay sau khi nạp xong. Mọi logic khởi động, theo dõi MutationObserver hay Drive hooks được ẩn hoàn toàn bên trong lõi private của runtime (`core/boot.js`).

---

## 3. Ranh giới Bảo mật HTML (Security Boundary)

### ⛔ HTML Expression KHÔNG BAO GIỜ thấy:
* `kit` (không được gọi `kit.storage.clear()` hay `kit.request` từ HTML).
* `window`, `document`, raw DOM nodes, hay native `Event` objects.

### ✅ HTML Expression CHỈ ĐƯỢC thấy:
1. Bare state & methods nội bộ của Component (`count`, `increment()`).
2. Các ngữ cảnh dành riêng bắt đầu bằng `$`:
   - `$element`: Phần tử DOM hiện tại đang chạy directive.
   - `$host`: Phần tử Component host chứa directive.
   - `$event`: Event snapshot đã được chuẩn hóa.
   - `$component`: Đối tượng Component sở hữu.
   - `$parent`: Component cha (nếu có lồng nhau).
   - `$item`, `$index`: Biến vòng lặp `data-kit-for`.
3. Bí danh Component / DOM Element direct alias: `$<alias>` (ví dụ `$counter`, `$theme`, `$searchInput`).

---

## 4. Master Directives & Ví dụ Thực tế

### NHÓM 1: COMPONENT & ALIAS DIRECTIVES

#### 1. `data-kit-component`
* **Mô tả:** Khai báo vùng quản lý của một Component.
* **Cú pháp:** `data-kit-component="<componentName>"`; ghim phiên bản chính xác bằng thuộc tính riêng `data-kit-version="<exact-semver>"`. Cú pháp `component@version` trong `data-kit-component` bị cấm.
* **Ví dụ:**
```html
<script>
  kit.component("counter", { count: 0 });
</script>

<div data-kit-component="counter">
  <button data-kit-click="count = count + 1">+</button>
  <span data-kit-text="count">0</span>
</div>
```

#### 2. `data-kit-as`
* **Mô tả:** Đặt bí danh (Alias) bắt đầu bằng dấu `$` cho Component instance để truy cập state/methods của nó từ bất kỳ đâu trên trang.
* **Cú pháp:** `data-kit-as="$<aliasName>"`
* **Ví dụ:**
```html
<div data-kit-component="theme" data-kit-as="$theme">
  <button data-kit-click="toggle()">Chuyển Giao diện</button>
</div>

<!-- Truy cập $theme từ bất kỳ vị trí nào -->
<span data-kit-text="`Chế độ hiện tại: ${$theme.mode}`"></span>
```

#### 3. `data-kit-alias`
* **Mô tả:** Đặt bí danh (Alias) bắt đầu bằng dấu `$` trực tiếp cho một DOM Element. Cho phép tham chiếu thẳng tới thẻ DOM đó mà không cần thông qua namespace phụ nào.
* **Cú pháp:** `data-kit-alias="$<aliasName>"`
* **Ví dụ:**
```html
<div data-kit-component="search-box">
  <!-- $searchInput trỏ thẳng tới HTMLInputElement -->
  <input type="text" data-kit-alias="$searchInput" placeholder="Tìm kiếm..." />

  <!-- $element = Chính cái nút này, $searchInput = Thẻ input ở trên -->
  <button data-kit-click="
    $element.disabled = true;
    $searchInput.focus();
  ">Focus ô tìm kiếm</button>
</div>
```

#### 4. `data-kit-scope`
* **Mô tả:** Khởi tạo dữ liệu (state seed) ban đầu cho phần tử hoặc Component host ngay khi mount (chạy đúng 1 lần).
* **Cú pháp:** `data-kit-scope="key: value; key2: value2;"`
* **Ví dụ:**
```html
<div data-kit-scope="open: false; title: 'Xin chào';">
  <button data-kit-click="open = !open">Bật / Tắt</button>
  <p data-kit-show="open" data-kit-text="title"></p>
</div>
```

---

### NHÓM 2: OUTPUT / RENDER DIRECTIVES

#### 5. `data-kit-text`
* **Mô tả:** Cập nhật văn bản an toàn (`textContent`) của phần tử theo giá trị biểu thức.
* **Cú pháp:** `data-kit-text="<expression>"`
* **Ví dụ:**
```html
<div data-kit-scope="user: { name: 'Quốc', role: 'Admin' };">
  <h2 data-kit-text="user.name"></h2>
  <span data-kit-text="`Vai trò: ${user.role}`"></span>
</div>
```

#### 6. `data-kit-show`
* **Mô tả:** Bật/tắt hiển thị phần tử bằng thuộc tính `hidden` của HTML (phần tử vẫn nằm trong DOM).
* **Cú pháp:** `data-kit-show="<booleanExpression>"`
* **Ví dụ:**
```html
<div data-kit-scope="isMenuOpen: false;">
  <button data-kit-click="isMenuOpen = !isMenuOpen">Menu</button>
  <nav data-kit-show="isMenuOpen">
    <a href="/home">Trang chủ</a>
    <a href="/profile">Hồ sơ</a>
  </nav>
</div>
```

#### 7. `data-kit-class`
* **Mô tả:** Thêm/Xóa các class CSS động dựa trên điều kiện (sử dụng dạng Class Map Shorthand).
* **Cú pháp:** `data-kit-class="className: condition; 'complex-class': condition;"`
* **Ví dụ:**
```html
<div data-kit-scope="active: true; saving: false;">
  <button data-kit-class="
    active: active;
    'bg-blue-500 text-white': active;
    'opacity-50 pointer-events-none': saving;
  ">
    Lưu thay đổi
  </button>
</div>
```

#### 8. `data-kit-style`
* **Mô tả:** Gán giá trị cho các thuộc tính CSS Inline.
* **Cú pháp:** `data-kit-style="property: expression;"`
* **Ví dụ:**
```html
<div data-kit-scope="progress: 75; color: 'red';">
  <div data-kit-style="
    width: `${progress}%`;
    background-color: color;
  " style="height: 10px;"></div>
</div>
```

#### 9. `data-kit-bind`
* **Mô tả:** Gán tự động các thuộc tính HTML chuẩn như `aria-*`, `data-*`, `disabled`, `title`, `id`,...
* **Cú pháp:** `data-kit-bind="attributeName: expression;"`
* **Ví dụ:**
```html
<div data-kit-scope="isLoading: true; currentStep: 2;">
  <button data-kit-bind="
    disabled: isLoading;
    aria-busy: isLoading;
    aria-disabled: isLoading;
    data-step: currentStep;
    title: isLoading ? 'Đang tải...' : 'Sẵn sàng';
  ">
    Gửi dữ liệu
  </button>
</div>
```

---

### NHÓM 3: STRUCTURE & FORM DIRECTIVES

#### 10. `data-kit-if`
* **Mô tả:** Thêm hoặc Xóa phần tử thực sự khỏi DOM dựa trên điều kiện (unmount subtree hoàn toàn).
* **Cú pháp:** `data-kit-if="<booleanExpression>"`
* **Ví dụ:**
```html
<div data-kit-scope="isLoggedIn: false;">
  <form data-kit-if="!isLoggedIn">
    <input type="text" placeholder="Tên đăng nhập" />
    <button data-kit-click="isLoggedIn = true">Đăng nhập</button>
  </form>

  <div data-kit-if="isLoggedIn">
    <p>Chào mừng bạn đã quay trở lại!</p>
  </div>
</div>
```

#### 11. `data-kit-for` & `data-kit-key`
* **Mô tả:** Lặp một mảng dữ liệu để render danh sách phần tử, bắt buộc đi kèm `data-kit-key` để định danh từng hàng.
* **Cú pháp:** `data-kit-for="$item, $index of <array>"` và `data-kit-key="<uniqueId>"`
* **Ví dụ:**
```html
<div data-kit-scope="
  todos: [
    { id: 101, text: 'Học KitJS' },
    { id: 102, text: 'Viết bài đặc tả' }
  ];
">
  <ul>
    <li data-kit-for="$item, $index of todos" data-kit-key="$item.id">
      <span data-kit-text="`${$index + 1}. ${$item.text}`"></span>
    </li>
  </ul>
</div>
```

#### 12. `data-kit-model`
* **Mô tả:** Binding dữ liệu 2 chiều (Two-way data binding) cho thẻ Form.
* **Cú pháp:** `data-kit-model="<writablePath>"`
* **Ví dụ:**
```html
<div data-kit-scope="username: 'Quốc'; rememberMe: true;">
  <input type="text" data-kit-model="username" />
  
  <label>
    <input type="checkbox" data-kit-model="rememberMe" /> Ghi nhớ tôi
  </label>

  <p data-kit-text="`Tài khoản: ${username} (Ghi nhớ: ${rememberMe})`"></p>
</div>
```

---

### NHÓM 4: EVENT DIRECTIVES & MODIFIERS

#### 13. `data-kit-<event>`
* **Mô tả:** Lắng nghe các sự kiện trình duyệt kết hợp với các Modifiers điều khiển luồng.
* **Modifiers:** `:prevent`, `:stop`, `:once`, `:outside`, `:enter`, `:escape`, `:debounce(ms)`, `:throttle(ms)`.
* **Ví dụ:**
```html
<!-- Submit form ngắt reload bằng :prevent -->
<form data-kit-submit:prevent="saveData()">
  <button type="submit">Lưu</button>
</form>

<!-- Input hoãn 300ms khi gõ bằng :debounce -->
<input 
  type="text" 
  data-kit-model="query" 
  data-kit-input:debounce(300)="fetchSuggestions()" 
  placeholder="Tìm kiếm..." />

<!-- Lắng nghe click ra ngoài (:outside) hoặc phím Escape (:escape) -->
<div 
  data-kit-scope="open: true;"
  data-kit-show="open" 
  data-kit-click:outside="open = false"
  data-kit-keydown:escape:document="open = false">
  <p>Hộp thoại Modal</p>
</div>
```

---

## 5. Chuẩn Cú pháp Map Đồng nhất trong HTML

Tất cả các directive dạng Map đều dùng chung một kiểu cú pháp `key: expression;`:

```html
<!-- Scope seeding -->
<div data-kit-scope="open: false; count: 0;"></div>

<!-- Attribute binding -->
<button data-kit-bind="aria-expanded: open; disabled: loading;"></button>

<!-- Inline Style binding -->
<div data-kit-style="width: width; opacity: opacity;"></div>

<!-- Dynamic Class Map -->
<div data-kit-class="active: open; 'md:grid-cols-6': desktop;"></div>
```

---

## 6. Form Model Coercion Matrix (`data-kit-model`)

| Thẻ Form HTML | Giá trị State ép kiểu | Sự kiện lắng nghe |
| :--- | :--- | :--- |
| `<input type="text">`, `search`, `password`, `<textarea>` | `String` | `input` (Tự hoãn sync khi gõ tiếng Việt IME composition) |
| `<input type="number">`, `range` | `Number` (Trống/Lỗi $\rightarrow$ `null`) | `input` |
| `<input type="checkbox">` (Đơn) | `Boolean` (`true`/`false`) | `change` |
| `<input type="checkbox">` (Nhóm cùng model) | `Array` chứa các giá trị checked | `change` |
| `<input type="radio">` (Nhóm) | Giá trị của radio được chọn | `change` |
| `<select>` (Đơn) | `String` | `change` |
| `<select multiple>` | `Array` các giá trị chọn | `change` |
| `<input type="file">` | Read-only `FileList` (Không ghi ngược từ JS) | `change` |

---

## 7. Scope Resolution Order & Lexical Boundaries

Khi một tên biến (bare identifier) được gọi trong HTML expression, Runtime tra cứu theo 5 cấp:

```text
CẤP 1: Runtime Contexts & Local Bindings ($element, $host, $event, $item, $index, $<alias>)
  │
  ▼
CẤP 2: Nearest Local Scope (Dữ liệu khai báo từ data-kit-scope gần nhất)
  │
  ▼
CẤP 3: Component State, Getters & Methods (Thuộc tính & hàm của Component sở hữu)
  │
  ▼
CẤP 4: App Root Scope (State mức toàn cục ứng dụng)
  │
  ▼
CẤP 5: undefined (Trả về undefined, không bao giờ tự nhảy sang JavaScript Globals)
```

---

## 8. Vòng đời Component & Clean `init()` Pattern

* Component **không có bộ đôi public `mount()` / `unmount()`**.
* Nếu Component cần khởi tạo công việc sau khi host DOM tồn tại (như đọc `kit.storage` hay lắng nghe OS theme), sử dụng hàm **`init()` tùy chọn**:

```js
kit.component("theme", {
  mode: "system",

  async init() {
    this.mode = await kit.storage.get("theme", "system");

    var media = matchMedia("(prefers-color-scheme: dark)");
    var self = this;
    var onChange = function () { self.$invalidate(); };
    media.addEventListener("change", onChange);

    // Trả về hàm cleanup để runtime tự dọn dẹp khi unmount / Morph DOM
    return function cleanup() {
      media.removeEventListener("change", onChange);
    };
  }
});
```

---

## 9. Services Guidelines (`kit.<service>`)

```js
kit.storage = {
  prefix: "app:",

  async get(key, fallback) {
    var raw = localStorage.getItem(this.prefix + key);
    if (raw === null) return fallback;
    try { return JSON.parse(raw); } catch (_) { return raw; }
  },

  async set(key, value) {
    localStorage.setItem(this.prefix + key, JSON.stringify(value));
    return value;
  },

  async remove(key) {
    localStorage.removeItem(this.prefix + key);
  }
};
```

1. **Plain Objects:** Service là plain object chứa các async methods hoặc sync utilities.
2. **Luôn trả về Promise cho I/O:** Mọi thao tác I/O, Web API, Fetch, Storage, Clipboard phải trả về `Promise`.
3. **Không động vào DOM:** Service **tuyệt đối không tạo thẻ HTML hay chèn CSS style**.
4. **Không gọi từ HTML:** Service chỉ được gọi từ bên trong Component JS (`kit.storage.get(...)`).

---

## 10. Luồng Event Pipeline & Microtask Scheduler

Khi người dùng thực hiện một tương tác, trình duyệt xử lý theo 9 bước tuần tự:

```text
1. Xác định target sự kiện ($element & $event)
2. Lọc sự kiện qua Modifiers (:enter, :escape, :window, :document)
3. Thực thi đồng bộ preventDefault() (:prevent) & stopPropagation() (:stop)
4. Kiểm tra và đánh dấu ngắt nếu có modifier :once
5. Áp dụng Modifier thời gian (:debounce(ms) hoặc :throttle(ms))
6. Thực thi Action Program (Gọi hàm Component hoặc biểu thức gán)
7. Lắng nghe & theo dõi các Promise phát sinh từ Action
8. Đưa lỗi (nếu có) vào Error Pipeline
9. Đẩy thông báo cập nhật DOM vào Microtask Scheduler (Batch Invalidation)
```

---

## 11. Bộ Mã Lỗi Diagnostic Chuẩn (Diagnostic Error Codes)

| Mã Lỗi | Nguyên nhân phát sinh |
| :--- | :--- |
| `KIT_UNKNOWN_DIRECTIVE` | Sử dụng chỉ thị không tồn tại trong Core (ví dụ `data-kit-foo`). |
| `KIT_INVALID_MODIFIER` | Dùng modifier sai ngữ cảnh (ví dụ `:enter` trên sự kiện click hoặc `:outside` với `:window`). |
| `KIT_UNSAFE_MEMBER` | Cố tình truy cập biến bị cấm (như `__proto__`, `constructor`, `window`, `eval`). |
| `KIT_DUPLICATE_ALIAS` | Khai báo 2 component hoặc DOM Element cùng trùng 1 tên alias `$name` trong trang. |
| `KIT_DUPLICATE_KEY` | Trùng lặp `data-kit-key` trong danh sách lặp `data-kit-for`. |
| `KIT_MODEL_NOT_WRITABLE` | `data-kit-model` trỏ vào một đường dẫn không thể ghi. |
| `KIT_ASYNC_BINDING` | Biểu thức `data-kit-text` hoặc `data-kit-bind` trả về một Promise. |

---

## 12. Bảng Hướng dẫn Chuyển đổi từ Code cũ (Migration Guide)

| Cú pháp Cũ (Legacy) | Cú pháp Mới (1.0 Clean Spec) | Lý do thay đổi |
| :--- | :--- | :--- |
| `$el` | **`$element`** | Giữ từ khóa ngữ cảnh chuẩn hóa rõ nghĩa. |
| `$root` | **`$host`** | Chỉ rõ host element của component. |
| `data-kit-alias="name"` | **`data-kit-alias="$name"`** | Đặt bí danh trực tiếp cho DOM Element. |
| `data-kit-ref="name"` | **`data-kit-alias="$name"`** | Thay thế `ref` bằng `alias` tham chiếu trực tiếp. |
| `data-kit-component="dialog=$modal"` | **`data-kit-component="dialog" data-kit-as="$modal"`** | Tách riêng directive component và alias. |
| `data-kit-data`, `data-kit-aria` | **`data-kit-bind="aria-*: ...;"`** | Gom về 1 directive bind thống nhất. |
| `data-kit-away="close()"` | **`data-kit-click:outside="close()"`** | Chuyển thành event modifier `:outside`. |
| `data-kit-escape="close()"` | **`data-kit-keydown:escape:document="close()"`** | Chuyển thành keyboard modifier `:escape`. |
| `data-kit-hidden="expr"` | **`data-kit-show="!expr"`** | Đổi tên trực quan theo trạng thái hiển thị. |
| `data-kit-action="copy"` | **`data-kit-click="..."` hoặc Component** | Rõ ràng luồng event và component. |
