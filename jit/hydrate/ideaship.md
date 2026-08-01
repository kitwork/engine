# KITJS V2

## Quy chuẩn kiến trúc và tài liệu thiết kế runtime tương tác HTML

> **Tác giả và kiến trúc sư:** Huỳnh Nhân Quốc  
> **Dự án:** `@kitwork/kitjs`  
> **Hệ sinh thái:** Kitwork Engine

---

## 1. Tổng quan

KitJS V2 là một runtime tương tác HTML nhỏ gọn, an toàn và có thể chạy nhất quán trong hệ sinh thái Kitwork.

Mục tiêu của KitJS không phải biến HTML thành JavaScript, cũng không phải xây dựng thêm một framework frontend đầy đủ. KitJS giữ HTML ở vị trí trung tâm và chỉ bổ sung những khả năng cần thiết:

* trạng thái cục bộ;
* component;
* binding dữ liệu;
* sự kiện;
* danh sách;
* async action;
* browser và native capability;
* hydration từ server;
* đồng bộ semantics giữa Go Engine và client runtime.

Triết lý cốt lõi của KitJS là:

> HTML vẫn là cấu trúc chính. KitJS chỉ bổ sung trạng thái và hành vi vào đúng nơi chúng được sử dụng.

---

# 2. Các nguyên tắc kiến trúc

## 2.1 HTML-first Reactive Islands

KitJS không coi mọi phần tử là component.

Thay vào đó, giao diện được chia thành các vùng tương tác độc lập, gọi là reactive islands.

Một reactive island có thể được tạo bằng scope:

```html
<div data-kit-scope="{ count: 0 }">
  ...
</div>
```

Hoặc bằng component:

```html
<div
  data-kit-component="counter"
  data-kit-scope="{ count: 0 }"
>
  ...
</div>
```

Khác biệt giữa hai loại:

* `data-kit-scope` tạo một vùng trạng thái cục bộ;
* `data-kit-component` gắn hành vi có tên và khả năng tái sử dụng;
* component có thể sở hữu scope;
* scope không bắt buộc phải là component.

Component vì vậy không phải nền móng duy nhất của KitJS. Nền móng thực sự là lexical scope gắn với HTML.

---

## 2.2 Một grammar biểu thức

KitJS chỉ nên có một grammar biểu thức cốt lõi.

Grammar đó được dùng cho:

* event handler;
* điều kiện hiển thị;
* binding;
* scope;
* danh sách;
* giá trị tính toán;
* gọi method;
* truy cập state.

Ví dụ:

```html
data-kit-click="count = count + 1"
data-kit-show="isOpen && !loading"
data-kit-text="qty * price"
data-kit-scope="{ qty: 1, price: 250 }"
```

KitJS có thể cung cấp nhiều cú pháp viết tiện lợi, nhưng tất cả phải được normalize về cùng một AST và cùng semantics runtime.

Nguyên tắc:

> Nhiều surface syntax, một AST, một runtime behavior.

---

## 2.3 Zero Eval

KitJS không sử dụng:

```js
eval()
new Function()
```

Mọi biểu thức được xử lý qua lexer, parser, compiler hoặc interpreter do KitJS kiểm soát.

Điều này giúp:

* tương thích với strict Content Security Policy;
* không cần `unsafe-eval`;
* kiểm soát được grammar;
* hỗ trợ static analysis;
* đồng bộ semantics với Go Engine;
* hạn chế khả năng thực thi JavaScript tùy ý.

Không nên tuyên bố KitJS “tuân thủ 100% CSP”, vì CSP còn phụ thuộc vào cấu hình của ứng dụng.

Tuyên bố chính xác là:

> KitJS không yêu cầu `unsafe-eval` và có thể hoạt động trong môi trường strict CSP.

---

## 2.4 Server và client có trách nhiệm riêng

Kitwork sử dụng hai lớp biểu thức rõ ràng.

### Server-owned rendering

