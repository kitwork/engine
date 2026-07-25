Mình sẽ **không sửa chắp vá runtime cũ**. Hãy giữ lại phần đã chứng minh được giá trị, rồi viết một kernel mới có ranh giới rõ ràng.

Runtime cũ hiện đang trộn bốn vai trò:

```text
expression/runtime
reactive UI
SPA Drive
platform capabilities
```

Nó còn chứa CDN component loader, IndexedDB cache, native bridge thử nghiệm, camera, clipboard, theme và window trong cùng file. 

## 1. Những phần nên giữ

Giữ trong kernel cốt lõi:

```text
lex
parse
run
directive
scope
component
render
model
validate
delegated events
live/SSE
API data source
morph
```

Đây là bản sắc thực sự của Kitwork client runtime.

Có thể giữ `Drive`, nhưng tách thành module riêng:

```text
kitwork.drive
```

Runtime cũ hiện đã xuất trực tiếp khá nhiều hàm nội bộ như `compile`, `run`, `scopeFor`, `render`, `morph`, `syncApi` và `streams`.  Mình sẽ giảm public API này xuống mức cần thiết.

## 2. Những phần nên loại khỏi kernel

Xóa khỏi kernel chính:

```text
bridge
isNative
CDN component loader
IndexedDB blueprint cache
camera
clipboard
window aliases
theme property đặc biệt
fetchWithRetry
window.hydrate alias
```

Cụ thể bỏ:

```js
kitwork.bridge
kitwork.isNative

kitwork.toggleTheme
kitwork.clipboard = function () {}
kitwork.camera = function () {}

kitwork.window = function () {}
kitwork.minimize
kitwork.maximize
kitwork.closeWindow

window.hydrate = kitwork
```

Bỏ cả:

```text
getDB
dbGet
dbSet
injectScriptCode
fetchCodeAndStore
loadComponentFromCDN
```

Component registration nên là local/JIT trước. Khi thật sự cần remote components, xây thành package riêng.

## 3. Cấu trúc mới

```text
kit.js
├── runtime metadata
├── module registry
├── expression compiler
├── IR walker
├── scopes
├── reactive render
├── components/actions
├── delegated listeners
├── live/api
└── lifecycle

kit.drive.js
kit.storage.js
kit.theme.js
kit.platform.js
kit.device.js
kit.display.js
kit.network.js
kit.clipboard.js
kit.files.js
kit.media.js
```

Tất cả vẫn dùng chung:

```js
window.kitwork
```

Không tạo global thứ hai.

---

# 4. Phần đầu của kernel mới

```js
// Kitwork client kernel.
//
// One root.
// One expression runtime.
// One component registry.
// One delegated event system.
// One DOM observer.
//
// No eval.
// No new Function.
(function (window, document) {
  "use strict";

  var existing = window.kitwork || {};

  if (
    existing.runtime &&
    existing.runtime.booted
  ) {
    return;
  }

  var kitwork = existing;

  window.kitwork = kitwork;

  kitwork.runtime = {
    name: "kitwork",
    version: "1.0.0",
    engine: "web",
    development: false,
    booted: true,

    info: function () {
      return {
        name: this.name,
        version: this.version,
        engine: this.engine,
        development: this.development
      };
    }
  };

  // Runtime modules currently installed.
  var modules = Object.create(null);

  kitwork.module = function (name, value) {
    if (
      typeof name !== "string" ||
      name.trim() === ""
    ) {
      throw new TypeError(
        "kitwork.module: name is required"
      );
    }

    if (value === undefined) {
      return modules[name] || null;
    }

    if (modules[name]) {
      return modules[name];
    }

    modules[name] = value;
    kitwork[name] = value;

    return value;
  };

  kitwork.has = function (name) {
    return !!modules[name];
  };

  kitwork.modules = function () {
    return Object.keys(modules);
  };

  // Kernel implementation continues here.
})(window, document);
```

Các capability có thể đăng ký sau:

```js
kitwork.module("storage", storage);
kitwork.module("theme", theme);
kitwork.module("platform", platform);
```

Kết quả vẫn là:

```js
kitwork.storage
kitwork.theme
kitwork.platform
```

Nhưng kernel không cần biết implementation của chúng.

---

# 5. Runtime nội bộ không nên nằm hết trên `kitwork`

Thay vì xuất tất cả:

```js
kitwork.scope
kitwork.scopeFor
kitwork.streams
kitwork.sync
kitwork.syncApi
```

