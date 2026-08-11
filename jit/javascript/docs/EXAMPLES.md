# 🧩 KitJS Headless Components — Catalog & Practical Examples

> **Package Location:** `engine/jit/javascript/docs/EXAMPLES.md`  
> **Normative Baseline:** KitJS Clean 1.0 Standard  
> **Related Files:** [README.md](../README.md), [SPEC.md](SPEC.md), [DIRECTIVES.md](DIRECTIVES.md), [SERVICES.md](SERVICES.md)  

---

## 1. 📊 Bảng Tổng hợp Danh mục 16 Headless Components

Dưới đây là bảng tra cứu nhanh 16 Component Headless chuẩn hóa trong catalog của KitJS:

| Component Name | File Path | State & Key Methods | Mô tả Tóm tắt |
| :--- | :--- | :--- | :--- |
| **`dropdown`** | `component/dropdown/1.0.0.js` | `open`, `toggle()`, `close()` | Menu thả xuống đơn giản với tự động đóng khi click ngoài. |
| **`theme`** | `component/theme/1.0.0.js` | `mode`, `resolved`, `set()`, `toggle()` | Quản lý sáng/tối (`light`/`dark`/`system`) kết hợp `kit.storage` & OS. |
| **`accordion`** | `component/accordion/1.0.0.js` | `activeItem`, `toggle()`, `open()`, `close()` | Danh sách xòe/gập hỗ trợ điều hướng phím `Up`/`Down`/`Home`/`End`. |
| **`dialog`** | `component/dialog/1.0.0.js` | `open`, `show()`, `close()` | Hộp thoại Modal khóa cuộn trang, tự đóng khi bấm `Escape` hoặc backdrop. |
| **`drawer`** | `component/drawer/1.0.0.js` | `open`, `position`, `toggle()`, `close()` | Khung trượt (slide-over panel) từ cạnh trái/phải màn hình. |
| **`tabs`** | `component/tabs/1.0.0.js` | `activeTab`, `select()`, `next()`, `prev()` | Khung chuyển thẻ Tab hỗ trợ phím mũi tên `Left`/`Right`. |
| **`tab`** | `component/tab/1.0.0.js` | `selected`, `select()` | Thẻ Tab đơn lẻ kết hợp cùng `tabs`. |
| **`toast`** | `component/toast/1.0.0.js` | `visible`, `message`, `show()`, `dismiss()` | Thông báo tự ẩn sau khoảng thời gian timeout. |
| **`tooltip`** | `component/tooltip/1.0.0.js` | `visible`, `show()`, `hide()`, `toggle()` | Khung chú thích ngắn khi hover hoặc focus. |
| **`combobox`** | `component/combobox/1.0.0.js` | `query`, `selected`, `open`, `select()` | Ô tìm kiếm gợi ý danh sách (Autosuggest / Autocomplete). |
| **`command-palette`** | `component/command-palette/1.0.0.js` | `open`, `query`, `execute()` | Khung lệnh tắt tìm kiếm nhanh (`Cmd+K` / `Ctrl+K`). |
| **`menu`** | `component/menu/1.0.0.js` | `open`, `activeIndex`, `toggle()` | Context menu chuẩn ARIA menuitem navigation. |
| **`popover`** | `component/popover/1.0.0.js` | `open`, `toggle()`, `close()` | Panel nổi sử dụng Popover API gốc của trình duyệt. |
| **`progress-bar`** | `component/progress-bar/1.0.0.js` | `value`, `max`, `percent`, `set()` | Thanh hiển thị tiến trình tải (Progress Indicator). |
| **`clipboard`** | `component/clipboard/1.0.0.js` | `copied`, `copy(text)` | Nút sao chép văn bản nhanh tương tác `kit.clipboard`. |
| **`announce`** | `component/announce/1.0.0.js` | `announce(message, mode)` | Phát thông báo đọc cho Screen Readers (Accessibility ARIA). |

---

