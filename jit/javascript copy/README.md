# 🏛️ Kitwork Client Runtime — Architecture & Implementation Specification

> **Status:** Architecture Draft 0.7 — Implementation Baseline  
> **Specification Version:** 0.7.0-draft  
> **Target Engine Version:** 1.0.0-draft  
> **Client Runtime Version:** next  
> **Normative Location:** `engine/jit/javascript/README.md`  
> **Purpose:** Hợp đồng triển khai duy nhất giữa Go Engine, JavaScript Runtime, Drive, Platform Adapters, tests và các Coding Agents.

---

## 0. Cách đọc tài liệu

Các từ khóa sau mang ý nghĩa chuẩn hóa:

- **MUST / BẮT BUỘC**: Không được triển khai khác nếu vẫn tuyên bố tương thích specification.
- **MUST NOT / KHÔNG ĐƯỢC**: Hành vi bị cấm.
- **SHOULD / NÊN**: Mặc định phải làm như vậy; chỉ thay đổi khi có lý do kỹ thuật được ghi lại.
- **MAY / CÓ THỂ**: Tùy chọn hợp lệ.

Specification này khóa **semantics**, không khóa cấu trúc AST nội bộ, tên hàm private hoặc cách tối ưu hóa. Go và JavaScript có thể dùng implementation khác nhau, nhưng phải cho cùng kết quả quan sát được và vượt qua cùng conformance fixtures.

---

# 1. Triết lý cốt lõi

> **“The page already exists. Runtime JS makes it alive.”**

Kitwork Client Runtime vận hành theo nguyên lý **HTML First**:

1. Server render HTML có nội dung và cấu trúc sử dụng được ngay.
2. Server sở hữu canonical business state và luôn xác thực lại mọi mutation.
3. Client runtime chỉ hydrate behavior, local interaction state, form state và platform capabilities.
4. JavaScript bị tắt không được làm trang mất nội dung chính hoặc khiến form cơ bản không thể submit.
5. Client runtime không trở thành một client template framework hoặc một SPA state store tổng quát.

Luồng dữ liệu chuẩn:

```text
SSR / Parent Lexical Context
            ↓
      data-kit-scope
  SSR-seeded + mutable client state
            ↓
 Component methods / Action Programs
            ↓
 text / class / style / bind / model
            ↓
            DOM
```

## 1.1 Quyết định nền tảng

| Chủ đề | Quyết định chuẩn |
| :--- | :--- |
| Rendering | HTML First; server render hoàn chỉnh, client hydrate tăng cường. |
| Server data | Dữ liệu client behavior cần dùng đi vào `data-kit-scope`. Không có `data-kit-props` hoặc `$props` trong 1.0. |
| Client state | Mutable lexical/component state; khởi tạo đúng một lần khi mount/hydrate. |
| Runtime contexts | Dùng `$`: `$element`, `$host`, `$event`, `$refs`, `$component`, `$parent`, loop locals và direct aliases. |
| Services | Dùng namespace `kit.*`; authored expressions chỉ thấy member được explicit grant. |
| Expressions | Private cached AST; không `eval`, không `new Function`, không public/serialized IR. |
| Attributes | Chỉ dùng `data-kit-bind`; không tách `data-kit-data`, `data-kit-aria`, `data-kit-attribute`. |
| Events | Chỉ dùng `data-kit-<event>:<modifier>`. Không có `data-kit-action` hoặc companion modifier attributes. |
| Component aliases | `data-kit-as="$paymentModal"` và truy cập trực tiếp `$paymentModal.open()`. |
| Component inputs | `data-kit-scope` trên component host seed component instance state. |
| Persistence | `data-kit-persist="key"` là keyed reuse trong Drive, không phải node bất tử. |
| Progressive enhancement | Link và form cơ bản phải tiếp tục hoạt động khi runtime không chạy. |

## 1.2 Non-goals của Client Runtime 1.0

Runtime 1.0 không nhằm cung cấp:

- Virtual DOM tổng quát.
- Client-side HTML template compilation.
- Global reactive store mới.
- `watch`/`effect` chạy ngầm.
- Raw HTML injection.
- Public bytecode/IR ABI.
- Toàn bộ platform capabilities trong core bundle.

---

# 2. Kiến trúc lớp và hướng phụ thuộc

```text
core/          Boot, app roots, scheduler, lifecycle, ownership, diagnostics
expression/    Lexer, parser, private AST, evaluator, cache, safety
 directive/    Core directives and event registry
component/     Component blueprints, instances, refs, local lifecycle
service/       Public API façades and orchestration
platform/      Browser/native adapters and side effects
drive/         Navigation, morphing, persisted identity
capability/    Optional only-used directives/services
```

Hướng phụ thuộc capability:

```text
component → service → platform
```

Quy tắc:

- `platform` MUST NOT phụ thuộc `component`.
- `service` MUST NOT tạo markup hoặc inject CSS.
- `component` quản lý behavior, local UI state và lifecycle; markup/presentation chủ yếu vẫn thuộc SSR HTML và CSS.
- `core` có thể điều phối các lớp nhưng không nhét policy capability vào kernel luôn được tải.

---

# 3. Application Root — `data-kit-app`

```html
<html data-kit-app="main">
```

Nhiều island độc lập MAY được khai báo:

```html
<main data-kit-app="dashboard">...</main>
<aside data-kit-app="supportWidget">...</aside>
```

Mỗi app root sở hữu:

- App/root lexical scope.
- Component alias registry.
- Render scheduler và dirty boundaries.
- Pending tasks và abort ownership.
- Error pipeline.
- Drive root và persisted-node registry.
- Lifecycle destroy độc lập.

Quy tắc:

1. Nếu document không có `data-kit-app`, `document.documentElement` trở thành app mặc định.
2. Nested app roots là lỗi `KIT_APP_NESTED`.
3. Alias, `$parent`, refs và bare state không xuyên app root.
4. `data-kit-app` MAY đi cùng `data-kit-scope`; scope đó seed app root scope.
5. `data-kit-app` MUST NOT đi cùng `data-kit-component` trên cùng element trong 1.0. App component phải nằm ở element con.
6. Di chuyển component trong cùng app không unmount. Di chuyển qua app root khác phải unmount khỏi app cũ và mount lại ở app mới.

---

# 4. Bảy Parser Modes chuẩn

Việc chấp nhận cả Class Map và Class Value Expression khiến `data-kit-class` cần grammar chuyên biệt. Runtime 1.0 có **7 parser modes**, không ép class vào một mode khác rồi fallback mơ hồ.

