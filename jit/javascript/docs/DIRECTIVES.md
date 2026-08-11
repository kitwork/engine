# 📜 KitJS Directives Reference — Complete Specification & Usage Guide

> **Package Location:** `engine/jit/javascript/docs/DIRECTIVES.md`  
> **Normative Baseline:** KitJS Clean 1.0 Standard  
> **Related Files:** [README.md](../README.md), [SPEC.md](SPEC.md), [SERVICES.md](SERVICES.md)  

---

## 1. 📊 Bảng Tổng hợp Toàn bộ 13 Core Directives

Dưới đây là bảng tra cứu nhanh toàn bộ 13 chỉ thị cốt lõi của KitJS:

| Directive | Parser Mode | Cú pháp Ví dụ | Giải thích Tóm tắt |
| :--- | :--- | :--- | :--- |
| **`data-kit-component`** | Identity | `data-kit-component="counter"` | Khai báo vùng host quản lý của Component. |
| **`data-kit-as`** | Identity | `data-kit-as="$theme"` | Đặt bí danh toàn cục (Alias) trỏ tới **Component Instance**. |
| **`data-kit-alias`** | Identity | `data-kit-alias="$searchInput"` | Đặt bí danh toàn cục (Alias) trỏ tới **HTML DOM Element**. |
| **`data-kit-scope`** | Named Map | `data-kit-scope="open: false;"` | Khởi tạo dữ liệu state seed ban đầu (chỉ chạy đúng 1 lần khi mount). |
| **`data-kit-text`** | Binding Expression | `data-kit-text="user.name"` | Cập nhật văn bản an toàn (`textContent`) chống XSS. |
| **`data-kit-show`** | Binding Expression | `data-kit-show="isOpen"` | Bật/Tắt hiển thị bằng thuộc tính `hidden` của HTML. |
| **`data-kit-class`** | Class Value | `data-kit-class="active: open;"` | Thêm/Xóa class CSS động theo điều kiện (Class Map Shorthand). |
| **`data-kit-style`** | Named Map | `data-kit-style="width: w;"` | Gán giá trị cho thuộc tính CSS Inline & CSS Custom Variables. |
| **`data-kit-bind`** | Named Map | `data-kit-bind="disabled: loading;"` | Gán tự động thuộc tính HTML chuẩn (`aria-*`, `data-*`, `disabled`,...). |
| **`data-kit-if`** | Binding Expression | `data-kit-if="isLoggedIn"` | Thêm hoặc Xóa phần tử thực sự khỏi DOM (Mount/Unmount subtree). |
| **`data-kit-for`** | Iterator | `data-kit-for="$item of items"` | Lặp danh sách phần tử (bắt buộc đi kèm `data-kit-key`). |
| **`data-kit-model`** | Writable Path | `data-kit-model="form.username"` | Binding dữ liệu 2 chiều (Two-way binding) cho các thẻ Form. |
| **`data-kit-<event>`** | Action Program | `data-kit-submit:prevent="save()"` | Lắng nghe sự kiện DOM kết hợp với các Event Modifiers. |

---

## 2. Tổng quan Hệ thống Directives (`data-kit-*`)

KitJS sử dụng các thuộc tính HTML tùy chỉnh mang tiền tố `data-kit-*` để bổ sung tính năng tương tác trực quan cho các trang SSR HTML.

### 🔑 Các quy tắc cốt lõi:
1. **Tiền tố tác giả:** Tác giả trang web luôn viết tiền tố `data-kit-*`. Tiền tố `data-kitwork-*` được dành riêng cho Go Engine ghi metadata nội bộ.
2. **Zero-Eval Sandbox:** Biểu thức bên trong các directive được phân tích thành AST đóng (Private Cached AST), tuyệt đối **không sử dụng `eval()` hay `new Function()`**.
3. **Ranh giới Ngữ cảnh (Security Contexts):** Biểu thức HTML chỉ có thể truy cập state/methods của Component và các ngữ cảnh `$`:
   - `$element`: Phần tử DOM hiện tại đang chạy directive.
   - `$host`: Phần tử Component host chứa directive.
   - `$event`: Event snapshot đã được chuẩn hóa.
   - `$component`: Đối tượng Component sở hữu.
   - `$parent`: Component cha (nếu lồng nhau).
   - `$item`, `$index`: Biến vòng lặp `data-kit-for`.
   - `$<alias>`: Bí danh Component (`$theme`) hoặc DOM Element (`$searchInput`) toàn cục.
   - ⛔ **BỊ CẤM TRONG HTML:** `kit`, `window`, `document`, DOM nodes thô, native `Event` objects, `eval`, `Function`.