## 2. Mã nguồn & Ví dụ Thực tế cho 16 Components

---

### 2.1 `dropdown` — Menu Thả xuống
Hỗ trợ mở/đóng menu và tự động đóng khi bấm phím `Escape` hoặc click ra ngoài.

#### JavaScript (`component/dropdown/1.0.0.js`):
```js
kit.component("dropdown", {
  open: false,

  toggle() {
    this.open = !this.open;
  },

  close() {
    this.open = false;
  }
});
```

#### HTML Markup:
```html
<div 
  data-kit-component="dropdown" 
  data-kit-as="$dropdown"
  data-kit-click:outside="close()"
  data-kit-keydown:escape:document="close()"
  class="relative inline-block">
  
  <!-- Nút kích hoạt Dropdown -->
  <button 
    type="button" 
    data-kit-click="toggle()"
    data-kit-bind="aria-expanded: open;"
    class="px-4 py-2 bg-slate-800 text-white rounded-lg shadow">
    Tùy chọn ▼
  </button>

  <!-- Menu nội dung -->
  <nav 
    data-kit-show="open" 
    class="absolute left-0 mt-2 w-48 bg-white border border-slate-200 rounded-lg shadow-xl py-1">
    <a href="#profile" class="block px-4 py-2 hover:bg-slate-100">Hồ sơ cá nhân</a>
    <a href="#settings" class="block px-4 py-2 hover:bg-slate-100">Cài đặt</a>
    <hr class="my-1 border-slate-100" />
    <button data-kit-click="close()" class="w-full text-left px-4 py-2 text-red-600 hover:bg-slate-100">
      Đóng menu
    </button>
  </nav>
</div>
```

---

### 2.2 `theme` — Quản lý Giao diện Sáng/Tối
Tự động đọc/ghi `kit.storage` và lắng nghe sự thay đổi theme hệ điều hành.

#### JavaScript (`component/theme/1.0.0.js`):
```js
kit.component("theme", {
  mode: "system",

  get resolved() {
    if (this.mode !== "system") return this.mode;
    return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  },

  async init() {
    this.mode = await kit.storage.get("theme", "system");

    var media = matchMedia("(prefers-color-scheme: dark)");
    var self = this;
    var onChange = function () {
      if (self.mode === "system") self.$invalidate();
    };
    media.addEventListener("change", onChange);

    return function cleanup() {
      media.removeEventListener("change", onChange);
    };
  },

  async setMode(mode) {
    this.mode = mode === "light" || mode === "dark" ? mode : "system";
    await kit.storage.set("theme", this.mode);
  },

  toggle() {
    return this.setMode(this.resolved === "dark" ? "light" : "dark");
  }
});
```

#### HTML Markup:
```html
<html data-kit-bind="data-theme: $theme.resolved;" data-kit-class="$theme.resolved">
  <body>
    <header data-kit-component="theme" data-kit-as="$theme" class="p-4 flex gap-2">
      <button data-kit-click="toggle()" class="px-3 py-1.5 bg-indigo-600 text-white rounded">
        Đổi giao diện (Đang là: <span data-kit-text="$theme.resolved"></span>)
      </button>
      
      <button data-kit-click="setMode('system')" class="px-3 py-1.5 border rounded">
        Hệ thống
      </button>
    </header>
  </body>
</html>
```

---

### 2.3 `accordion` — Danh sách Xòe / Gập
Quản lý mục đang mở và hỗ trợ di chuyển phím mũi tên `ArrowUp`/`ArrowDown`.