| # | Parser Mode | Directives | Grammar chuẩn |
| :-: | :--- | :--- | :--- |
| 1 | **Named Map** | `scope`, `style`, `bind` | `static-key: binding-expression;` |
| 2 | **Binding Expression** | `text`, `show`, `if`, `key` | Một read-only expression |
| 3 | **Class Value** | `class` | Class Map Shorthand **hoặc** Class Value Expression |
| 4 | **Action Program** | Event directives | `action-expression (; action-expression)*` |
| 5 | **Writable Path** | `model` | `identifier(.property | [binding-expression])*` |
| 6 | **Identity Literal** | `app`, `component`, `as`, `ref`, `persist` | Literal identifier/key |
| 7 | **Iterator** | `for` | `$item, $index of binding-expression` |

Capability directives MAY định nghĩa grammar riêng trong capability contract, nhưng MUST NOT âm thầm thay đổi grammar của core directives.

---

## 4.1 Named Map Grammar

Canonical form:

```html
<div data-kit-scope="
    open: false;
    count: 0;
    user: { name: 'Quoc', role: 'owner' };
"></div>
```

Grammar khái niệm:

```text
named-map       := map-entry (";" map-entry)* ";"?
map-entry       := static-key ":" binding-expression
```

Quy tắc parser:

1. Không dùng `{}` bao quanh toàn bộ map.
2. Object/array nằm trong value expression vẫn dùng `{}` và `[]` bình thường.
3. Chỉ tách `;` và `:` ở top level; không tách bên trong string, template, `()`, `[]`, `{}`.
4. Dấu `;` cuối MAY thiếu, nhưng formatter MUST sinh dấu `;`.
5. Duplicate key là lỗi; directive không được áp dụng một phần.
6. Key luôn tĩnh; không có computed map key.
7. Mỗi directive có validator key riêng.

### Key rules theo directive

| Directive | Key hợp lệ |
| :--- | :--- |
| `scope` | Bare identifier `[A-Za-z_][A-Za-z0-9_]*`; không bắt đầu bằng `$`; không phải `kit`. |
| `style` | CSS property, kebab-case hoặc custom property `--name`; quoted key MAY dùng khi cần. |
| `bind` | Attribute name tĩnh; dấu `-` không cần nháy; key chứa `:` phải bọc nháy. |

---

## 4.2 Binding Expression

Dùng cho:

```html
<span data-kit-text="user.name"></span>
<div data-kit-show="open"></div>
<section data-kit-if="ready && !error"></section>
<li data-kit-key="$item.id"></li>
```

Binding Expression:

- MUST là read-only.
- MUST NOT chứa assignment.
- MUST NOT resolve thành Promise.
- MAY gọi synchronous component method/getter hoặc granted service method, nhưng method dùng trong binding SHOULD không có side effect.
- Promise result tạo lỗi `KIT_ASYNC_BINDING`; DOM không được update từ Promise đó.

---

## 4.3 Class Value Grammar

`data-kit-class` chấp nhận hai authoring forms.

### A. Class Map Shorthand — canonical trong HTML

```html
<div data-kit-class="
    active: open;
    'md:grid-cols-6': desktop;
    'opacity-50 pointer-events-none': saving;
"></div>
```

Grammar:

```text
class-map       := class-entry (";" class-entry)* ";"?
class-entry     := static-class-key ":" binding-expression
```

Quy tắc:

- Simple class token MAY không cần nháy.
- Key chứa `:`, whitespace hoặc grammar-reserved characters MUST được quote.
- Quoted key MAY chứa nhiều class, tách bằng whitespace.
- Value là Binding Expression read-only.
- Duplicate exact class key là dev/compile error.
- Các nhóm khác nhau MAY chứa class token trùng nhau; runtime dùng set semantics.

### B. Class Value Expression — accepted advanced form

```html
<div data-kit-class="open ? 'active' : 'disabled'"></div>

<div data-kit-class="{
    active: open,
    'md:grid-cols-6': desktop
}"></div>

<div data-kit-class="[
    'card',
    sizeClass,
    { active: open }
]"></div>
```

Expression MAY trả về:

- `string`
- `array` lồng nhau
- plain object truth-map
- `null`, `undefined`, `false`

Không được trộn map shorthand và expression trong cùng attribute.

### Chọn form một cách tất định

Class parser xác định Class Map khi token đầu tạo thành một static class key và gặp top-level `:` trước bất kỳ top-level ternary `?`. Các trường hợp còn lại là Class Value Expression.

Ví dụ:

```text
active: open;                       → Class Map
'md:grid-cols-6': desktop;          → Class Map
open ? 'active' : 'disabled'        → Expression
{ active: open }                    → Expression
classes                             → Expression
```

Formatter, documentation, SSR generator và coding agents MUST ưu tiên Class Map Shorthand khi class được khai báo trực tiếp trong markup.

### DOM ownership

Runtime MUST:

1. Normalize cả hai forms thành một desired class set.
2. Diff desired set với dynamic class set của lần render trước.
3. Chỉ xóa class do directive này đã thêm.
4. Không xóa static classes trong `class="..."`.
5. Cảnh báo class được tạo bằng template/string concatenation mà CSS extractor không thể nhìn thấy trước.

---

## 4.4 Action Program

```html
<button data-kit-click="
    error = null;
    saving = true;
    $component.save();
">Save</button>
```

Action Program:

- Cho phép assignment vào writable state/path.
- Cho phép function/method call.
- Cho phép nhiều statement phân cách bằng `;`.
- Runtime theo dõi mọi top-level Promise được tạo ra trong program.
- Không hỗ trợ declaration hoặc control-flow JavaScript đầy đủ.

Bị cấm trong markup:

```text
var, let, const, function, class, for, while, do, switch,
new, delete, await, yield, try, catch, finally, import, export,
arrow functions, ++, --, compound assignment
```

Logic phức tạp MUST nằm trong component method hoặc service.

---

## 4.5 Writable Path

Dùng cho `data-kit-model`:

```html
<input data-kit-model="username">
<input data-kit-model="user.name">
<input data-kit-model="form.fields[fieldName]">
<input data-kit-model="$item.quantity">
```

Root không được là call, ternary hoặc binary expression.

Hợp lệ:

```text
username
user.name
items[$index].quantity
$item.quantity
$component.query
$paymentModal.open
```

Không hợp lệ:

```text
getUser().name
active ? a : b
username || fallback
$refs.input.value
kit.storage.value
```

Runtime MUST chặn write vào reserved runtime bindings, services, DOM handles và read-only getters.

---

## 4.6 Identity Literal

Identity values không được evaluate như expression:

```html
<html data-kit-app="main">
<div data-kit-component="dialog">
<div data-kit-component="dialog" data-kit-as="$paymentModal">
<input data-kit-ref="searchInput">
<div data-kit-persist="global-progress">
```

Quy tắc canonical:

```text
app/component/ref/persist: [A-Za-z][A-Za-z0-9_-]*
as alias:                  $[A-Za-z][A-Za-z0-9_]*
```

---

## 4.7 Iterator Grammar