---

## 3. Cú pháp Map Đồng nhất (Unified Map Syntax)

Các directive dạng Map (`scope`, `bind`, `style`, `class`) sử dụng chung kiểu phân tách `key: expression;`:

```text
key-1: expression-1; key-2: expression-2;
```

* **Dấu chấm phẩy `;`**: Phân cách các cặp key-value. Dấu `;` ở cuối là tùy chọn nhưng được khuyến nghị.
* **Dấu hai chấm `:`**: Phân cách giữa Key và Expression.
* **Quy tắc Parser:** Parser chỉ tách `;` và `:` ở cấp top-level; các dấu này bên trong chuỗi quote `'...'`, `"..."`, mảng `[...]`, object `{...}` hay template `` `...` `` được giữ nguyên.

---

## 4. Danh mục Chi tiết 13 Core Directives

---

### 4.1 `data-kit-component`
* **Parser Mode:** `Identity`
* **Mô tả:** Đánh dấu phần tử gốc (Host Element) sở hữu một Component instance. Khi được mount, runtime sẽ clone state từ blueprint đăng ký trong JS và gắn vào phần tử này.
* **Cú pháp:** `data-kit-component="<componentName>"`


#### Ví dụ Thực tế:
```html
<script>
  kit.component("counter", {
    count: 0,
    increment() { this.count++; }
  });
</script>

<div data-kit-component="counter">
  <button data-kit-click="increment()">+</button>
  <span data-kit-text="count">0</span>
</div>
```

---

### 4.2 `data-kit-as`
* **Parser Mode:** `Identity`
* **Mô tả:** Đặt bí danh toàn cục (Alias) bắt đầu bằng dấu `$` cho Component instance. Giúp các phần tử ở bất kỳ vị trí nào trên trang đều có thể đọc state hoặc gọi method của component này.
* **Cú pháp:** `data-kit-as="$<aliasName>"`
* **Ràng buộc:**
  - Chỉ hợp lệ khi đi cùng `data-kit-component`.
  - Tên alias phải match định dạng `/^\$[A-Za-z][A-Za-z0-9_]*$/`.
  - Tên alias phải là duy nhất trong toàn bộ ứng dụng.

#### Ví dụ Thực tế:
```html
<!-- Component Sidebar đặt alias là $sidebar -->
<aside data-kit-component="sidebar" data-kit-as="$sidebar">
  <nav data-kit-show="isOpen">
    <a href="/dashboard">Dashboard</a>
  </nav>
</aside>

<!-- Nút bấm đặt ở Header trang web vẫn gọi mở/đóng $sidebar được -->
<header>
  <button data-kit-click="$sidebar.toggle()">Toggle Sidebar</button>
</header>
```

---

### 4.3 `data-kit-alias`
* **Parser Mode:** `Identity`
* **Mô tả:** Đặt bí danh toàn cục (Alias) bắt đầu bằng dấu `$` trực tiếp cho một HTML DOM Element. Giúp tham chiếu thẳng tới phần tử DOM này mà không cần qua namespace phụ nào.
* **Cú pháp:** `data-kit-alias="$<aliasName>"`
* **Ràng buộc:**
  - Tên alias phải match định dạng `/^\$[A-Za-z][A-Za-z0-9_]*$/`.
  - Biến `$aliasName` trỏ trực tiếp tới đối tượng `HTMLElement`.

#### Ví dụ Thực tế:
```html
<div data-kit-component="search-box">
  <!-- $searchInput trỏ thẳng tới HTMLInputElement -->
  <input type="text" data-kit-alias="$searchInput" placeholder="Nhập từ khóa..." />
  
  <!-- $element = Nút bấm hiện tại, $searchInput = Thẻ input ở trên -->
  <button data-kit-click="
    $element.disabled = true;
    $searchInput.focus();
  ">Focus & Disable nút</button>
</div>
```

---

### 4.4 `data-kit-scope`
* **Parser Mode:** `Named Map`
* **Mô tả:** Khởi tạo dữ liệu (state seed) ban đầu cho phần tử hoặc Component host ngay khi mount. Chỉ thực thi đúng 1 lần duy nhất lúc khởi tạo.
* **Cú pháp:** `data-kit-scope="key1: expr1; key2: expr2;"`
* **Quy tắc:**
  - Trên phần tử thường: Tạo local lexical scope.
  - Trên Component host: Override các giá trị state ban đầu của Component instance.