```html
{{ expression }}
```

Được xử lý bởi Go Engine trước khi HTML được gửi đến trình duyệt.

Ví dụ:

```html
<title>{{ page.title }}</title>
```

### Client-owned reactivity

```html
data-kit-text="expression"
data-kit-show="expression"
data-kit-click="expression"
```

Được xử lý bởi KitJS trong trình duyệt.

Ví dụ:

```html
<strong data-kit-text="qty * price"></strong>
```

KitJS V2 không sử dụng `${expression}` trong text node.

Lý do:

* xung đột thị giác với JavaScript template literal;
* xung đột với ký hiệu tiền tệ;
* buộc runtime phải quét toàn bộ text node;
* tạo hai cách cập nhật text cùng lúc;
* làm ranh giới server và client kém rõ ràng;
* khó verify và pre-render hơn attribute directive.

Quy chuẩn chính thức:

```text
{{ expression }}       Server rendering
data-kit-*="expression" Client reactivity
```

---

## 2.5 Không nhân đôi semantics

KitJS có thể hỗ trợ hai cách viết scope, alias và ref, nhưng chúng không được trở thành những hệ thống runtime riêng biệt.

Ví dụ:

```html
data-kit-scope="{ qty: 1, price: 250 }"
```

và:

```html
data-kit-scope="qty: 1; price: 250"
```

đều phải tạo cùng một `ObjectExpression`.

Tương tự:

```html
data-kit-alias="$paymentModal"
```

và:

```html
data-kit-ref="paymentModal"
```

có thể cùng tồn tại, nhưng phải tham chiếu cùng một component hoặc scope instance.

---

# 3. Kích thước runtime

Không sử dụng các con số ước lượng chưa được đo.

Baseline hiện tại:

```text
KitJS kernel npm build: khoảng 9,4KB gzip
Bản /kit.js đầy đủ:     khoảng 25,6KB gzip
```

Đây đã là một kích thước tốt đối với runtime có:

* parser;
* evaluator;
* DOM walker;
* reactivity;
* morph;
* event delegation;
* component;
* scope.

KitJS V2 đặt size budget có thể kiểm chứng bằng CI:

```text
Core kernel: không vượt quá 12KB gzip
Full bundle: đo riêng theo từng build profile
```

Các capability lớn được tách khỏi core khi cần:

```text
@kitwork/kitjs/core
@kitwork/kitjs/morph
@kitwork/kitjs/platform
```

Mục tiêu không phải đạt một con số quảng cáo thật nhỏ.

Mục tiêu là:

> Giữ core có thể dự đoán, đo được và không tăng kích thước ngoài kiểm soát.

---

# 4. Scope

## 4.1 Object literal

Đây là cú pháp đầy đủ và phù hợp cho state phức tạp:

```html
<div
  data-kit-scope="{
    qty: 1,
    price: 250,
    user: {
      name: 'Quốc'
    },
    tags: ['go', 'runtime']
  }"
>
</div>
```

Ưu điểm:

* sử dụng cùng grammar với expression;
* hỗ trợ object và array lồng nhau;
* phù hợp với code generation;
* rõ ràng khi state phức tạp.

---

## 4.2 CSS-style shorthand

Đối với scope nhỏ, KitJS hỗ trợ cú pháp rút gọn:

```html
<div data-kit-scope="qty: 1; price: 250; loading: false">
</div>
```

Cú pháp này chỉ là syntactic sugar.

Runtime normalize nó thành biểu thức tương đương:

```js
{
  qty: 1,
  price: 250,
  loading: false
}
```

Giá trị của từng field vẫn sử dụng expression grammar chuẩn:

```html
<div
  data-kit-scope="
    user: { name: 'Quốc' };
    tags: ['go', 'runtime'];
    total: qty * price
  "
>
</div>
```