```html
<li
    data-kit-for="$item, $index of items"
    data-kit-key="$item.id">
    <span data-kit-text="$item.name"></span>
</li>
```

Quy tắc:

- Dùng `of`, không dùng `in`.
- Item variable bắt buộc; index variable tùy chọn.
- Loop variable MUST bắt đầu bằng `$`.
- Loop variable MUST NOT trùng reserved context hoặc component alias trong app.
- Không có automatic property unwrapping; dùng `$item.name`, không dùng bare `name`.
- Collection 1.0 là `Array`; `null`/`undefined` được coi là empty list.

---

# 5. Expression Language 1.0

## 5.1 Features được hỗ trợ

| Nhóm | Hỗ trợ |
| :--- | :--- |
| Literals | `null`, boolean, number, string, array, plain object, template literal |
| Access | Dot access, computed access, optional chaining `?.` |
| Unary | `!`, unary `-` |
| Arithmetic | `+`, `-`, `*`, `/`, `%` |
| Comparison | `<`, `<=`, `>`, `>=`, `===`, `!==` |
| Logic | `&&`, `||`, `??` |
| Conditional | Ternary `condition ? a : b` |
| Calls | Component functions/methods, runtime DOM methods, granted service methods |
| Assignment | Chỉ Action Program và Writable Path targets |

Loose equality `==` và `!=` MUST NOT được hỗ trợ.

## 5.2 Template literals

```html
<span data-kit-text="`Xin chào, ${user.name}`"></span>

<button data-kit-bind="
    aria-label: `Mở hồ sơ của ${user.name}`;
"></button>
```

Quy tắc:

- Template literal là một expression feature chung, không phải parser riêng của `text`.
- Interpolation dùng Binding Expression.
- Assignment và Promise trong `${...}` bị cấm.
- `null`/`undefined` interpolate thành `""`.
- Object/array interpolation SHOULD tạo dev warning.
- `data-kit-text` luôn dùng `textContent`; template không render raw HTML.
- Tagged templates không được hỗ trợ.

## 5.3 Private AST, không public IR

```text
Source directive
      ↓
Lexer + parser theo mode
      ↓
Private cached AST
      ↓
Closed evaluator
```

Quy tắc:

- Runtime MUST NOT dùng `eval` hoặc `new Function`.
- AST format là private implementation detail.
- Không có `data-kitwork-*` trong runtime mới.
- Không public `kit.compile()`/`kit.run()` như compatibility contract.
- Go MAY parse/validate cùng source nhưng vẫn emit source `data-kit-*` xuống HTML.
- Cache key MUST bao gồm parser mode + raw source.

## 5.4 Safety

Evaluator MUST chặn mọi read/call/write qua:

```text
constructor
prototype
__proto__
__defineGetter__
__defineSetter__
__lookupGetter__
__lookupSetter__
ownerDocument
defaultView
contentWindow
window
globalThis
top
parent
self
```

Ngoài ra:

- Không fallback sang JavaScript globals.
- Có evaluation node budget.
- Có call-depth limit.
- Chỉ call service member đã explicit grant.
- User-generated HTML MUST được sanitize và không được phép kích hoạt `data-kit-*`.
- Zero-eval không biến arbitrary user HTML thành trusted template.

---

# 6. Runtime Contexts, alias và lexical resolution

## 6.1 Context matrix

| Context | Giá trị | Availability | Mutability |
| :--- | :--- | :--- | :--- |
| `$element` | Element sở hữu directive hiện tại | Mọi runtime directive | Binding read-only; DOM object có thể gọi method được phép |
| `$host` | Nearest component host | Trong component | Read-only handle |
| `$event` | Native DOM Event | Event Action Program | Read-only; ngoài event là `undefined` |
| `$refs` | Component-local ref registry | Trong component | Registry read-only; entries là elements |
| `$component` | Owning component instance | Trong component | Binding read-only; state fields có thể ghi |
| `$parent` | Parent component instance | Nested component | Không phải parent lexical scope |
| `$loopVar` | Dynamic loop binding | Trong row | Binding không reassign; nested writable property MAY được ghi |
| `$<alias>` | Named component instance | Cùng app root | Alias binding read-only; component state có thể ghi |
| `kit` | Curated service surface | Authored expressions | Namespace read-only; explicit grants only |

Tên duy nhất cho current element là `$element`. Không hỗ trợ `$el`, `$root`, `$components`, `$props`, `this` hoặc `ref` trong authored expressions.

## 6.2 Reserved names

Tối thiểu phải reserve:

```text
$element
$host
$event
$refs
$component
$parent
$error
kit
```

Direct alias và loop variable không được trùng reserved names.

## 6.3 Bare-name read order

Trong một component boundary:

```text
1. Runtime contexts, loop locals, direct aliases, kit
2. Nearest local lexical scope
3. Parent local scopes trong current component
4. Current component state/getters/methods
5. App root scope
6. undefined
```

Quy tắc:

- Local scope shadow component state.
- Component là lexical boundary.
- Component con không tự nhìn thấy bare state của component cha.
- Truy cập parent component bằng `$parent` hoặc direct alias.
- Missing identifier trả về `undefined`, không trả về `0`.

## 6.4 Write ownership

Bare identifier assignment:

```text
1. Nearest local scope đang sở hữu key
2. Current component đang sở hữu writable key
3. App root scope đang sở hữu key
4. Nếu chưa tồn tại:
   nearest local scope → current component → app root
```

Member assignment:

- Base object/path phải writable.
- Assignment vào `$component.open`, `$parent.open`, `$alias.open` MAY hợp lệ nếu target là writable component state.
- Assignment vào alias binding, refs registry, runtime handles hoặc `kit` bị cấm.
- Không được override component methods, getters không setter hoặc runtime metadata.
- Loop variable binding không được reassign; `$item.quantity` MAY ghi nếu object writable.

---

# 7. Scope, SSR data và component state

## 7.1 Scope trên element thường

```html
<div data-kit-scope="
    open: false;
    selectedId: null;
"></div>
```

Tạo mutable lexical local scope.

- Evaluate đúng một lần khi mount/hydrate.
- Map entries evaluate theo source order.
- Entry sau nhìn thấy entry trước và parent context.
- Client re-render không chạy lại initializer.
- Node mới từ SSR/Drive tạo scope mới.
- Reused keyed/persisted node giữ scope hiện tại.

## 7.2 Scope trên component host

```html
<div
    data-kit-component="dialog"
    data-kit-scope="
        open: true;
        order: { id: 128 };
        amount: order.amount;
    ">
</div>
```

Semantics:

1. Resolve parent lexical context.
2. Evaluate host scope map tuần tự trong seed context gồm parent context + các host entries đã evaluate.
3. Clone blueprint state cho instance.
4. Override blueprint state theo top-level host keys.
5. Không deep-merge object ngầm.
6. Không tạo host-scope store thứ hai.
7. Host keys không được override method, accessor hoặc runtime metadata.