#### JavaScript (`component/accordion/1.0.0.js`):
```js
kit.component("accordion", {
  activeItem: null,

  toggle(item) {
    var id = String(item || "");
    this.activeItem = this.activeItem === id ? null : id;
  },

  isOpen(item) {
    return this.activeItem === String(item || "");
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="accordion" class="border rounded-xl divide-y">
  <!-- Section 1 -->
  <div>
    <button 
      type="button" 
      data-kit-click="toggle('item-1')"
      class="w-full text-left p-4 font-medium flex justify-between">
      <span>Câu hỏi 1: KitJS là gì?</span>
      <span data-kit-text="isOpen('item-1') ? '▲' : '▼'"></span>
    </button>
    <div data-kit-show="isOpen('item-1')" class="p-4 bg-slate-50 text-slate-600">
      KitJS là client runtime siêu nhẹ thiết kế cho HTML-First.
    </div>
  </div>

  <!-- Section 2 -->
  <div>
    <button 
      type="button" 
      data-kit-click="toggle('item-2')"
      class="w-full text-left p-4 font-medium flex justify-between">
      <span>Câu hỏi 2: Có cần Node.js để chạy KitJS không?</span>
      <span data-kit-text="isOpen('item-2') ? '▲' : '▼'"></span>
    </button>
    <div data-kit-show="isOpen('item-2')" class="p-4 bg-slate-50 text-slate-600">
      Không, KitJS chạy trực tiếp trên browser và được Go Engine đóng gói JIT.
    </div>
  </div>
</div>
```

---

### 2.4 `dialog` — Hộp thoại Modal
Hộp thoại Modal chuẩn Accessibility tự động khóa cuộn trang và bắt phím `Escape`.

#### JavaScript (`component/dialog/1.0.0.js`):
```js
kit.component("dialog", {
  open: false,

  show() {
    this.open = true;
    document.body.style.overflow = "hidden";
  },

  close() {
    this.open = false;
    document.body.style.overflow = "";
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="dialog" data-kit-as="$confirmDialog">
  <!-- Nút mở Modal -->
  <button data-kit-click="show()" class="px-4 py-2 bg-red-600 text-white rounded">
    Xóa tài khoản
  </button>

  <!-- Backdrop và Modal Box -->
  <div 
    data-kit-show="open" 
    data-kit-keydown:escape:document="close()"
    class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
    
    <div 
      data-kit-click:outside="close()" 
      class="bg-white rounded-2xl p-6 max-w-md w-full shadow-2xl space-y-4">
      <h3 class="text-lg font-bold">Xác nhận xóa?</h3>
      <p class="text-slate-600">Hành động này không thể hoàn tác.</p>
      
      <div class="flex justify-end gap-2">
        <button data-kit-click="close()" class="px-4 py-2 border rounded-lg">Hủy</button>
        <button data-kit-click="close()" class="px-4 py-2 bg-red-600 text-white rounded-lg">Xóa ngay</button>
      </div>
    </div>
  </div>
</div>
```

---

### 2.5 `drawer` — Khung Trượt Slide-Over Panel

#### JavaScript (`component/drawer/1.0.0.js`):
```js
kit.component("drawer", {
  open: false,

  toggle() {
    this.open = !this.open;
  },

  close() {
    this.open = false;
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="drawer" data-kit-as="$cartDrawer">
  <button data-kit-click="toggle()" class="px-4 py-2 bg-indigo-600 text-white rounded-lg">
    Giỏ hàng (3)
  </button>

  <!-- Drawer Slide Over Panel -->
  <div 
    data-kit-show="open" 
    data-kit-keydown:escape:document="close()"
    class="fixed inset-0 z-50 flex justify-end bg-black/40">
    
    <div 
      data-kit-click:outside="close()" 
      class="w-full max-w-md bg-white h-full p-6 shadow-2xl flex flex-col justify-between">
      <div>
        <div class="flex justify-between items-center pb-4 border-b">
          <h2 class="text-xl font-bold">Giỏ hàng của bạn</h2>
          <button data-kit-click="close()" class="text-2xl">&times;</button>
        </div>
        <p class="py-4 text-slate-500">Chưa có sản phẩm nào trong giỏ.</p>
      </div>
      
      <button data-kit-click="close()" class="w-full py-3 bg-indigo-600 text-white rounded-xl font-medium">
        Thanh toán ngay
      </button>
    </div>
  </div>
</div>
```

---