KitJS không xây dựng một expression engine thứ hai. Parser của scope shorthand nhận biết phân cách theo token depth (xử lý đúng chuỗi, object hoặc array chứa dấu chấm phẩy) và chuyển chúng thành `ObjectExpression`.

---

## 4.3 Lexical scope

Scope được resolve theo phần tử gần nhất.

Ví dụ:

```html
<div data-kit-scope="{ open: false }">
  <section data-kit-scope="{ loading: false }">
    <button data-kit-click="loading = true">
      Chạy
    </button>
  </section>
</div>
```

Trong button:

* `loading` thuộc scope của `<section>`;
* `open` có thể được resolve từ scope cha;
* assignment ưu tiên scope sở hữu biến gần nhất;
* biến mới không được âm thầm ghi vào global scope.

---

# 5. Component

Component gắn một tên và một tập hành vi vào reactive island.

```html
<div
  data-kit-component="modal"
  data-kit-scope="open: false; loading: false"
>
</div>
```

Component có thể cung cấp:

* initial state;
* methods;
* lifecycle;
* async actions;
* error handling;
* platform capabilities;
* reusable behavior.

Component không nhất thiết render HTML riêng. HTML có thể được viết trực tiếp trên server, còn component chỉ cung cấp runtime behavior.

---

# 6. Biến hệ thống

KitJS V2 cung cấp một tập biến hệ thống cố định.

## 6.1 `$this`

`$this` là phần tử sở hữu directive đang được thực thi.

Về mặt ngữ nghĩa, `$this` đóng vai trò tương đương với phần tử sở hữu handler, giống như vai trò mà `event.currentTarget` cung cấp khi listener được gắn trực tiếp. Trong cơ chế Event Delegation, `$this` do KitJS resolve từ AST Node của directive và không nhất thiết bằng với native `event.currentTarget` (vốn có thể trỏ tới delegation listener root như `document`).

Ví dụ khi người dùng bấm vào thẻ `<span>` bên trong nút bấm `<button data-kit-click="..."><span>Chọn</span></button>`:

```text
$this               = button (phần tử sở hữu data-kit-click)
$event.target       = span (thẻ thực sự nhận tương tác con)
$event.currentTarget = delegated listener root (ví dụ: document)
```

---

## 6.2 `$host`

`$host` là element sở hữu lexical scope gần nhất đang cung cấp context cho expression.

Ví dụ:

```html
<div data-kit-scope="{ loading: false }">
  <button
    data-kit-click="
      loading = true;
      $host.classList.add('is-loading')
    "
  >
    Xử lý
  </button>
</div>
```

Trong trường hợp scope lồng nhau:

```html
<div
  data-kit-component="modal"
  data-kit-scope="{ open: false }"
>
  <section data-kit-scope="{ loading: false }">
    <button data-kit-click="$host.classList.add('loading')">
      Chạy
    </button>
  </section>
</div>
```

Ở đây `$host` là `<section>`, không phải component modal bên ngoài.

---

## 6.3 `$event`

`$event` là native DOM event hiện tại.

```html
<input
  data-kit-input="search($event.target.value)"
>
```

Với form submit:

```html
<form
  data-kit-submit:prevent="
    $event.submitter.disabled = true;
    save()
  "
>
</form>
```

`$this` trong trường hợp này là `<form>`, còn `$event.submitter` là button đã gửi form. `$event` chỉ dành riêng cho DOM event.

---

## 6.4 `$root`

`$root` là root reactive state của `data-kit-app` hiện tại.

```html
<body
  data-kit-app="main"
  data-kit-scope="{ cartCount: 0 }"
>
  <button
    data-kit-click="
      $root.cartCount = $root.cartCount + 1
    "
  >
    Thêm vào giỏ
  </button>
</body>
```

---

## 6.5 `$refs`

`$refs` là registry chứa các reference có tên trong application hiện tại.

```html
<div
  data-kit-component="modal"
  data-kit-ref="paymentModal"
  data-kit-scope="{ open: false }"
>
</div>
```