## 7.3 Blueprint state contract

```js
kit.component("dialog", {
    open: false,
    saving: false,

    show() {
        this.open = true
    },

    close() {
        this.open = false
    }
})
```

Blueprint rules:

- Primitive, array và plain-object state được clone theo instance.
- Function methods và accessor descriptors được giữ.
- Date, Map, Set, class instance và opaque native object không phải state-default contract của 1.0.
- Runtime metadata được gắn non-enumerable/read-only:

```text
this.$host
this.$refs
this.$parent
this.$app
```

- Component method được gọi với `this` là component instance.
- Current event actor không được lưu trên instance; truyền `$element`/`$event` làm argument khi cần.

## 7.4 SSR serializer contract

Go Engine MUST có một canonical serializer cho scope values.

Allowed SSR-seeded values:

```text
null, boolean, finite number, string, array, plain object
```

Serializer MUST:

- Escape đúng expression-string context và HTML-attribute context.
- Không nối raw user string vào directive source.
- Từ chối function, channel, native handle hoặc unsupported values.
- Báo lỗi kèm component, attribute và key.
- Có configurable payload budget; development SHOULD cảnh báo payload quá lớn.
- Chỉ hydrate dữ liệu client behavior thực sự cần, không duplicate toàn bộ server model.

Client scope là snapshot cho interaction, không phải bằng chứng authorization.

---

# 8. Component instance, aliases, refs và lifecycle

## 8.1 Component registration

```js
kit.component(name, blueprint)
```

- `name` phải là identity literal hợp lệ.
- Mỗi host tạo instance riêng.
- Blueprint đăng ký trễ MAY hydrate các unresolved hosts, nhưng production SHOULD đăng ký trước boot.
- Missing blueprint tạo `KIT_COMPONENT_NOT_FOUND` và giữ SSR DOM nguyên vẹn.

## 8.2 Direct alias — `data-kit-as`

```html
<div
    data-kit-component="dialog"
    data-kit-as="$paymentModal">
</div>

<button data-kit-click="$paymentModal.open()">Open</button>
```

Rules:

- Chỉ hợp lệ trên component host.
- Alias phải match `/^\$[A-Za-z][A-Za-z0-9_]*$/`.
- Unique trong app root.
- Duplicate alias không được ghi đè owner cũ.
- Alias được register trước khi child components hydrate.
- Alias tự xóa khi component unmount.

## 8.3 Component-local refs

```html
<input data-kit-ref="searchInput">
<button data-kit-click="$refs.searchInput.focus()">Focus</button>
```

Rules:

- `data-kit-ref` chỉ hợp lệ trong component.
- Ref thuộc nearest component và không xuyên child component boundary.
- Ref name unique trong component tại một thời điểm.
- `$refs.name` luôn là một Element, không lúc Element lúc Array.
- Ref trong `if`/`for` được thêm/xóa khi subtree reconcile.
- Repeated list refs cùng tên tạo duplicate-ref error; collection refs deferred.

## 8.4 Mount pipeline

Cho mỗi component host:

1. Resolve app root, parent component và parent lexical context.
2. Evaluate host scope seed.
3. Tạo instance từ blueprint và seed state.
4. Attach runtime metadata.
5. Register direct alias.
6. Initialize local scopes và structural directives thuộc boundary.
7. Initial render của boundary, không render xuyên child component internals.
8. Reconcile refs của current component.
9. Hydrate child components recursively.
10. Call `mount()` sau khi refs và child components đã sẵn sàng.

`mount()` MAY trả về cleanup function hoặc Promise resolve thành cleanup function. Nếu Promise hoàn thành sau khi instance đã unmount, cleanup phải được gọi ngay.

State mutation trong `mount()` schedule một render mới; không chạy re-entrant render trực tiếp.

## 8.5 Unmount pipeline

1. Mark instance `unmounting`.
2. Unmount child components theo reverse ownership order.
3. Abort tasks, subscriptions, timers và direct listeners của owner.
4. Call `unmount()` trong khi `$host`, `$refs`, alias và state còn truy cập được.
5. Call cleanup trả về từ `mount()`.
6. Remove refs và alias.
7. Release state, metadata và registries.
8. Remove DOM khi owner thực hiện removal.

`unmount()` SHOULD synchronous; runtime không trì hoãn DOM removal để chờ Promise từ `unmount()`.

## 8.6 DOM move

- Move trong cùng app root không unmount.
- Mutation cleanup được defer đến microtask và chỉ chạy nếu node không còn connected hoặc đổi app ownership.
- Move sang app root khác = unmount + mount mới.

---

# 9. Master Directive Matrix

| Directive | Mode | Phase | Semantics | Status |
| :--- | :--- | :--- | :--- | :--- |
| `data-kit-app="main"` | Identity | Boot | Tạo app/island root | Core |
| `data-kit-component="dialog"` | Identity | Mount | Tạo component instance | Core |
| `data-kit-as="$paymentModal"` | Identity | Mount | Direct named instance alias | Core |
| `data-kit-scope="open: false;"` | Named Map | Mount | Component seed hoặc local scope; init once | Core |
| `data-kit-ref="input"` | Identity | Reconcile | Component-local DOM ref | Core |
| `data-kit-text="expr"` | Binding | Render | `textContent` binding | Core |
| `data-kit-show="open"` | Binding | Render | Toggle `hidden`, giữ subtree mounted | Core |
| `data-kit-if="open"` | Binding | Structure | Mount/unmount subtree | Core |
| `data-kit-for="$item of items"` | Iterator | Structure | Keyed list materialization | Core |
| `data-kit-key="$item.id"` | Binding | Structure | Stable row identity | Core |
| `data-kit-model="user.name"` | Writable Path | Input | Two-way form binding | Core |
| `data-kit-class="active: open;"` | Class Value | Render | Class map shorthand hoặc class expression | Core |
| `data-kit-style="width: width;"` | Named Map | Render | Inline CSS property binding | Core |
| `data-kit-bind="aria-expanded: open;"` | Named Map | Render | Safe unified DOM attribute binding | Core |
| `data-kit-persist="global-progress"` | Identity | Morph | Keyed node reuse giữa Drive trees | Drive |
| `data-kit-<event>:<modifier>="..."` | Action | Event | Event Action Program | Core |
| `data-kit-teleport="body"` | Capability | Reconcile | Logical-owner-preserving portal | Deferred capability |
| `data-kit-transition="fade"` | Capability | Structure | Exit-aware unmount | Deferred capability |

---

# 10. Binding directives

## 10.1 `data-kit-text`

```html
<span data-kit-text="`Xin chào, ${user.name}`">Xin chào, Quoc</span>
```

Rules:

- Writes through `textContent` only.
- `null`/`undefined` → `""`.
- String/number/boolean → `String(value)`.
- Array/object → `KIT_TEXT_NON_SCALAR`; binding không mutate DOM.
- SSR text SHOULD tồn tại trong element để no-JS vẫn có nội dung.

## 10.2 `data-kit-show`

```html
<div data-kit-show="open"></div>
```

- Owns `hidden` property.
- False → `hidden = true`.
- Subtree remains mounted; refs, state và tasks vẫn tồn tại.
- Element không được đồng thời bind `hidden` qua `data-kit-bind`.

## 10.3 `data-kit-if`

```html
<section data-kit-if="isEditing">...</section>
```

- First hydration giữ SSR node nếu condition true.
- Runtime đặt anchor và capture template cho remount sau.
- False unmount/cleanup subtree thật sự.
- Clone materialized MUST không còn structural source directive gây recursive capture.
- Transition capability MAY trì hoãn final removal.

## 10.4 `data-kit-for` và `data-kit-key`

```html
<li
    data-kit-for="$item, $index of items"
    data-kit-key="$item.id">
    <span data-kit-text="$item.name"></span>
</li>
```

Rules:

- Stable key SHOULD được khai báo.
- Missing key dùng index fallback và dev warning.
- Key phải là string/number.
- Duplicate key tạo error và abort update của list đó; không render partial corrupted order.
- Surviving keyed row được move, không rebuild.
- Row local bindings được rebind sang current item/index.
- Component instances trong surviving row được giữ.
- Removed rows chạy full cleanup.

## 10.5 `data-kit-class`

Canonical:

```html
<div data-kit-class="
    active: open;
    'md:grid-cols-6': desktop;
    'opacity-50 pointer-events-none': saving;
"></div>
```

Accepted:

```html
<div data-kit-class="classes"></div>
<div data-kit-class="open ? 'active' : 'disabled'"></div>
<div data-kit-class="{ active: open }"></div>
<div data-kit-class="['card', { active: open }]"></div>
```

- Map key/value rules theo Section 4.3.
- Runtime owns only classes it added.
- Dynamic template-generated Tailwind class SHOULD produce `KIT_DYNAMIC_CLASS_TOKEN` warning.

## 10.6 `data-kit-style`

```html
<div data-kit-style="
    width: width;
    opacity: opacity;
    --progress: progress;
"></div>
```

Rules:

- Uses `style.setProperty(key, String(value))`.
- `null`/`undefined`/`false` remove property.
- Không tự thêm `px`.
- Static style properties không nằm trong map được giữ.
- Bound property được directive sở hữu.
- Unsafe CSS forms như legacy `expression()` và `url(javascript:...)` bị chặn.

## 10.7 `data-kit-bind`

```html
<button data-kit-bind="
    aria-expanded: open;
    aria-controls: 'main-menu';
    aria-label: `Mở menu của ${user.name}`;
    data-state: open ? 'open' : 'closed';
    disabled: loading;
    title: title;
"></button>
```

Bare key có `-` không cần nháy:

```text
aria-expanded
data-state
hx-get
tabindex
viewBox
```

Key chứa `:` phải quote:

```html
<use data-kit-bind="'xlink:href': `#${iconId}`;"></use>
```

### Boolean serialization

| Attribute category | `null` / `undefined` | `false` | `true` | String / Number |
| :--- | :--- | :--- | :--- | :--- |
| `data-*` | Remove | `"false"` | `"true"` | `String(value)` |
| `aria-*` | Remove | `"false"` | `"true"` | `String(value)` |
| HTML boolean | Remove | Remove | Empty attribute | `String(value)` |
| Ordinary | Remove | Remove | `"true"` | `String(value)` |

Runtime MUST maintain a canonical HTML boolean-attribute set.

### Bị cấm

`data-kit-bind` MUST NOT manage:

```text
class
style
on*
srcdoc
data-kit-*
data-kitwork-*
```

Trên form controls, `value`, `checked`, `selected`, `selectedIndex` và `files` thuộc `data-kit-model`; binding conflict tạo `KIT_OWNERSHIP_CONFLICT`.

URL-bearing attributes phải chặn tối thiểu:

```text
javascript:
vbscript:
```

`data:` URL policy MAY được cấu hình theo attribute/media type.

---

# 11. Form Model Contract

## 11.1 Initial precedence

```text
Nếu writable path đã tồn tại → state thắng và sync ra DOM.
Nếu path chưa tồn tại        → DOM current value seed path đúng một lần.
```

Drive reuse giữ model state hiện tại. Node mới từ SSR có thể seed state mới nếu path chưa tồn tại.

## 11.2 Coercion matrix

| Control | State value |
| :--- | :--- |
| `input[type=text]`, search, email, url, tel, password, textarea | String |
| number, range | Number; empty/invalid → `null` |
| checkbox đơn | Boolean |
| checkbox group cùng exact path và value | Array các checked values |
| radio group | Selected value hoặc `null` |
| select đơn | String |
| select multiple | Array |
| date/time/month/week/datetime-local | Canonical DOM string |
| file | `FileList`; read from DOM only, runtime không ghi ngược |

Group detection phải nằm trong cùng form owner và app root.

## 11.3 Input events

- Text-like controls: `input`.
- Checkbox/radio/select/file: `change`.
- Runtime MUST handle IME composition: không commit intermediate composed text; sync tại `compositionend`.
- Model update invalidates nearest render boundary.

## 11.4 Progressive enhancement

- `name`, `value`, `action`, `method` và native constraint validation vẫn phải tồn tại trong SSR HTML.
- Form phải submit bình thường khi JS không chạy.
- Runtime enhancement MAY intercept bằng `data-kit-submit:prevent`, nhưng server vẫn xử lý cùng endpoint và validate lại.

---

# 12. Event System & Modifiers

## 12.1 Syntax

```html
<button data-kit-click:prevent:once="$component.save()"></button>
<input data-kit-keydown:enter="$component.search($event.target.value)">
<div data-kit-click:outside="open = false"></div>
<div data-kit-keydown:escape:document="open = false"></div>
```

Modifier order trong markup không thay đổi semantics; parser normalize về canonical plan.

## 12.2 Core modifiers

| Modifier | Semantics |
| :--- | :--- |
| `:window` | Listen trên `window` |
| `:document` | Listen trên `document` |
| `:outside` | Fire khi event target nằm ngoài `$element`; ngầm dùng document observation |
| `:enter` | Keyboard filter `Enter` |
| `:escape` | Keyboard filter `Escape` |
| `:prevent` | `preventDefault()` đồng bộ |
| `:stop` | `stopPropagation()` đồng bộ |
| `:once` | Consume binding trước timing/action |
| `:debounce(ms)` | Chạy sau khoảng yên lặng |
| `:throttle(ms)` | Giới hạn tần suất |

Validation:

- Tối đa một target modifier.
- Tối đa một timing modifier.
- `outside` không kết hợp `window`/`document`.
- `enter`/`escape` chỉ dùng với keyboard events.
- `outside` chỉ dùng với click/pointer-compatible events.
- Invalid combination tạo `KIT_INVALID_MODIFIER`.

## 12.3 Execution order

```text
1. Resolve event source/target
2. Apply filters
3. Run prevent/stop synchronously
4. Check and mark once-consumed
5. Apply debounce/throttle
6. Execute Action Program
7. Observe every top-level Promise
8. Route errors
9. Invalidate nearest render boundary
```

`prevent`/`stop` phải chạy trước debounce. Khi debounce/throttle được dùng, `$event.currentTarget` không được coi là ổn định; dùng `$element`.

## 12.4 Outside freshness

Một subtree vừa được mount bởi chính click hiện tại MUST NOT bị cùng click đó đóng qua `:outside`. Runtime giữ fresh-mount markers đến microtask sau event dispatch.

## 12.5 Event registry

Core runtime SHOULD có registry cho common bubbling events và direct/global events. Unknown event tạo diagnostic thay vì âm thầm bỏ qua. Capability MAY đăng ký thêm event types.

---

# 13. Scheduler, render boundaries và DOM reconciliation

## 13.1 Invalidation target

```text
Nearest component host
→ nearest local scope root
→ app root
```

Scheduler:

- Dùng `Set` cho dirty boundaries.
- Batch ít nhất một lần mỗi JavaScript turn.
- Nếu parent và child cùng dirty, chỉ render parent.
- Không full-document scan sau mọi action.

## 13.2 Boundary render phases

```text
1. Evaluate structural directives: for, if
2. Cleanup removed subtrees
3. Hydrate newly inserted scopes/components
4. Reconcile refs
5. Sync model DOM properties
6. Apply text/show/class/style/bind
7. Queue mount hooks for new component instances
```

Parent render MUST NOT evaluate bindings bên trong child component internals; child component là ownership boundary riêng.

## 13.3 Directive ownership

Mỗi directive chỉ mutate phần DOM nó sở hữu:

- `text` → `textContent`
- `show` → `hidden`
- `class` → dynamic class set
- `style` → declared CSS properties
- `bind` → declared attributes
- `model` → live form properties
- `if`/`for` → structural region

Hai directives cùng sở hữu một DOM target trên cùng element tạo `KIT_OWNERSHIP_CONFLICT`.

## 13.4 MutationObserver

- Chỉ hydrate external/Drive-added subtrees.
- Removed nodes được cleanup deferred; DOM move trong cùng app không cleanup.
- Internal runtime mutations SHOULD được grouped để tránh redundant observer work.

---

# 14. Async Actions, `kit.task` và error pipeline

## 14.1 Promise observer

Event Action Program MAY trả nhiều Promise.

Runtime MUST:

- Track pending count theo event binding/element owner.
- Set `data-busy="true"` và `aria-busy="true"` khi pending > 0.
- Chỉ remove busy state khi pending về 0.
- Invalidate boundary sau settlement.
- Route rejection; không nuốt lỗi.
- Coi Promise trong render binding là `KIT_ASYNC_BINDING`.

Automatic busy attributes là runtime-owned. Element không được đồng thời bind `data-busy` hoặc `aria-busy` bằng `data-kit-bind`.

## 14.2 Task ownership API

```js
kit.task.run(owner, task)
kit.task.latest(owner, key, task)
kit.task.abort(owner, key)
kit.task.delay(ms, options)
```

Contract:

- `owner` là component instance hoặc app handle.
- Task callback nhận `{ signal }` khi có thể abort.
- `latest` abort previous same-key task và bảo đảm stale result không commit qua helper context.
- Unmount/destroy abort toàn bộ tasks của owner.
- `AbortError` do intentional cancellation không mặc định báo global error.

## 14.3 Error pipeline

Error context tối thiểu:

```js
{
    code,
    message,
    phase,
    directive,
    source,
    element,
    component,
    app,
    cause
}
```

Bubbling order:

```text
Nearest component.error(error, context)
→ parent component error hooks
→ kit.onError(error, context)
→ document CustomEvent "kitwork:error"
→ development console
```

Error hook trả `true` để đánh dấu handled. Không có `data-kit-error` trong 1.0.

---

# 15. `kit.request` — Server Interaction Contract

```js
kit.request.get(url, options)
kit.request.post(url, body, options)
kit.request.submit(formElement, options)
kit.request.abort(key)
```

`kit.request` SHOULD quản lý:

- CSRF headers/tokens.
- JSON, URL-encoded, `FormData`, multipart.
- `AbortSignal` và timeout.
- Normalized errors.
- Deduplication khi được yêu cầu.
- Latest-wins integration với `kit.task`.
- Redirect handling.
- Drive HTML response handoff.
- Server validation errors.

Mọi request vẫn phải được server authorize/validate.

---

# 16. Drive, Morph và keyed persistence

```html
<div data-kit-persist="global-progress"></div>
```

Semantics:

```text
Old tree có key + incoming tree có cùng key
→ reuse old node/instance/state