#### Ví dụ Thực tế:
```html
<!-- Local Scope trên phần tử HTML thường -->
<div data-kit-scope="open: false; title: 'Thông báo';">
  <button data-kit-click="open = !open">Mở / Đóng</button>
  <div data-kit-show="open">
    <h3 data-kit-text="title"></h3>
  </div>
</div>

<!-- Seed State ban đầu cho Component Instance -->
<div 
  data-kit-component="dialog" 
  data-kit-as="$confirmModal"
  data-kit-scope="open: true; title: 'Xác nhận xóa tài khoản';">
</div>
```

---

### 4.5 `data-kit-text`
* **Parser Mode:** `Binding Expression`
* **Mô tả:** Cập nhật an toàn thuộc tính `textContent` của phần tử theo giá trị của biểu thức. Tự động chống tấn công XSS.
* **Cú pháp:** `data-kit-text="<expression>"`
* **Quy tắc:**
  - `null` hoặc `undefined` được render thành chuỗi rỗng `""`.
  - Mọi giá trị primitive (String, Number, Boolean) được chuyển thành `String(value)`.
  - Không render thẻ HTML thô.

#### Ví dụ Thực tế:
```html
<div data-kit-scope="user: { name: 'Nguyễn Văn Quốc', unread: 5 };">
  <h1 data-kit-text="user.name">Tên mặc định SSR</h1>
  <span data-kit-text="`Bạn có ${user.unread} tin nhắn mới`"></span>
</div>
```

---

### 4.6 `data-kit-show`
* **Parser Mode:** `Binding Expression`
* **Mô tả:** Bật/Tắt hiển thị phần tử bằng thuộc tính `hidden` của HTML.
* **Cú pháp:** `data-kit-show="<booleanExpression>"`
* **Quy tắc:**
  - Biểu thức trả về `false` $\rightarrow$ Thêm thuộc tính `hidden` (`display: none`).
  - Biểu thức trả về `true` $\rightarrow$ Xóa thuộc tính `hidden`.
  - Phần tử vẫn nằm nguyên trong cây DOM; state, task của nó được giữ nguyên.

#### Ví dụ Thực tế:
```html
<div data-kit-scope="tab: 'overview';">
  <button data-kit-click="tab = 'overview'">Tổng quan</button>
  <button data-kit-click="tab = 'settings'">Cài đặt</button>

  <section data-kit-show="tab === 'overview'">Nội dung Tổng quan</section>
  <section data-kit-show="tab === 'settings'">Nội dung Cài đặt</section>
</div>
```

---

### 4.7 `data-kit-class`
* **Parser Mode:** `Class Value (Class Map Shorthand)`
* **Mô tả:** Thêm/Xóa các class CSS động theo điều kiện.
* **Cú pháp:** `data-kit-class="className: condition; 'complex-class': condition;"`
* **Quy tắc:**
  - Class đơn giản không cần bọc nháy (ví dụ `active: isOpen;`).
  - Class phức tạp (có chứa dấu `:`, khoảng trắng hoặc ký tự đặc biệt) phải bọc trong dấu nháy đơn hoặc kép (ví dụ `'bg-red-500 text-white': hasError;`).
  - Các class tĩnh sẵn có trong `class="..."` của SSR HTML được giữ nguyên không bị xóa.

#### Ví dụ Thực tế:
```html
<div data-kit-scope="isActive: true; isSaving: false;">
  <button 
    class="px-4 py-2 rounded transition-all"
    data-kit-class="
      'bg-blue-600 text-white': isActive;
      'bg-gray-200 text-gray-700': !isActive;
      'opacity-50 pointer-events-none': isSaving;
    ">
    Lưu dữ liệu
  </button>
</div>
```

---

### 4.8 `data-kit-style`
* **Parser Mode:** `Named Map`
* **Mô tả:** Gán giá trị động cho các thuộc tính CSS Inline (kể cả CSS Custom Properties / Biến CSS).
* **Cú pháp:** `data-kit-style="property: expression; --custom-prop: expression;"`

#### Ví dụ Thực tế:
```html
<div data-kit-scope="percent: 65; themeColor: '#2563eb';">
  <div 
    data-kit-style="
      width: `${percent}%`;
      --main-color: themeColor;
      opacity: percent > 0 ? 1 : 0.5;
    "
    style="height: 8px; background-color: var(--main-color);">
  </div>
</div>
```

---

### 4.9 `data-kit-bind`
* **Parser Mode:** `Named Map`
* **Mô tả:** Gán tự động các thuộc tính HTML chuẩn (`aria-*`, `data-*`, `disabled`, `title`, `id`, `tabindex`, `src`, `href`,...).
* **Cú pháp:** `data-kit-bind="attributeName: expression;"`