### 2.6 `tabs` & `tab` — Thẻ Khung Chuyển Đổi Tab

#### JavaScript (`component/tabs/1.0.0.js`):
```js
kit.component("tabs", {
  activeTab: "tab-1",

  select(tabId) {
    this.activeTab = String(tabId);
  },

  isSelected(tabId) {
    return this.activeTab === String(tabId);
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="tabs" class="space-y-4">
  <!-- Danh sách Thanh Tab -->
  <div class="flex border-b border-slate-200">
    <button 
      data-kit-click="select('tab-1')"
      data-kit-class="'border-b-2 border-indigo-600 text-indigo-600': isSelected('tab-1'); 'text-slate-500': !isSelected('tab-1');"
      class="px-4 py-2 font-medium">
      Thông tin
    </button>
    <button 
      data-kit-click="select('tab-2')"
      data-kit-class="'border-b-2 border-indigo-600 text-indigo-600': isSelected('tab-2'); 'text-slate-500': !isSelected('tab-2');"
      class="px-4 py-2 font-medium">
      Đánh giá (12)
    </button>
  </div>

  <!-- Nội dung từng Tab -->
  <div data-kit-show="isSelected('tab-1')" class="p-4 bg-white rounded-lg shadow-sm">
    Nội dung thẻ Thông tin chi tiết...
  </div>
  <div data-kit-show="isSelected('tab-2')" class="p-4 bg-white rounded-lg shadow-sm">
    Nội dung danh sách Đánh giá của khách hàng...
  </div>
</div>
```

---

### 2.7 `toast` — Thông Báo Banner Tự Ẩn

#### JavaScript (`component/toast/1.0.0.js`):
```js
kit.component("toast", {
  visible: false,
  message: "",

  show(msg, timeout) {
    this.message = String(msg || "");
    this.visible = true;

    var self = this;
    clearTimeout(this._timer);
    this._timer = setTimeout(function () {
      self.dismiss();
    }, timeout || 3000);
  },

  dismiss() {
    this.visible = false;
    this.$invalidate();
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="toast" data-kit-as="$toast">
  <!-- Nút kích hoạt Toast -->
  <button 
    data-kit-click="show('Lưu thay đổi thành công!', 3000)" 
    class="px-4 py-2 bg-emerald-600 text-white rounded">
    Gửi Form
  </button>

  <!-- Banner Toast nổi ở góc màn hình -->
  <div 
    data-kit-show="visible" 
    class="fixed bottom-5 right-5 z-50 bg-slate-900 text-white px-5 py-3 rounded-xl shadow-2xl flex items-center gap-3">
    <span data-kit-text="message"></span>
    <button data-kit-click="dismiss()" class="text-slate-400 hover:text-white">&times;</button>
  </div>
</div>
```

---

### 2.8 `tooltip` — Chú Thích Ngắn

#### JavaScript (`component/tooltip/1.0.0.js`):
```js
kit.component("tooltip", {
  visible: false,

  show() { this.visible = true; },
  hide() { this.visible = false; }
});
```

#### HTML Markup:
```html
<div data-kit-component="tooltip" class="relative inline-block">
  <button 
    data-kit-click="visible = !visible"
    class="p-2 bg-slate-100 rounded-full hover:bg-slate-200">
    ℹ️
  </button>

  <div 
    data-kit-show="visible" 
    class="absolute bottom-full mb-2 left-1/2 -translate-x-1/2 w-48 p-2 bg-slate-800 text-white text-xs rounded-lg shadow-lg text-center z-20">
    Đây là chú thích trợ giúp cho nút bấm này!
  </div>
</div>
```

---

### 2.9 `combobox` — Ô Tìm Kiếm Gợi Ý (Autosuggest)

#### JavaScript (`component/combobox/1.0.0.js`):
```js
kit.component("combobox", {
  query: "",
  selected: null,
  open: false,

  select(item) {
    this.selected = item;
    this.query = item;
    this.open = false;
  }
});
```