Truy cập:

```html
<button
  data-kit-click="$refs.paymentModal.open = true"
>
  Mở modal
</button>
```

---

## 6.6 `$app`

`$app` là capability bridge đến trình duyệt, desktop, mobile wrapper hoặc native host.

Ví dụ:

```js
$app.camera
$app.qrcode
$app.clipboard
$app.storage
```

---

# 7. Alias và Ref Namespaces

KitJS hỗ trợ cả alias trực tiếp và ref có namespace. Chúng phục vụ các nhu cầu truy cập khác nhau và thuộc **hai namespace hoàn toàn riêng biệt**.

## 7.1 Alias

```html
<div
  data-kit-component="modal"
  data-kit-alias="$paymentModal"
  data-kit-scope="{ open: false }"
>
</div>
```

Truy cập trực tiếp:

```html
<button data-kit-click="$paymentModal.open = true">
  Mở modal
</button>
```

---

## 7.2 Ref

```html
<div
  data-kit-component="modal"
  data-kit-ref="paymentModal"
  data-kit-scope="{ open: false }"
>
</div>
```

Truy cập:

```html
<button data-kit-click="$refs.paymentModal.open = true">
  Mở modal
</button>
```

---

## 7.3 Sử dụng đồng thời

Một component instance **hoàn toàn có thể vừa khai báo Alias vừa khai báo Ref**:

```html
<section
  data-kit-component="modal"
  data-kit-alias="$paymentModal"
  data-kit-ref="paymentModal"
>
</section>
```

Về mặt ngữ nghĩa:

```js
$paymentModal === $refs.paymentModal
```

---

## 7.4 Quy tắc chống trùng lặp (Uniqueness Rules)

1. **Tên Alias không được trùng với một Alias khác trong cùng app:**
   - Hợp lệ: Không có hai thẻ nào cùng mang `data-kit-alias="$paymentModal"`.
2. **Tên Ref không được trùng với một Ref khác trong cùng app:**
   - Hợp lệ: Không có hai thẻ nào cùng mang `data-kit-ref="paymentModal"`.
3. **Alias và Ref có thể đặt cùng một tên trên cùng một instance** vì chúng nằm ở 2 namespace khác nhau (`$` namespace vs `$refs` registry).
4. Alias không được trùng các tên biến hệ thống reserved: `$this`, `$host`, `$event`, `$root`, `$refs`, `$app`.

---

# 8. Event directives & Modifier Pipeline Order

KitJS sử dụng tên native DOM event làm nền tảng cho event directive.

Cú pháp tổng quát:

```text
data-kit-<event>[:<modifier>[(<argument>)]]*
```

Ví dụ:

```html
data-kit-click="open()"
data-kit-submit:prevent="save()"
data-kit-click:stop="select()"
data-kit-input:debounce(300)="search()"
data-kit-click:throttle(1000)="pay()"
data-kit-keydown:window:escape="close()"
data-kit-click:once="initialize()"
```

---

## 8.1 Modifier Categories & Pipeline Order

Dù tác giả khai báo thứ tự modifier thế nào trong HTML, Runtime **luôn thực thi theo đường ống cố định (Deterministic Pipeline Order)**:

```text
1. Resolve Target       :window, :document
2. Filter Event         :outside, :escape, :enter
3. Prevent Default      :prevent
4. Propagation Control  :stop
5. Timing Control       :debounce, :throttle
6. Lifecycle Control     :once
7. Execute Expression
```

> **Lý do kiến trúc:** Kiểm tra Filter (như `:outside`, `:escape`) bắt buộc phải chạy TRƯỚC `:prevent` và `:stop`. Nếu filter không thỏa mãn, sự kiện không bị nuốt (preventDefault/stopPropagation) và tiếp tục lan truyền tự nhiên.

---

# 9. Native events và synthetic sources

## 9.1 Target Modifiers vs Filter Modifiers