hãy giữ private:

```js
var raw = {};
var streams = {};
var behaviors = {};
var blueprints = {};
```

Chỉ xuất API cần dùng thật:

```js
kitwork.compile
kitwork.run
kitwork.get
kitwork.set
kitwork.update
kitwork.render
kitwork.component
kitwork.action
```

Ví dụ:

```js
kitwork.get = function (key) {
  return scope[key];
};

kitwork.set = function (key, value) {
  scope[key] = value;
  scheduleRender();

  return value;
};

kitwork.update = function (values) {
  if (
    !values ||
    typeof values !== "object"
  ) {
    return kitwork;
  }

  Object.keys(values).forEach(function (key) {
    scope[key] = values[key];
  });

  scheduleRender();

  return kitwork;
};
```

Không gọi `render()` trực tiếp ở mọi nơi. Dùng:

```js
scheduleRender()
```

để nhiều thay đổi trong một tick chỉ render một lần.

---

# 6. Thêm lifecycle tối thiểu

Đây là phần cần thiết trước khi tích hợp media, sensors và recorder.

```js
var cleanups = new Set();

kitwork.cleanup = function (callback) {
  if (typeof callback !== "function") {
    throw new TypeError(
      "kitwork.cleanup: callback must be a function"
    );
  }

  cleanups.add(callback);

  return function () {
    cleanups.delete(callback);
  };
};

kitwork.destroy = function () {
  cleanups.forEach(function (cleanup) {
    try {
      cleanup();
    } catch (_) {}
  });

  cleanups.clear();
};
```

Các module như network có thể đăng ký cleanup:

```js
function onOnline() {
  // ...
}

window.addEventListener("online", onOnline);

kitwork.cleanup(function () {
  window.removeEventListener(
    "online",
    onOnline
  );
});
```

Nhưng lifecycle toàn runtime chưa đủ cho component. Vì vậy thêm cleanup theo element.

```js
var stateKey = Symbol("kitwork");

function state(element) {
  if (!element[stateKey]) {
    element[stateKey] = {
      cleanups: []
    };
  }

  return element[stateKey];
}

function cleanupElement(element) {
  var current = element[stateKey];

  if (!current || !current.cleanups) {
    return;
  }

  current.cleanups.forEach(function (cleanup) {
    try {
      cleanup();
    } catch (_) {}
  });

  current.cleanups.length = 0;
}

kitwork.onCleanup = function (
  element,
  callback
) {
  if (!element) {
    throw new TypeError(
      "kitwork.onCleanup: element is required"
    );
  }

  if (typeof callback !== "function") {
    throw new TypeError(
      "kitwork.onCleanup: callback must be a function"
    );
  }

  state(element).cleanups.push(callback);

  return callback;
};
```

Khi observer thấy node bị remove, gọi `cleanupElement()` cho node và descendants.

```js
function cleanupTree(node) {
  if (!node || node.nodeType !== 1) {
    return;
  }

  cleanupElement(node);

  node
    .querySelectorAll("*")
    .forEach(cleanupElement);
}
```

Observer:

```js
var observer = new MutationObserver(
  function (records) {
    records.forEach(function (record) {
      record.removedNodes.forEach(
        cleanupTree
      );
    });

    scheduleSync();
  }
);

observer.observe(document.documentElement, {
  childList: true,
  subtree: true
});
```

Bây giờ camera stream, sensor watch và event listener có nơi cleanup thật sự.

---

# 7. Async contract mới

Không cần thêm `await` vào expression parser ngay.

Expression có thể gọi Promise-returning method, và event runner tự xử lý Promise.

Hiện listener cũ chạy expression rồi render ngay:

```js
run(x, elementScope(ex));
render();
```

Thay bằng:

```js
function execute(ir, currentScope) {
  var result;

  try {
    result = run(ir, currentScope);
  } catch (error) {
    console.error(error);
    return Promise.reject(error);
  }

  if (
    result &&
    typeof result.then === "function"
  ) {
    return result.then(function (value) {
      scheduleRender();
      return value;
    });
  }

  scheduleRender();

  return Promise.resolve(result);
}
```

Click listener:

```js
document.addEventListener(
  "click",
  function (event) {
    var element =
      event.target.closest &&
      event.target.closest(
        selector("click")
      );

    if (!element) {
      return;
    }

    var expression =
      directive(element, "click");

    if (!expression) {
      return;
    }

    execute(
      expression,
      elementScope(element)
    ).catch(function (error) {
      var current = scopeFor(element);

      current.error =
        error && error.message
          ? error.message
          : String(error);

      scheduleRender();
    });
  }
);
```