#### Bảng Chuẩn hóa Attribute Serialization:
| Loại thuộc tính | `null` / `undefined` | `false` | `true` | Chuỗi / Số |
| :--- | :--- | :--- | :--- | :--- |
| **`data-*`** | Xóa attribute | `"false"` | `"true"` | `String(value)` |
| **`aria-*`** | Xóa attribute | `"false"` | `"true"` | `String(value)` |
| **HTML Boolean** (`disabled`, `checked`, `readonly`) | Xóa attribute | Xóa attribute | Thẻ rỗng (Attribute present) | `String(value)` |
| **Thuộc tính thường** (`title`, `id`, `placeholder`) | Xóa attribute | Xóa attribute | `"true"` | `String(value)` |

#### Ví dụ Thực tế:
```html
<div data-kit-scope="loading: true; currentStep: 3; avatarUrl: '/img/user.jpg';">
  <img data-kit-bind="src: avatarUrl; alt: 'Avatar người dùng';" />

  <button data-kit-bind="
    disabled: loading;
    aria-busy: loading;
    aria-disabled: loading;
    data-step: currentStep;
    title: loading ? 'Đang xử lý...' : 'Nhấn để tiếp tục';
  ">
    Tiếp tục
  </button>
</div>
```

---

### 4.10 `data-kit-if`
* **Parser Mode:** `Binding Expression`
* **Mô tả:** Thêm hoặc Xóa phần tử thật sự khỏi cây DOM (Unmount/Mount subtree).
* **Cú pháp:** `data-kit-if="<booleanExpression>"`

#### Ví dụ Thực tế:
```html
<div data-kit-scope="user: null; loading: false;">
  <div data-kit-if="!user">
    <button data-kit-click="user = { name: 'Quốc' }">Đăng nhập</button>
  </div>

  <div data-kit-if="user">
    <p data-kit-text="`Xin chào, ${user.name}`"></p>
    <button data-kit-click="user = null">Đăng xuất</button>
  </div>
</div>
```

---

### 4.11 `data-kit-for` & `data-kit-key`
* **Parser Mode:** `Iterator` (`data-kit-for`) & `Binding Expression` (`data-kit-key`)
* **Mô tả:** Lặp một mảng dữ liệu để render danh sách phần tử, bắt buộc đi kèm `data-kit-key` trên cùng phần tử để đảm bảo định danh từng hàng.
* **Cú pháp:** `data-kit-for="$item, $index of <array>"` và `data-kit-key="<uniqueId>"`

#### Ví dụ Thực tế:
```html
<div data-kit-scope="
  products: [
    { id: 'p1', name: 'Bàn phím Cơ', price: 1200 },
    { id: 'p2', name: 'Chuột Không dây', price: 450 }
  ];
">
  <table>
    <thead>
      <tr><th>STT</th><th>Tên sản phẩm</th><th>Giá</th></tr>
    </thead>
    <tbody>
      <tr data-kit-for="$product, $index of products" data-kit-key="$product.id">
        <td data-kit-text="$index + 1"></td>
        <td data-kit-text="$product.name"></td>
        <td data-kit-text="`${$product.price}.000đ`"></td>
      </tr>
    </tbody>
  </table>
</div>
```

---

### 4.12 `data-kit-model`
* **Parser Mode:** `Writable Path`
* **Mô tả:** Binding dữ liệu 2 chiều (Two-way data binding) cho các phần tử Form Controls. Tự động đồng bộ giá trị giữa thẻ HTML và biến State.
* **Cú pháp:** `data-kit-model="<writablePath>"`

#### Bảng Ép kiểu Trạng thái (Coercion Matrix):
| Thẻ HTML Form | State Type | Sự kiện Lắng nghe |
| :--- | :--- | :--- |
| `<input type="text">`, `search`, `password`, `<textarea>` | `String` | `input` (Tự hoãn sync khi gõ tiếng Việt IME) |
| `<input type="number">`, `range` | `Number` (Trống $\rightarrow$ `null`) | `input` |
| `<input type="checkbox">` (Đơn) | `Boolean` (`true`/`false`) | `change` |
| `<input type="checkbox">` (Nhóm cùng model) | `Array` các giá trị được chọn | `change` |
| `<input type="radio">` (Nhóm) | Giá trị String của radio được chọn | `change` |
| `<select>` (Đơn) | `String` | `change` |
| `<select multiple>` | `Array` | `change` |
| `<input type="file">` | Read-only `FileList` | `change` |