KitJS phân biệt rõ ràng giữa vị trí lắng nghe (Target) và điều kiện lọc (Filter):

- **Target Modifiers:** `:window`, `:document` (xác định nơi đăng ký listener).
- **Filter Modifiers:** `:outside`, `:escape`, `:enter` (điều kiện kiểm tra trước khi thực thi).

## 9.2 Synthetic runtime sources

Hành vi xây dựng từ browser observer hoặc runtime lifecycle:

```html
data-kit-intersect="loadMore()"
data-kit-mount="initialize()"
```

---

# 10. Hiển thị

`data-kit-show` điều khiển native `hidden` attribute.

```html
<div data-kit-show="open">
  Nội dung
</div>
```

Hành vi tương đương:

```js
element.hidden = !value
```

Ưu điểm: không ghi đè `style.display`, giữ nguyên CSS Flexbox/Grid.

---

# 11. Attribute và Property Binding Contract

Binding được chia thành ba nhóm chiến lược dựa trên metadata của property:

## 11.1 Reflected Boolean

Ghi đồng thời vào cả DOM property và HTML attribute:

```text
disabled, required, readonly, multiple, hidden, open
```

```js
element.disabled = Boolean(value);
element.toggleAttribute('disabled', Boolean(value));
```

## 11.2 Live State Property

Chỉ cập nhật DOM Property hiện tại (giữ nguyên attribute mặc định cho form reset):

```text
checked, selected, value, indeterminate
```

```js
element.checked = Boolean(value);
element.value = String(value);
```

## 11.3 Attribute-only (`data-kit-attr:<name>`)

Dành cho ARIA và thuộc tính tùy biến:

```html
<div data-kit-attr:aria-expanded="open"></div>
```

## 11.4 Canonical Binding Syntax

Cú pháp chuẩn mực chính thức là `data-kit-bind:<prop>="expr"`. Các dạng `data-kit-disabled` hay `data-kit-value` chỉ là shorthand tùy chọn được normalize về canonical form.

---

# 12. Danh sách & SSR Blueprint Contract (`data-kit-for`, `data-kit-item`, `data-kit-key`)

Bộ 3 chỉ thị danh sách chuẩn mực của KitJS V2:

```text
data-kit-for   Khai báo phương thức tạo danh sách trong Source HTML
data-kit-item  Đánh dấu một DOM element đã được materialize từ blueprint
data-kit-key   Identity dữ liệu ổn định của item
```

> **Định nghĩa chính thức:**  
> **`data-kit-item="<loop-id>"` đánh dấu một DOM element đã được materialize từ một `data-kit-for` blueprint và liên kết element đó với loop runtime tương ứng.**

---

## 12.1 Authoring vs SSR Output Contract

Có sự phân định sạch sẽ và rõ ràng giữa HTML tác giả viết và HTML do Go Engine SSR render:

### Source Authored HTML (Code tác giả viết):
```html
<ul>
  <li
    data-kit-for="item, index of items"
    data-kit-key="item.id"
  >
    <span data-kit-text="item.name"></span>
  </li>
</ul>
```

### SSR Output HTML (Kết quả Go Engine render ra browser):
```html
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

- `data-kit-for` chỉ tồn tại trong source declaration hoặc compiled blueprint.
- `data-kit-item="<loop-id>"` chỉ tồn tại trên các item đã materialize.
- `data-kit-key` giữ identity ổn định khi insert, remove hoặc reorder.

---

## 12.2 Danh Sách Lồng Nhau (Nested Lists)

`data-kit-item` giải quyết hoàn hảo danh sách lồng nhau mà không bị nhầm lẫn ranh giới:

```html
<!--kit-for:start id=categories-1-->

<section data-kit-item="categories-1" data-kit-key="category-a">

  <!--kit-for:start id=products-1-->

  <article data-kit-item="products-1" data-kit-key="product-a">
    ...
  </article>

  <!--kit-for:end id=products-1-->

</section>