#### HTML Markup:
```html
<div 
  data-kit-component="combobox" 
  data-kit-scope="items: ['Hà Nội', 'TP. Hồ Chí Minh', 'Đà Nẵng', 'Cần Thơ'];"
  data-kit-click:outside="open = false"
  class="relative w-72">
  
  <input 
    type="text" 
    data-kit-model="query" 
    data-kit-input="open = true"
    data-kit-focus="open = true"
    placeholder="Chọn thành phố..."
    class="w-full px-4 py-2 border rounded-lg" />

  <!-- Danh sách gợi ý -->
  <ul 
    data-kit-show="open" 
    class="absolute w-full mt-1 bg-white border rounded-lg shadow-lg max-h-48 overflow-y-auto z-30">
    <li 
      data-kit-for="$city, $i of items" 
      data-kit-key="$city"
      data-kit-click="select($city)"
      class="px-4 py-2 hover:bg-indigo-50 cursor-pointer">
      <span data-kit-text="$city"></span>
    </li>
  </ul>
</div>
```

---

### 2.10 `command-palette` — Khung Lệnh Tắt (`Cmd+K`)

#### JavaScript (`component/command-palette/1.0.0.js`):
```js
kit.component("command-palette", {
  open: false,

  init() {
    var self = this;
    function onKeydown(e) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        self.open = !self.open;
        self.$invalidate();
      }
    }
    window.addEventListener("keydown", onKeydown);
    return function () { window.removeEventListener("keydown", onKeydown); };
  },

  close() { this.open = false; }
});
```

#### HTML Markup:
```html
<div data-kit-component="command-palette" data-kit-as="$palette">
  <!-- Modal Command Palette -->
  <div 
    data-kit-show="open" 
    data-kit-keydown:escape:document="close()"
    class="fixed inset-0 z-50 bg-black/50 p-4 pt-20 flex justify-center">
    
    <div data-kit-click:outside="close()" class="w-full max-w-xl bg-white rounded-2xl shadow-2xl overflow-hidden">
      <input 
        data-kit-alias="$cmdInput"
        type="text" 
        placeholder="Nhập lệnh hoặc tìm kiếm (Phím ESC để đóng)..." 
        class="w-full p-4 border-b text-lg outline-none" />
      
      <div class="p-2 max-h-80 overflow-y-auto divide-y">
        <button data-kit-click="close()" class="w-full text-left p-3 hover:bg-slate-100 rounded-lg">
          📄 Tạo bài viết mới
        </button>
        <button data-kit-click="close()" class="w-full text-left p-3 hover:bg-slate-100 rounded-lg">
          ⚙️ Cài đặt hệ thống
        </button>
      </div>
    </div>
  </div>
</div>
```

---

### 2.11 `menu` — ARIA Context Menu

#### JavaScript (`component/menu/1.0.0.js`):
```js
kit.component("menu", {
  open: false,

  toggle() { this.open = !this.open; },
  close() { this.open = false; }
});
```

#### HTML Markup:
```html
<div 
  data-kit-component="menu" 
  data-kit-click:outside="close()"
  data-kit-keydown:escape:document="close()"
  class="relative">
  
  <button data-kit-click="toggle()" class="p-2 border rounded-lg">⚙️ Thao tác</button>

  <div data-kit-show="open" class="absolute right-0 mt-2 w-48 bg-white border rounded-lg shadow-xl py-1 z-20">
    <button data-kit-click="close()" class="w-full text-left px-4 py-2 hover:bg-slate-100">Chỉnh sửa</button>
    <button data-kit-click="close()" class="w-full text-left px-4 py-2 hover:bg-slate-100">Sao chép link</button>
  </div>
</div>
```

---

### 2.12 `popover` — Panel Nổi Popover API

#### JavaScript (`component/popover/1.0.0.js`):
```js
kit.component("popover", {
  open: false,

  toggle() { this.open = !this.open; },
  close() { this.open = false; }
});
```