Như vậy:

```html
<button data-kit-click="$app.clipboard.write(text)">
```

vẫn chạy dù trả Promise.

Tuy nhiên, biểu thức vẫn chưa thể viết:

```js
photo = await $app.camera.take()
```

Muốn cập nhật state, component method sẽ là nơi phù hợp:

```js
kitwork.component("camera", {
  photo: null,
  error: null,

  take: function () {
    var self = this;

    return kitwork.camera
      .take()
      .then(function (blob) {
        self.photo =
          URL.createObjectURL(blob);
      })
      .catch(function (error) {
        self.error = error.message;
      });
  }
});
```

Khi Promise hoàn thành, `execute()` sẽ render lại.

---

# 8. Theme mới

Bỏ `Object.defineProperty(kitwork, "theme", ...)` cũ và `$theme` alias. Runtime cũ đang dùng theme vừa như string vừa như capability, khiến API khó mở rộng. 

Module mới:

```js
(function (kitwork) {
  "use strict";

  if (kitwork.has("theme")) {
    return;
  }

  var theme = {
    get: function () {
      var saved =
        kitwork.storage.getSync("theme");

      if (
        saved === "light" ||
        saved === "dark"
      ) {
        return saved;
      }

      return document.documentElement
        .classList.contains("dark")
        ? "dark"
        : "light";
    },

    set: function (value) {
      var next =
        value === "dark"
          ? "dark"
          : "light";

      document.documentElement
        .classList.toggle(
          "dark",
          next === "dark"
        );

      kitwork.storage.set(
        "theme",
        next
      );

      return next;
    },

    toggle: function () {
      return this.set(
        this.get() === "dark"
          ? "light"
          : "dark"
      );
    }
  };

  kitwork.module("theme", theme);
})(window.kitwork);
```

Markup:

```html
<button data-kit-click="$app.theme.toggle()">
  Toggle theme
</button>
```

---

# 9. Window vẫn là trường hợp đặc biệt

Vì expression sandbox đang chặn `"window"` để không lần ra browser global, đừng dùng:

```html
data-kit-click="$app.window.close()"
```

Giữ directive riêng:

```html
<header data-kit-drag="true">
  <button
    data-kit-drag="false"
    data-kit-window="close">
    Close
  </button>
</header>
```

Kernel chỉ dispatch:

```js
var DRAG =
  '[data-kit-drag="true"]';

var UNDRAG =
  '[data-kit-drag="false"]';

var WINDOW =
  "[data-kit-window]";

document.addEventListener(
  "mousedown",
  function (event) {
    if (event.button !== 0) return;
    if (!event.target.closest) return;
    if (event.target.closest(UNDRAG)) return;

    if (
      event.target.closest(DRAG) &&
      kitwork.window
    ) {
      kitwork.window.drag();
    }
  }
);

document.addEventListener(
  "dblclick",
  function (event) {
    if (!event.target.closest) return;
    if (event.target.closest(UNDRAG)) return;

    if (
      event.target.closest(DRAG) &&
      kitwork.window
    ) {
      kitwork.window.maximize();
    }
  }
);

document.addEventListener(
  "click",
  function (event) {
    if (!event.target.closest) return;

    var element =
      event.target.closest(WINDOW);

    if (!element || !kitwork.window) {
      return;
    }

    var action =
      element.getAttribute(
        "data-kit-window"
      );

    if (
      action === "minimize" ||
      action === "maximize" ||
      action === "close"
    ) {
      kitwork.window[action]();
    }
  }
);
```

Nếu web không có `kitwork.window`, không có gì xảy ra.

---

# 10. Storage và remember

Runtime cũ có một hệ thống persistence riêng cho `data-kit-remember`, trực tiếp dùng `localStorage`. 

Nên giữ `remember`, nhưng cho nó dùng `kitwork.storage` thay vì tạo hệ thống storage thứ hai.

Tuy nhiên, `storage.get()` của anh là async. Reactive property getter không thể `await`. Vì vậy có hai lựa chọn:

### Cách đơn giản nhất

Giữ một adapter đồng bộ nội bộ cho web:

```js
kitwork.storage.getSync()
kitwork.storage.setSync()
```

Public API vẫn là async:

```js
await kitwork.storage.get()
await kitwork.storage.set()
```

Implementation:

```js
getSync: function (key, options) {
  options = options || {};

  var value =
    localStorage.getItem(this.key(key));

  if (value === null) {
    return options.default;
  }

  try {
    return JSON.parse(value);
  } catch (_) {
    return value;
  }
},

setSync: function (key, value) {
  localStorage.setItem(
    this.key(key),
    JSON.stringify(value)
  );

  return true;
},

get: function (key, options) {
  return Promise.resolve(
    this.getSync(key, options)
  );
},

set: function (key, value) {
  this.setSync(key, value);

  return Promise.resolve(true);
}
```

`remember` dùng sync methods. Sau này native storage không hỗ trợ sync thì `remember` cần preload trước khi boot; chưa cần giải quyết lúc này.

---

# 11. Drive nên tách ra

Kernel mới không tự động sở hữu navigation.

`kit.drive.js`:

```js
(function (kitwork) {
  "use strict";

  if (
    kitwork.has("drive") ||
    !window.fetch ||
    !window.history.pushState
  ) {
    return;
  }

  var drive = {
    started: false,

    start: function () {
      if (this.started) {
        return this;
      }

      this.started = true;

      // initDrive implementation here.

      return this;
    },

    stop: function () {
      if (!this.started) {
        return this;
      }

      this.started = false;

      // Remove Drive listeners here.

      return this;
    }
  };

  kitwork.module("drive", drive);
})(window.kitwork);
```

Trang muốn SPA navigation:

```html
<script src="/kit.js"></script>
<script src="/kit/drive.js"></script>
```

Hoặc Go JIT gộp module Drive khi thấy:

```html
data-kit-app
```

Như vậy `kit.js` không còn mặc định là SPA router.

---

# 12. Thứ tự viết lại

Mình sẽ làm theo thứ tự này:

```text
1. Sao chép runtime cũ thành kit.legacy.js
2. Tạo kit.js mới với root + runtime + module registry
3. Chuyển lex/parse/run sang kernel mới
4. Chuyển scope/component/render
5. Chuyển delegated events
6. Thêm async execute()
7. Thêm lifecycle cleanup
8. Chuyển live/api
9. Chuyển morph
10. Tách Drive thành module
11. Thêm storage/theme/platform cơ bản
12. Chạy compatibility tests
```

Không bắt đầu bằng việc tích hợp camera, media hay sensors.

# Bản kernel mới nên có hình dạng cuối cùng

```js
(function (window, document) {
  "use strict";

  var kitwork =
    window.kitwork ||
    (window.kitwork = {});

  if (
    kitwork.runtime &&
    kitwork.runtime.booted
  ) {
    return;
  }

  kitwork.runtime = {
    name: "kitwork",
    version: "1.0.0",
    engine: "web",
    development: false,
    booted: true
  };

  var modules = Object.create(null);
  var behaviors = Object.create(null);
  var blueprints = Object.create(null);
  var streams = Object.create(null);
  var raw = {};
  var cleanups = new Set();
  var stateKey = Symbol("kitwork");

  kitwork.module = registerModule;
  kitwork.has = hasModule;

  // Expression:
  // lex
  // parse
  // run
  // execute

  // State:
  // scope
  // scopeFor
  // elementScope

  // UI:
  // directive
  // render
  // scheduleRender

  // Components:
  // component
  // action
  // state
  // cleanup

  // Data:
  // syncApi
  // syncLive

  // DOM:
  // morph
  // observer
  // delegated listeners

  // Minimal public API:
  kitwork.compile = compile;
  kitwork.run = executeIR;
  kitwork.get = get;
  kitwork.set = set;
  kitwork.update = update;
  kitwork.render = scheduleRender;
  kitwork.component = component;
  kitwork.action = action;
  kitwork.cleanup = cleanup;
  kitwork.destroy = destroy;

  boot();

  function registerModule(name, value) {
    if (value === undefined) {
      return modules[name] || null;
    }

    if (modules[name]) {
      return modules[name];
    }

    modules[name] = value;
    kitwork[name] = value;

    return value;
  }

  function hasModule(name) {
    return !!modules[name];
  }

  function boot() {
    seedModels();
    syncApi();
    syncLive();
    scheduleRender();
  }
})(window, document);
```

Đây là hướng mình chọn: **viết lại từ lõi, không bê toàn bộ capability vào kernel, không giữ compatibility vô thời hạn**. Runtime mới vẫn kế thừa những phần mạnh nhất của bản cũ, nhưng mỗi phần chỉ có một trách nhiệm.