<!--kit-for:end id=categories-1-->
```

Mỗi item tự khai báo rõ nó thuộc loop nào (`categories-1` hay `products-1`), giữ cho cây DOM cực kỳ trong sáng và dễ debug.

---

# 13. Async actions

HTML gọi method component:

```html
<button data-kit-click="scanQRCode()">
  Quét mã QR
</button>
```

Method xử lý async:

```js
async function scanQRCode() {
  loading = true
  try {
    scannedToken = await $app.qrcode.scan()
  } finally {
    loading = false
  }
}
```

Runtime Contract: `prevent` và `stop` chạy đồng bộ trước expression. Rejection Promise chuyển đến error boundary.

---

# 14. Error Handling & Error Boundary (`$error`)

Xử lý lỗi sử dụng biến hệ thống dành riêng **`$error`**:

```html
<div
  data-kit-component="payment"
  data-kit-error="handleError($error)"
>
</div>
```

Cấu trúc đối tượng `$error`:
- `$error.cause` : Lỗi gốc (Error object / Exception)
- `$error.directive` : Directive gây lỗi
- `$error.element` : Element liên quan
- `$error.scope` : Scope path hiện tại

Semantics được giữ sạch tuyệt đối: **`$event` chỉ dành cho DOM Event, `$error` chỉ dành cho Error Boundary.**

---

# 15. Server Twin & Conformance Test Suite

Go Engine Evaluator và KitJS Client Evaluator phải vượt qua cùng một Conformance Test Suite chạy trong CI.

Ví dụ:

```text
Expression: qty * price
Scope:      { qty: 2, price: 250 }
Expected:   500
```

Nếu kết quả tính toán giữa Go và KitJS khác nhau ➔ CI Build bắt buộc thất bại.

---

# 16. Hydration State Ownership & Mismatch Contract

1. **Nguyên tắc Hợp đồng:** 
   > Khi boot, KitJS giữ nguyên DOM do server render và dùng nó làm binding snapshot ban đầu. Sau khi hydration hoàn tất, bất kỳ mutation hợp lệ nào của reactive state (từ user interaction, async fetch, SSE, timer, lifecycle hay bridge), đều cập nhật DOM theo binding contract.

2. **Xử lý Mismatch State:** 
   Server serialize chính state đã dùng để render vào scope (`data-kit-scope="{ amount: 500 }"`). 
   - Dev mode: In warning chi tiết `[KitJS Hydrate Warning] Scope mismatch at <strong data-kit-text>`.
   - Client Hydration Flush đầu tiên: Client Reactive State tiếp quản và đồng bộ thống nhất DOM.

---

# 17. Demo hoàn chỉnh

```html
<!DOCTYPE html>
<html lang="vi">
<head>
  <meta charset="UTF-8">
  <title>{{ page.title }}</title>
  <script src="/kit.js" defer></script>