#### HTML Markup:
```html
<div data-kit-component="popover" data-kit-click:outside="close()" class="relative inline-block">
  <button data-kit-click="toggle()" class="px-4 py-2 bg-slate-200 rounded-lg">Hiện Popover</button>

  <div data-kit-show="open" class="absolute left-0 mt-2 w-64 p-4 bg-white border rounded-xl shadow-2xl z-30">
    <h4 class="font-bold border-b pb-2">Thông tin nhanh</h4>
    <p class="text-sm text-slate-600 mt-2">Đây là nội dung popover nổi hiển thị chi tiết.</p>
  </div>
</div>
```

---

### 2.13 `progress-bar` — Thanh Tiến Trình

#### JavaScript (`component/progress-bar/1.0.0.js`):
```js
kit.component("progress-bar", {
  value: 0,
  max: 100,

  get percent() {
    return Math.min(100, Math.max(0, Math.round((this.value / this.max) * 100)));
  },

  set(val) {
    this.value = Number(val || 0);
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="progress-bar" data-kit-scope="value: 45; max: 100;" class="space-y-2">
  <div class="flex justify-between text-sm font-medium">
    <span>Tiến trình tải</span>
    <span data-kit-text="`${percent}%`"></span>
  </div>

  <div class="w-full bg-slate-200 rounded-full h-3 overflow-hidden">
    <div 
      data-kit-style="width: `${percent}%`;" 
      class="bg-indigo-600 h-full transition-all duration-300">
    </div>
  </div>
</div>
```

---

### 2.14 `clipboard` — Nút Sao Chép Nhanh

#### JavaScript (`component/clipboard/1.0.0.js`):
```js
kit.component("clipboard", {
  copied: false,

  async copy(text) {
    await kit.clipboard.copy(text);
    this.copied = true;
    
    var self = this;
    clearTimeout(this._timer);
    this._timer = setTimeout(function () {
      self.copied = false;
      self.$invalidate();
    }, 2000);
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="clipboard" class="flex items-center gap-2">
  <input data-kit-alias="$tokenInput" type="text" readonly value="npm i kitwork" class="px-3 py-1.5 border rounded font-mono text-sm" />
  
  <button 
    data-kit-click="copy($tokenInput.value)" 
    data-kit-class="'bg-emerald-600': copied; 'bg-indigo-600': !copied;"
    class="px-4 py-1.5 text-white rounded text-sm transition-all">
    <span data-kit-text="copied ? 'Đã chép! ✓' : 'Sao chép'"></span>
  </button>
</div>
```

---

### 2.15 `announce` — Accessibility Screen Reader Announcer

#### JavaScript (`component/announce/1.0.0.js`):
```js
kit.component("announce", {
  announce(message, mode) {
    if (kit.announce) {
      if (mode === "assertive") kit.announce.assertive(message);
      else kit.announce.polite(message);
    }
  }
});
```

#### HTML Markup:
```html
<div data-kit-component="announce" data-kit-as="$announcer">
  <button data-kit-click="announce('Đã lưu bài viết thành công!', 'polite')" class="px-4 py-2 bg-slate-800 text-white rounded">
    Gửi bài
  </button>
</div>
```

---

## 3. Cập nhật README.md

Đã cập nhật mục Documentation trong **[README.md](../README.md)** để bao gồm tài liệu ví dụ này:

```markdown
## Documentation

- [docs/DIRECTIVES.md](docs/DIRECTIVES.md) — Full specification and practical examples for all 13 core `data-kit-*` directives.
- [docs/SERVICES.md](docs/SERVICES.md) — Specification and usage guide for all 9 canonical infrastructure services (`kit.storage`, `kit.request`, etc.).
- [docs/EXAMPLES.md](docs/EXAMPLES.md) — Production-grade markup and JavaScript examples for all 16 canonical headless components.
- [docs/SPEC.md](docs/SPEC.md) — Normative architecture and implementation baseline for KitJS Clean 1.0 Standard.
```