Incoming tree không có key
→ old node unmount/remove bình thường
```

Rules:

- Key unique trong Drive root.
- Persist không có nghĩa node sống vĩnh viễn.
- Reused node giữ component instance, scope, refs identity và owned tasks.
- Incoming `data-kit-scope` không tự merge vào reused state.
- Server muốn reset phải đổi key, thay node hoặc gọi reconciliation API tường minh.
- Drive SHOULD preserve focus, selection, active input draft và scroll where applicable.
- Removed component abort tasks và cleanup lifecycle.

Teleport và transition là capabilities, không ở core 1.0:

```html
<div data-kit-teleport="body"></div>
<div data-kit-transition="fade"></div>
```

Teleport phải giữ logical owner qua `WeakMap<Node, LogicalOwner>`; không dựa vào DOM ancestry sau khi move.

---

# 17. Services, platform adapters và capability loading

## 17.1 Service registration và expression grants

```js
kit.service("storage", storageService, {
    expression: ["get", "set", "remove", "clear"]
})
```

- Trusted JavaScript nhận concrete service object qua `kit.storage`.
- Authored expressions chỉ nhận read-only proxy chứa granted members.
- Service methods có I/O, permission hoặc native bridge MUST trả Promise.
- Getters/subscriptions MAY sync.
- Subscription MUST trả unsubscribe function.
- Async APIs SHOULD nhận `{ signal, timeout }` khi phù hợp.

## 17.2 Service tiers

### Runtime/server interaction — ưu tiên 1.0

```text
kit.task
kit.request
kit.storage
kit.navigation
kit.network
kit.display
kit.clipboard
kit.share
kit.permissions
```

### Browser/native capabilities — only-used modules

```text
kit.device
kit.location
kit.camera
kit.media
kit.recorder
kit.audio
kit.sensors
kit.files
kit.notification
kit.window
```

Tên namespace là định hướng/reserved. Mỗi service cần contract riêng trước khi method list trở thành stable public ABI.

## 17.3 Capability manifest

```js
kit.capability({
    name: "camera",
    version: "1.0.0",
    directives: [],
    services: ["camera", "permissions"],
    dependencies: ["permissions"],
    install(runtime) {},
    destroy() {}
})
```

Go Engine MAY scan markup/component source và chỉ ship capability được sử dụng.

---

# 18. Security contract

1. Author directives là trusted application template source.
2. User-generated HTML phải sanitize và loại bỏ/neutralize `data-kit-*`.
3. Không eval/new Function/global fallback.
4. Block dangerous member paths ở mọi read/call/write.
5. `data-kit-bind` chặn event-handler attributes và unsafe URLs.
6. `data-kit-style` chặn unsafe CSS execution forms.
7. Services dùng explicit grants.
8. SSR serializer context-aware; không raw interpolation.
9. Evaluation budget và call depth bắt buộc.
10. Client state không được dùng làm authorization truth.

---

# 19. Diagnostics chuẩn

Tối thiểu:

```text
KIT_APP_NESTED
KIT_PARSE_INVALID_TOKEN
KIT_PARSE_UNTERMINATED_STRING
KIT_PARSE_INVALID_MAP
KIT_UNKNOWN_DIRECTIVE
KIT_INVALID_MODIFIER
KIT_ASSIGNMENT_IN_BINDING
KIT_ASYNC_BINDING
KIT_EVALUATION_BUDGET
KIT_CALL_DEPTH
KIT_UNSAFE_MEMBER
KIT_SCOPE_KEY_RESERVED
KIT_SCOPE_METHOD_COLLISION
KIT_COMPONENT_NOT_FOUND
KIT_DUPLICATE_ALIAS
KIT_DUPLICATE_REF
KIT_DUPLICATE_KEY
KIT_MODEL_NOT_WRITABLE
KIT_TEXT_NON_SCALAR
KIT_BIND_UNSAFE_ATTRIBUTE
KIT_OWNERSHIP_CONFLICT
KIT_DYNAMIC_CLASS_TOKEN
KIT_SSR_SERIALIZE
KIT_CAPABILITY_MISSING
```

Development diagnostics MUST include element/directive/source when có thể. Production MAY dùng message ngắn hơn nhưng code phải ổn định.

---

# 20. Source tree chuẩn

```text
engine/jit/javascript/
├── README.md
├── MIGRATION.md
│
├── core/
│   ├── boot.js
│   ├── app.js
│   ├── kernel.js
│   ├── registry.js
│   ├── scheduler.js
│   ├── lifecycle.js
│   ├── ownership.js
│   └── diagnostics.js
│
├── expression/
│   ├── lexer.js
│   ├── parser.js
│   ├── parser-map.js
│   ├── parser-class.js
│   ├── parser-action.js
│   ├── parser-model.js
│   ├── evaluator.js
│   ├── safety.js
│   └── cache.js
│
├── directive/
│   ├── app.js
│   ├── component.js
│   ├── scope.js
│   ├── ref.js
│   ├── text.js
│   ├── show.js
│   ├── if.js
│   ├── for.js
│   ├── model.js
│   ├── class.js
│   ├── style.js
│   ├── bind.js
│   ├── event.js
│   └── persist.js
│
├── component/
│   ├── registry.js
│   ├── instance.js
│   └── lifecycle.js
│
├── service/
│   ├── task.js
│   ├── request.js
│   ├── storage.js
│   └── ...
│
├── platform/
│   ├── browser/
│   └── native/
│
├── drive/
│   ├── navigation.js
│   ├── morph.js
│   ├── focus.js
│   └── persist.js
│
├── capability/
│   ├── teleport.js
│   ├── transition.js
│   └── ...
│
└── test/
    ├── conformance/
    ├── browser/
    ├── fuzz/
    └── benchmark/
```

Trong giai đoạn đầu MAY giữ một reference kernel file để review semantics, nhưng production implementation SHOULD tách theo ownership trên sau khi fixtures ổn định.

---

# 21. Shared Conformance Suite

```text
test/conformance/
├── lexer.json
├── expression.json
├── template.json
├── map.json
├── class.json
├── scope.json
├── component.json
├── alias.json
├── refs.json
├── text.json
├── bind.json
├── model.json
├── events.json
├── list.json
├── lifecycle.json
├── async.json
├── drive.json
└── security.json
```

Mỗi fixture SHOULD chứa:

```json
{
  "name": "nested write updates owning scope",
  "mode": "action",
  "source": "count = count + 1",
  "context": {},
  "expect": {},
  "error": null
}
```

Conformance matrix phải tách:

| Area | Specified | JS Runtime | Go Validator | Browser DOM | Drive |
| :--- | :-: | :-: | :-: | :-: | :-: |
| Expressions | ☐ | ☐ | ☐ | n/a | n/a |
| Scope ownership | ☐ | ☐ | ☐ | ☐ | ☐ |
| Components/lifecycle | ☐ | ☐ | partial | ☐ | ☐ |
| Bind/model/events | ☐ | ☐ | ☐ | ☐ | n/a |
| Morph/persist | ☐ | ☐ | n/a | ☐ | ☐ |
| Security | ☐ | ☐ | ☐ | ☐ | n/a |

Không dùng một con số tổng như `53/53` để tuyên bố frozen nếu coverage chưa bao phủ toàn bộ contract.

---

# 22. Migration từ runtime cũ

| Cũ | Mới |
| :--- | :--- |
| `$el` | `$element` |
| `$root` | `$host` với semantics component-host-only |
| `data-kit-alias` | `data-kit-as` |
| `data-kit-component="dialog=$modal"` | `data-kit-component="dialog" data-kit-as="$modal"` |
| `data-kit-data`, `data-kit-aria`, `data-kit-attribute` | `data-kit-bind` |
| `data-kit-away="close()"` | `data-kit-click:outside="close()"` |
| `data-kit-escape="close()"` | `data-kit-keydown:escape:document="close()"` |
| `data-kit-guard="prevent stop"` | `:prevent:stop` |
| `data-kit-debounce="300"` | `:debounce(300)` |
| `data-kit-action` | Event directive trực tiếp |
| `data-kit-hidden` | `data-kit-show="!hidden"` |
| `data-kitwork-*` | Removed |
| Public compile/run IR | Removed from stable API |
| Multiple scope shapes | Chỉ Named Map |
| Generic attribute mirroring | Removed; dùng `data-kit-bind` |

Runtime mới không cần compatibility layer bên trong core. Migration codemod MAY tồn tại như tooling riêng.

---

# 23. Canonical implementation example

## 23.1 Component JavaScript

```js
kit.component("payment-dialog", {
    open: false,
    saving: false,
    error: null,
    amount: null,

    show() {
        this.open = true
    },

    close() {
        this.open = false
    },

    async pay() {
        if (this.saving) return

        this.saving = true
        this.error = null

        try {
            const result = await kit.task.latest(this, "pay", ({ signal }) =>
                kit.request.submit(this.$refs.form, { signal })
            )

            if (result && result.redirect) {
                return kit.navigation.visit(result.redirect)
            }

            this.open = false
        } catch (error) {
            if (error && error.name !== "AbortError") {
                this.error = error.message || String(error)
                throw error
            }
        } finally {
            this.saving = false
        }
    },

    mount() {
        return () => {
            // Optional component-owned cleanup.
        }
    },

    error(error) {
        this.error = error.message || String(error)
        return true
    }
})
```

## 23.2 SSR HTML

```html
<html data-kit-app="main">
<body>
    <div
        data-kit-component="payment-dialog"
        data-kit-as="$paymentModal"
        data-kit-scope="
            order: { id: 128, amount: 49.90, currency: 'USD' };
            canPay: true;
            open: false;
            saving: false;
            error: null;
            amount: order.amount;
        ">

        <section
            data-kit-if="open"
            data-kit-click:outside="open = false"
            data-kit-keydown:escape:document="open = false"
            data-kit-class="
                'is-open opacity-100': open;
                'opacity-50 pointer-events-none': saving;
            "
            data-kit-bind="
                role: 'dialog';
                aria-modal: true;
                aria-busy: saving;
                aria-label: `Payment for order #${order.id}`;
                data-state: saving ? 'saving' : 'ready';
            ">

            <h2 data-kit-text="`Order #${order.id}`">
                Order #128
            </h2>

            <form
                action="/orders/128/pay"
                method="post"
                data-kit-ref="form"
                data-kit-submit:prevent="$component.pay()">

                <label>
                    Amount
                    <input
                        name="amount"
                        type="number"
                        step="0.01"
                        value="49.90"
                        data-kit-model="amount">
                </label>

                <p data-kit-show="error" data-kit-text="error"></p>

                <button
                    type="submit"
                    data-kit-bind="
                        disabled: saving || !canPay;
                        aria-label: `Pay ${amount} ${order.currency}`;
                    ">
                    Pay
                    <span data-kit-text="`${amount} ${order.currency}`">
                        49.90 USD
                    </span>
                </button>
            </form>
        </section>
    </div>

    <button type="button" data-kit-click="$paymentModal.show()">
        Open payment dialog
    </button>