</head>
<body data-kit-app="main" data-kit-scope="{ cartCount: 0 }">

  <!-- Inline reactive scope -->
  <section class="cart-item" data-kit-scope="qty: 1; price: 250">
    <p>Sản phẩm A</p>
    <button type="button" data-kit-click:stop="qty = qty > 1 ? qty - 1 : 1">Giảm</button>
    <strong data-kit-text="qty">1</strong>
    <button type="button" data-kit-click="qty = qty + 1">Tăng</button>
    <p>Tổng tiền: <strong data-kit-text="qty * price">{{ initialTotal }}</strong></p>
  </section>

  <!-- Payment modal -->
  <section
    data-kit-component="payment-modal"
    data-kit-alias="$paymentModal"
    data-kit-ref="paymentModal"
    data-kit-scope="{ open: false, loading: false, title: 'Xác nhận thanh toán', amount: 500, error: null }"
    data-kit-error="handleError($error)"
  >
    <div class="modal-backdrop" data-kit-show="open">
      <div class="modal-box" data-kit-click:outside="open = false" data-kit-keydown:window:escape="open = false">
        <h2 data-kit-text="title">Xác nhận thanh toán</h2>
        <p>Số tiền: <strong data-kit-text="amount">500</strong></p>
        
        <button type="button" data-kit-click="scanQRCode()" data-kit-bind:disabled="loading">Quét mã QR</button>

        <form data-kit-submit:prevent="$event.submitter.disabled = true; submitPayment()">
          <button type="submit" data-kit-bind:disabled="loading">Xác nhận</button>
          <button type="button" data-kit-click="open = false">Hủy</button>
        </form>

        <p data-kit-show="error" data-kit-text="error.cause.message"></p>
      </div>
    </div>
  </section>

  <!-- Keyed list (SSR Output Format) -->
  <ul data-kit-scope="{ items: [] }">
    <!--kit-for:start id=items-1-->
    <li data-kit-item="items-1" data-kit-key="a">
      <span data-kit-text="item.name">Sản phẩm A</span>
      <button type="button" data-kit-click="remove(item.id)">Xóa</button>
    </li>
    <li data-kit-item="items-1" data-kit-key="b">
      <span data-kit-text="item.name">Sản phẩm B</span>
      <button type="button" data-kit-click="remove(item.id)">Xóa</button>
    </li>
    <!--kit-for:end id=items-1-->
  </ul>

</body>
</html>
```

---

# 18. Những quyết định chính thức của KitJS V2 (Official Frozen Decisions)

1. KitJS sử dụng mô hình HTML-first reactive islands.
2. Scope và component là hai khái niệm liên quan nhưng không đồng nhất.
3. `{{ expression }}` thuộc server rendering; `data-kit-*` thuộc client reactivity. Không sử dụng `${expression}` trong text node.
4. `data-kit-text` là cơ chế reactive text chính thức.
5. `data-kit-scope` hỗ trợ cả object literal và CSS-style shorthand, được normalize thành cùng một `ObjectExpression`.
6. Giữ `$this`, `$host`, `$event`, `$root`, `$refs`, `$app` với semantics nghiêm ngặt.
7. `$this` đóng vai trò ngữ nghĩa của phần tử sở hữu directive handler (directive owner); `$host` là scope boundary element gần nhất; `$event` chỉ dành riêng cho native DOM event.
8. `$error` là biến hệ thống dành riêng cho error boundary.
9. Alias và ref cùng được hỗ trợ, thuộc hai namespace khác nhau và có thể cùng tham chiếu một component hoặc scope instance.
10. Tên Alias không được trùng giữa các Alias khác; Tên Ref không được trùng giữa các Ref khác trong cùng app.
11. Event modifier thực thi theo **Deterministic Pipeline Order**: Target ➔ Filter ➔ Prevent ➔ Stop ➔ Timing ➔ Once ➔ Execute.
12. `outside` và `escape` được phân loại chính xác là Filter Modifiers.
13. `data-kit-show` điều khiển native `hidden` attribute.
14. Binding strategy chia 3 nhóm: Reflected Boolean (DOM prop + attr), Live State Property (DOM prop only), Attribute-only (`data-kit-attr:*`).
15. Canonical binding syntax là `data-kit-bind:<prop>="expr"`.
16. Bộ chỉ thị danh sách gồm: `data-kit-for` (khai báo source), `data-kit-item="<loop-id>"` (thẻ đã materialize từ SSR), `data-kit-key` (stable data identity). Prototype đến từ Compiled Server IR Blueprint.
17. Hydration tuân thủ nguyên tắc: *"Server Content First, Client Reactive State Ownership"*. Bất kỳ reactive mutation nào đều cập nhật DOM theo contract.
18. Server Go và Client KitJS dùng chung Conformance Test Suite trong CI.
19. Core bundle budget <= 12KB gzip, kiểm tra bằng CI size check.
20. KitJS không dùng `eval()` hoặc `new Function()`.