#### Ví dụ Thực tế:
```html
<div data-kit-scope="
  form: {
    username: 'quocnguyen',
    role: 'admin',
    agree: true,
    hobbies: ['coding']
  };
">
  <input type="text" data-kit-model="form.username" />

  <select data-kit-model="form.role">
    <option value="user">User</option>
    <option value="admin">Admin</option>
  </select>

  <label>
    <input type="checkbox" data-kit-model="form.agree" /> Đồng ý điều khoản
  </label>

  <label><input type="checkbox" value="coding" data-kit-model="form.hobbies" /> Lập trình</label>
  <label><input type="checkbox" value="music" data-kit-model="form.hobbies" /> Âm nhạc</label>

  <pre data-kit-text="JSON.stringify(form)"></pre>
</div>
```

---

### 4.13 `data-kit-<event>` & Modifiers
* **Parser Mode:** `Action Program`
* **Mô tả:** Đăng ký hàm xử lý cho các sự kiện DOM (`click`, `submit`, `input`, `keydown`, `keyup`, `focus`, `blur`, `change`,...). Cho phép gán nhiều câu lệnh phân cách bằng dấu `;`.
* **Cú pháp:** `data-kit-<event>:<modifier1>:<modifier2>="<actionProgram>"`

#### Danh mục 10 Modifiers Chuẩn:
| Modifier | Chức năng |
| :--- | :--- |
| `:prevent` | Tự động gọi `event.preventDefault()` đồng bộ. |
| `:stop` | Tự động gọi `event.stopPropagation()` đồng bộ. |
| `:self` | Chỉ kích hoạt khi mục tiêu click/sự kiện trúng CHÍNH XÁC phần tử hiện tại (`event.target === $element`), không chạy khi click vào phần tử con. |
| `:once` | Chỉ kích hoạt handler đúng 1 lần rồi tự tháo dỡ. |
| `:outside` | Lắng nghe khi người dùng click bên ngoài `$element`. |
| `:enter` | Chỉ kích hoạt khi phím bấm là phím `Enter` (Chỉ dùng cho keyboard events). |
| `:escape` | Chỉ kích hoạt khi phím bấm là phím `Escape` (Chỉ dùng cho keyboard events). |
| `:window` | Gán listener lên đối tượng `window`. |
| `:document` | Gán listener lên đối tượng `document`. |
| `:debounce(ms)` | Hoãn kích hoạt cho đến khi ngưng thao tác đủ `ms` mili-giây. |
| `:throttle(ms)` | Giới hạn tần suất kích hoạt tối đa 1 lần mỗi `ms` mili-giây. |


#### Ví dụ Thực tế Tổng hợp Modifiers:
```html
<div data-kit-scope="query: ''; isModalOpen: true;">
  
  <!-- 1. Submit Form ngắt reload bằng :prevent -->
  <form data-kit-submit:prevent="submitSearch()">
    
    <!-- 2. Debounce 300ms ô tìm kiếm để tránh gọi API liên tục -->
    <input 
      type="text" 
      data-kit-model="query" 
      data-kit-input:debounce(300)="fetchSuggestions()" 
      placeholder="Tìm kiếm..." />
      
    <!-- 3. Kích hoạt khi bấm phím Enter bằng :enter -->
    <input 
      type="text" 
      data-kit-keydown:enter="quickSubmit()" />
  </form>

  <!-- 4. Đóng Modal khi click ra bên ngoài (:outside) hoặc bấm phím ESC trên document (:escape:document) -->
  <div 
    data-kit-show="isModalOpen"
    data-kit-click:outside="isModalOpen = false"
    data-kit-keydown:escape:document="isModalOpen = false"
    class="modal-box">
    <p>Nội dung Modal</p>
  </div>

</div>
```

---

## 5. Bảng Xử lý Xung đột Sở hữu DOM (DOM Ownership Rules)

Mỗi directive chỉ sở hữu chính xác phần thuộc tính DOM mà nó quản lý:

| Directive | Thuộc tính DOM sở hữu |
| :--- | :--- |
| `data-kit-text` | Owning `textContent` |
| `data-kit-show` | Owning `hidden` property |
| `data-kit-class` | Owning dynamic class set |
| `data-kit-style` | Owning declared inline CSS properties |
| `data-kit-bind` | Owning declared attributes |
| `data-kit-model` | Owning live form value properties |

⚠️ **Lỗi Xung đột Ownership (`KIT_OWNERSHIP_CONFLICT`):**  
Nếu hai directive cùng cố tình quản lý 1 thuộc tính DOM trên cùng một phần tử (ví dụ: vừa dùng `data-kit-bind="hidden: expr;"` vừa dùng `data-kit-show="expr"`), Runtime sẽ phát lỗi diagnostic và ngắt thi hành để tránh xung đột giao diện.