</body>
</html>
```

Không có JavaScript, form vẫn submit tới `/orders/128/pay`. Có runtime, submit được progressive-enhance qua component method.

---

# 24. Thứ tự phát triển đề xuất

## Milestone 1 — Language & Validator

- [ ] Lexer hỗ trợ strings, templates, optional access, strict operators.
- [ ] 7 parser modes.
- [ ] Private AST evaluator và safety guards.
- [ ] Go validator chạy chung expression/map/class fixtures.
- [ ] Formatter canonical hóa map/class/event modifiers.

## Milestone 2 — App, Scope & Components

- [ ] App roots và isolated registries.
- [ ] Lexical read/write ownership.
- [ ] Blueprint clone/seed contract.
- [ ] Direct aliases.
- [ ] Component-local refs.
- [ ] Mount/unmount/move lifecycle.

## Milestone 3 — Core Directives & Scheduler

- [ ] Text/show/class/style/bind.
- [ ] If/for/key structural ownership.
- [ ] Model and IME behavior.
- [ ] Event modifiers and delegated registry.
- [ ] Dirty-boundary scheduler.
- [ ] Diagnostics.

## Milestone 4 — Async & Server Interaction

- [ ] Promise observer.
- [ ] `kit.task` ownership/latest/abort.
- [ ] Error pipeline.
- [ ] `kit.request`.
- [ ] Progressive form enhancement.

## Milestone 5 — Drive & Persistence

- [ ] Morph lifecycle.
- [ ] `data-kit-persist` keyed reuse.
- [ ] Focus, selection, input draft and scroll preservation.
- [ ] Component/task preservation and cleanup.

## Milestone 6 — Capabilities & Tooling

- [ ] Capability manifest and only-used loading.
- [ ] Teleport.
- [ ] Transition.
- [ ] Linter, formatter, inspector and browser DevTools.
- [ ] Performance regression benchmarks.

---

# 25. Deferred / Not-To-Add for 1.0

| Không thêm | Lý do |
| :--- | :--- |
| `data-kit-props` / `$props` | Scope đã seed SSR và client state; chỉ thêm props khi có read-only reactive parent-to-child contract thật sự. |
| `$el`, `$root`, `$components` aliases | Mỗi concept chỉ có một canonical name. |
| `data-kit-watch` / `data-kit-effect` | Tránh hidden side effects và render loops. |
| `data-kit-html` | Đi ngược HTML First và mở bề mặt XSS. |
| `data-kit-property` | Chưa cần khi `bind` + `model` đã phân vai. |
| `data-kit-hidden` | Trùng nghĩa ngược với `show`. |
| `data-kit-action` | Trùng event directive system. |
| Companion modifier attributes | Modifiers nằm trong event attribute name. |
| Public AST / serialized IR / `data-kitwork-*` | AST là implementation detail, không phải ABI. |
| Automatic loop property unwrapping | Bare names luôn là lexical/component state. |
| Automatic ref arrays | Giữ `$refs.name` có một type ổn định. |
| Auto `px` | CSS units phải tường minh. |
| Transition engine trong core | Transition là optional capability. |

---

# 26. Freeze Gate cho Kitwork Client Runtime 1.0

Specification chỉ được đổi từ **Implementation Baseline** sang **Frozen Executable Specification** khi:

1. Go validator và JavaScript parser pass cùng grammar/expression fixtures.
2. Browser tests bao phủ toàn bộ core directive matrix.
3. Lifecycle/move/unmount tests không leak refs, aliases, tasks hoặc listeners.
4. Security fuzz tests vượt qua blocked member/global escape cases.
5. Model tests bao phủ IME và các form control types.
6. Drive tests bao phủ persisted identity, focus/input preservation và cleanup.
7. Diagnostics codes ổn định.
8. Canonical formatter sinh một syntax duy nhất cho docs và agents.
9. Benchmark baseline được ghi lại trước tối ưu hóa production.

---

> **Implementation rule:** Khi code và tài liệu mâu thuẫn, specification này là nguồn chuẩn cho tới khi một revision mới được phê duyệt và conformance fixtures được cập nhật cùng lúc.

*Status: Architecture Draft 0.7 — Kitwork Client Runtime Implementation Baseline.*
