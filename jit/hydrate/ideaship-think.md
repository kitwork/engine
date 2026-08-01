# KitJS V2 — Bản tổng hợp và phản biện

> **Vai trò của tài liệu này:** hợp nhất `ideaship.md`, `ideashipping.md`, `commit.md` và `idea.md`,
> giải các mâu thuẫn giữa chúng, và ghi rõ chỗ **chưa chốt** thay vì giả vờ đã chốt.
> Viết sau khi đọc từng dòng `kernel.js`, `eval.go`, `compile.go`, `morph.js`, `drive.js`,
> `bridge.js`, `modules/native.js` và `legacy/core/reactive`.
>
> **Trạng thái:** đề xuất để phản biện. Không phải quyết định đã đóng.

---

# 0. Bản đồ tài liệu — đọc cái này trước

Năm tài liệu KHÔNG cạnh tranh nhau. Chúng là năm lớp:

| File | Lớp | Dùng khi |
| :--- | :--- | :--- |
| `ARCHITECTURE.md` | Hợp đồng composition **đang chạy** | Muốn biết runtime hôm nay ghép thế nào |
| `commit.md` | Chẩn đoán gốc: kernel trộn bốn vai | Muốn hiểu VÌ SAO cần V2 |
| `ideaship.md` | Bề mặt **tác giả** (cú pháp, biến hệ thống) | Viết HTML |
| `ideashipping.md` | Brief **thực thi** cho agent | Bắt tay sửa code |
| `idea.md` | Đặc tả API `$app` capability | Làm bridge / native |

**Quy tắc:** khi hai tài liệu nói khác nhau về cùng một thứ, tài liệu này là trọng tài. Nếu tài liệu
này im lặng, `ideashipping.md` thắng (vì nó có kỷ luật thực thi).

---

# 1. Mâu thuẫn chiến lược phải giải trước mọi thứ khác

`commit.md` mở đầu:

> "Mình sẽ **không sửa chắp vá runtime cũ**. Hãy … viết một kernel mới có ranh giới rõ ràng."

`ideashipping.md` §1:

> "**Không viết lại từ đầu.** Kế thừa kernel hiện tại."

Hai câu này ngược nhau, và chúng là câu hỏi đắt nhất trong toàn bộ dự án.

## Phán quyết đề xuất: cả hai đều đúng, ở hai mức khác nhau

`commit.md` đúng về **chẩn đoán**: `kernel.js` 1.050 dòng đang trộn bốn vai — expression/runtime,
reactive UI, SPA Drive, platform capabilities. Đó là gốc rễ thật.

`ideashipping.md` đúng về **phương pháp**: viết lại từ đầu là cách mất lặng lẽ nửa năng lực. Kernel
hiện tại chứa những thứ đã trả giá để học được — `blockedKey`, ngân sách 10k node, leak-free
by-architecture, boot-guard, morph giữ focus/cursor.

**Cách hoà giải:** tách vai bằng **trích xuất tại chỗ**, không phải viết mới.

```text
Mỗi bước: chuyển MỘT vai ra khỏi kernel.js
          → chạy full test + conformance
          → kernel nhỏ đi, năng lực không đổi
```

Đo được bằng `wc -l`. Nếu một bước làm mất một năng lực trong inventory §4 của `ideashipping.md`,
bước đó sai và phải hoàn tác. Không có "big rewrite" nào cả — chỉ có một chuỗi phép trừ.

---

# 2. Nhận thức chịu lực mà chưa tài liệu nào ghi

Đây là đóng góp chính của tài liệu này. Nó giải thích vì sao vài quyết định "kém hiện đại" lại đúng.

## 2.1 Dirty-check là ĐIỀU KIỆN để có server twin

`render()` (`kernel.js:607`) quét lại toàn document mỗi tick. Không đồ thị phụ thuộc. Đó là
dirty-checking kiểu AngularJS 1.x, và nhìn qua thì "lạc hậu" so với `legacy/core/reactive` — vốn là
một bản Vue 3 reactivity đầy đủ (WeakMap dep bucket, `activeEffect`, track/trigger, `ITERATE_KEY`).

Nhưng:

> **Không thể có đồng thời Vue-style reactivity VÀ một server twin.**
> Đồ thị Proxy phản ứng không chạy được trong Go để PreRender. Client walk IR; server walk **cùng**
> IR. Chỉ mô hình walk-based mới có twin.

Legacy chọn reactivity → cái giá là **all-client, không server twin**.
Bản hiện tại chọn dirty-check → phần thưởng là `walk`/`eval`, một IR, hai đầu, cùng đo gas.

Nó cũng đúng với kiến trúc: Kitwork render-ở-server-trước, nên bề mặt tương tác client nhỏ **theo
thiết kế**. Vue reactivity tồn tại vì SPA có bề mặt phản ứng khổng lồ — Kitwork không có bài toán đó.

**Hệ quả:** mỗi lần nghe "sao component không tự cập nhật khi tôi đổi mảng?", câu trả lời phải là
dirty-check + re-render, và đó là lý do cùng component chạy được trên `eval.go`. Đừng nhượng bộ.

## 2.2 Rủi ro thật nằm ở scope, không nằm ở số học

`eval.go:63` ghi thẳng:

> "On the server the scope is flat (one map) … the client walker distinguishes them across scopes."

- Client: scope là **chuỗi lexical** (`scopeFor` leo cây DOM, `kernel.js:498`)
- Server: scope là **một map phẳng**

Ghi **xuyên biên component** là chỗ duy nhất hai twin có thể tách nhau. Và `eval.go:53` (key thiếu
= 0) với `:176` (gọi non-lambda = undefined) cho thấy chúng đang được khớp **bằng tay**.

Đây không phải chuẩn bị cho mobile. Đây là nợ đang chạy production.

---

# 3. Triết lý

## 3.1 HTML-first Reactive Islands

Lấy nguyên từ `ideaship.md` §2.1 — cách phát biểu này tự nhất quán, không cần biện hộ:

- `data-kit-scope` tạo một vùng trạng thái cục bộ;
- `data-kit-component` gắn hành vi **có tên, tái dùng được**;
- component **có thể** sở hữu scope;
- scope **không bắt buộc** là component.

> Nền móng của KitJS không phải component. Là **lexical scope gắn với HTML**.

Câu "mọi thứ đều là component" bị **bác bỏ**: nó tự mâu thuẫn với inline scope, và nó bắt ca đơn
giản (một nút copy) phải trả giá của ca phức tạp.

## 3.2 Ba tầng, không phải một

Hệ quả trực tiếp của 3.1 — thay cho "pure component hết":

| Nhu cầu | Về đâu | Nặng bao nhiêu |
| :--- | :--- | :--- |
| Vô trạng thái (copy, dismiss) | expression + `$app` | nhẹ nhất — không registration |
| State cục bộ, dùng một lần | `data-kit-scope` | một attribute |
| Hành vi tái dùng có tên | component | file |

Ép `copy` thành component là thêm boundary cho ca không cần — đúng cái thuế mà dự án tồn tại để tránh.

## 3.3 Một grammar

Nhiều surface syntax, **một AST**, một runtime behavior. Mọi cú pháp tiện lợi phải normalize về cây
mà `compile.go` sinh ra và `eval.go` chạy được — nếu không, nó không có server twin.

---

# 4. Biến hệ thống — giải xung đột `$root`

`ideaship.md` §6.4 định nghĩa `$root` = **root state**.
`kernel.js:489` hiện dùng `$root` = **DOM boundary**.
`ideashipping.md` §7 hoãn bằng "compatibility alias".

Một biến hệ thống có hai nghĩa tuỳ tài liệu người ta đọc là lỗi nặng hơn cả việc đổi tên.

## Phán quyết: giữ nghĩa của CODE

```text
$this   Element sở hữu directive đang chạy   (canonical)
$el     Alias tương thích của $this
$host   Scope/component boundary gần nhất    (canonical)
$root   Alias tương thích của $host  ← GIỮ NGHĨA DOM, không repurpose
$       Page/application root state          (đã vậy trong code)
$event  Native DOM event — CHỈ dùng cho DOM event
$error  Error boundary context — CHỈ dùng cho lỗi
$refs   Registry tham chiếu DOM element
$app    Host capability bridge
```

Nếu muốn một tên rõ hơn cho root state, dùng `$page` — **đừng** đụng `$root`.

## `$this` — sống, nhưng có điều kiện

`ideaship.md` §6.1 cứu `$this` đúng về kỹ thuật: dưới event delegation, `$this` do KitJS resolve từ
directive owner, khác `event.currentTarget` (là `document`).

Nhưng phản biện thực dụng vẫn còn: `$this.disabled` trên `<form>` **vô nghĩa** — form không có
`disabled`. Chính demo của `ideaship.md` cũng lách bằng `$event.submitter`.

**Điều kiện giữ `$this`:** tài liệu phải nói thẳng — `$this` là *thẻ mang directive*, KHÔNG phải
*thứ vừa được bấm*. Với form, dùng `$event.submitter`. Nếu không viết được câu đó cho gọn, đổi sang
`$el` (không cần chú thích 8 dòng để khỏi bị hiểu sai).

---

# 5. Alias và Ref — theo phân vai đã chốt

`ideaship.md` §7.3 và `ideashipping.md` §14 đều ghi `$paymentModal === $refs.paymentModal`.
**Câu đó nay sai**, vì phân vai đã đổi:

```text
alias  →  component INSTANCE (scope object)   — đọc/ghi state
ref    →  DOM ELEMENT                          — gọi method của element
```

```html
<section data-kit-component="modal" data-kit-alias="$paymentModal">
  <input data-kit-ref="search">
</section>
```

```js
$paymentModal.open = true      // scope object
$refs.search.focus()           // element
```

Hai loại giá trị khác nhau → **không bao giờ `===`**. Và phần lớn "collision rules" tự biến mất, vì
hai namespace giờ chứa hai loại.

**Lợi ích phụ:** `$host` và `$refs.x` cùng loại (element); `$alias` cùng loại với scope. Ngữ nghĩa
sạch hơn hẳn.

**Còn phải chốt:** `$refs` gom theo phạm vi nào? Đề xuất: theo **component instance gần nhất**, không
global — nếu không, hai modal cùng có `ref="search"` sẽ đụng nhau.

---

# 6. Scope syntax — bỏ parser thứ hai

`ideaship.md` §4.2 nói CSS-shorthand "chỉ là syntactic sugar", rồi thừa nhận nó cần *"parser nhận
biết phân cách theo token depth (xử lý đúng chuỗi, object, array chứa dấu chấm phẩy)"*.

Đó **không** phải sugar — đó là bộ tách token thứ hai, phải viết **hai lần** (`kernel.js` +
`compile.go`), và phải qua conformance suite.

## Đề xuất: object literal thiếu ngoặc

Đừng thêm cú pháp — **nới lỏng cái đã có**:

```html
data-kit-scope="qty: 1, price: 250"            <!-- không {} -->
data-kit-scope="{ qty: 1, price: 250 }"        <!-- có {} — vẫn chạy -->
```

Một luật trong `primary()`: nếu biểu thức bắt đầu bằng `id :` thì parse như thân object cho tới hết.
Dùng **đúng** `assign()` cho mỗi value, **đúng** dấu `,`, **đúng** cây `["{}", pairs]`.

| Cú pháp | Ký tự thừa | Parser mới |
| :--- | :--- | :--- |
| `{ qty: 1, price: 250 }` | `{}` | không |
| `qty: 1; price: 250` | không | **có** |
| `qty: 1, price: 250` | không | **không** |

Ca khó vẫn đúng vì `,` bên trong `{...}`/`[...]` đã được `primary()` nuốt:

```html
data-kit-scope="user: { name: 'Quốc' }, tags: ['go'], total: qty * price"
```

**Bác bỏ** ý "state phức tạp thì nên thành component": nó bắt *hình dạng state* quyết định *kiến
trúc*. Ranh giới đúng là **hành vi tái dùng có tên**, không phải "state đủ phức tạp".

---

# 7. Event directives — phần mạnh nhất, giữ nguyên

Lấy từ `ideaship.md` §8 gần như nguyên văn. Đây là ý hay nhất trong cả loạt tài liệu: dùng **tên
sự kiện DOM native** làm bộ khung, nên tác giả không phải học từ vựng riêng.

```text
data-kit-<event>[:<modifier>[(<argument>)]]*
```

## Deterministic Pipeline Order

Runtime **không** phụ thuộc thứ tự tác giả viết:

```text
1. Resolve Target       :window, :document
2. Filter Event         :outside, :escape, :enter
3. Prevent Default      :prevent
4. Propagation Control  :stop
5. Timing Control       :debounce, :throttle
6. Lifecycle Control    :once
7. Execute Expression
```

**Lý do Filter phải trước Prevent/Stop:** nếu filter không thoả, event **không bị nuốt** và tiếp tục
lan truyền tự nhiên.

## Hai lỗi trong demo cũ — phải ghi thành luật

- `keydown:escape` trên `<div>` **không chạy** — div không tự nhận focus. Modal phải dùng
  `data-kit-keydown:window:escape`.
- `click:outside` đặt trên **backdrop** là sai — backdrop phủ toàn màn hình nên gần như không có vùng
  "outside". Phải đặt trên `.modal-box`.

Dùng `event.composedPath()` cho `outside` để đúng cả với Shadow DOM.

---

# 8. Binding contract — bắt một bug đang tồn tại

`render()` hiện tại (`kernel.js:614-625`) chỉ dùng `setAttribute`. Nên `data-kit-bind="{ checked: x }"`
**hỏng thật** với checkbox: `checked` là property, `setAttribute` chỉ đổi giá trị mặc định.

Ba nhóm (từ `ideaship.md` §11 — đây là phát hiện tốt):

```text
Reflected boolean   → property + attribute
                      disabled, required, readonly, multiple, hidden, open

Live state property → property only (giữ attribute cho form reset)
                      checked, selected, value, indeterminate

Attribute-only      → data-kit-attr:<name>   (ARIA, data-*, custom)
```

Canonical: `data-kit-bind:<prop>="expr"`. Dạng ngắn `data-kit-disabled` là shorthand normalize về
canonical.

`data-kit-show` → `element.hidden = !value` (đã đúng trong code). Giữ DOM, giữ Flexbox/Grid.

---

# 9. Block Engine — `if` và `for` dùng chung một máy

Lấy từ `ideashipping.md` §16. Đây là thiết kế **tốt hơn** đề xuất container-based ban đầu của tôi.

```text
Block
├── id
├── start marker  <!--kit-for:start id=…-->
├── end marker    <!--kit-for:end id=…-->
├── blueprint
├── instances
├── child scopes
├── keys
└── resources
```

```text
data-kit-for   Source declaration
data-kit-key   Key expression trong source
data-kit-item  Stable identity trên DOM item đã materialize
```

## Ba điều bắt buộc

**1. Blueprint KHÔNG lấy từ live hydrated DOM.** Item đầu đã hydrate mang `value`, focus, state —
clone nó là nhân bản trạng thái bẩn. Blueprint phải đến từ compiled server IR, hoặc immutable
pre-hydration clone đã strip live state. **Ưu tiên compiled IR.**

**2. Không mutate item object** để nhét metadata runtime. Child scope là **overlay**:
`item, index, count, first, last, even, odd`.

**3. Hợp nhất key resolver với `morph`.** `morph.js:47` đã có keyed reconciliation. Không xây hai
identity engine.

`data-kit-if` = mount/unmount subtree (khác `show` = giữ DOM, đổi visibility), triển khai bằng **cùng**
Block Engine. Không cần `switch/case` (không tạo semantics mới) và không cần `data-kit-hidden` (chỉ
đảo nghĩa `show`).

---

# 10. Async — CHƯA CHỐT, và đây là quyết định gốc

`ideashipping.md` §19 nói method là `async function` JavaScript.
`ideashipping.md` §25 nói Go evaluator và KitJS evaluator phải qua **cùng** fixtures, lệch thì CI fail.

**Hai mục này không thể cùng đúng.** `async`/`await` không chạy trên `eval.go` — Go walker không có
Promise. Nên component có async **không** qua được conformance và **không** PreRender được.

Đây không phải chi tiết cuối. Nó quyết định **method component viết bằng ngôn ngữ gì**, mà điều đó
quyết định `data-kit-click="scanQRCode()"` gọi vào cái gì.

## Ba lựa chọn thật — phải chọn một

**A. Method async là JS thật.**
Ghi thành **ngoại lệ tường minh**: component có async không có server twin, không PreRender.
Đơn giản nhất, và trung thực nếu ghi rõ.

**B. Method thuần đồng bộ; async đẩy hết vào capability.**
Transition chỉ **gán** một Promise; `set` trap của scope tháo nó, ghi giá trị thật, re-render.
Mẫu này **đã tồn tại** trong code — `data-kit-api` (`kernel.js:874`) và `data-kit-live` (`:851`) đều
là "effect → scope patch → re-render". Chỉ cần tổng quát hoá.
Giữ twin trọn vẹn. Cái giá: viết khác đi.

**C. Twin chỉ áp cho expression, không áp cho method.**
Thu hẹp phạm vi conformance. Trung thực, nhưng làm yếu lời hứa "một ngôn ngữ, hai runtime".

**Nghiêng của tôi: B** — vì mẫu đã có sẵn, và nó là thứ duy nhất giữ được lời hứa. Nhưng đây là
quyết định của tác giả, không phải của tài liệu.

**Cho tới khi chốt:** không đóng băng §19 và §25 như thể chúng nhất quán.

---

# 11. Conformance Suite — nhắm đúng chỗ

Go evaluator và KitJS evaluator chạy **cùng một corpus**, lệch thì CI fail.

Nhưng đừng chỉ test số học. Ca sinh tử là **ghi xuyên biên scope** (§2.2):

```text
Expression: $.count = $.count + 1; $.count
Scope:      { count: 41 }
Expected:   42
```

Tối thiểu: literals, arrays, objects, arithmetic, comparison, boolean logic, truthiness, property
access, method calls, assignment, **lexical resolution**, **shadowing**, **nearest-owner assignment**,
**new-key assignment**, missing variable, reserved variables, null behavior.

Đây là cổng cho mọi hợp nhất sau này, **và** là tiền đề bắt buộc cho capsule (code client gửi lên
chạy dưới identity — buộc phải biết hai đầu hiểu giống nhau).

---

# 12. Hai thứ nên HOÃN

## 12.1 Binding registry / dependency extraction — hoãn

`ideashipping.md` §13 đề xuất binding record + dependency map + dirty theo key, thay cho query toàn
document.

**Phản biện:** đây chính là đồ thị phụ thuộc — thứ mà cú lùi từ legacy reactive đã cố ý bỏ, và §2.1
giải thích vì sao cú lùi đó là **điều kiện** cho server twin.

§13 tự thừa nhận: *"nếu dependency không xác định tĩnh được → mark dynamic → dirty toàn boundary"*.
Với grammar này, **rất nhiều** biểu thức không phân tích tĩnh được (`items[i].name`, `$refs.x.value`,
method call). Nên sẽ có **hai đường cập nhật**, và đường chậm vẫn phải tồn tại. Tăng phức tạp để lấy
tối ưu cho bài toán **chưa ai đo là có**.

**Nếu một trang thật chạm trần**, làm bản rẻ hơn:

```text
read-set thu làm PHỤ PHẨM của lần walk
(walker đã thấy mọi node ["$", name])
+ scope theo BOUNDARY, không toàn document
```

90% lợi ích, 10% máy móc, và **không** cần static analysis hay fallback path.

## 12.2 Sáu build profile — hoãn

`ideashipping.md` §22 đề xuất `core/structural/data/drive/platform/full` trong khi kernel còn chưa
tách nổi `model`/`validate`/`live` ra khỏi 1.050 dòng.

Đặt sáu ranh giới **trước khi** biết chúng cắt ở đâu là cách tạo ranh giới sai rồi phải sống với nó.

**Đề xuất:** chỉ `core` và `full`, cho tới khi có người dùng thật nói họ cần bỏ `drive`. Ranh giới
nên do **nhu cầu** cắt, không do sơ đồ cắt.

---

# 13. Những gì XOÁ

Dấu hiệu hướng đúng: phần lớn việc là phép trừ.

| Xoá | Vì sao | Điều kiện trước |
| :--- | :--- | :--- |
| 9.002 dòng `runtime*.js` | `render.go` không embed bản nào — code chết | không |
| `lib/` (10 file action) + `behaviors` + `fire` + `compat.js` | 9 verb đang nhân đôi với `components/` | **effect trước** |
| Nhánh `typeof fn === "function"` (`kernel.js:310`) | Thứ DUY NHẤT không có server twin | async chốt xong |
| IR client `data-kitwork-*` | Parser luôn ship; IR là bản-viết-tay thứ tư | không |
| `legacy/core/{lexer,ast,evaluate,reactive}` | Con đường all-client đã bỏ đúng | không |
| 8 file `legacy/**/main copy*.js` | Bản sao bỏ quên, đang được git track | không |

**Thứ tự cứng:** `copy`/`get`/`more`/`submit` **trở thành** effect. Xoá action trước khi có effect =
gãy infinite-scroll đang chạy thật trên kitwork.io/.org/.vn (`data-kitwork-action="more"`).

---

# 14. Những gì GIỮ — inventory bắt buộc

Từ `ideashipping.md` §4. Không được mất năng lực nào mà chưa có quyết định loại bỏ tường minh:

zero-eval engine · lexer/parser/IR walker · `blockedKey` blocklist · lexical scope ·
component blueprint · event delegation · `data-kit-text` · `show` · `model` · `validate` ·
api/fetch · SSE/live · remember/storage · SPA navigation · morph · keyed identity · `$app` bridge ·
component init · compatibility syntax · build/generation flow.

**Luật:** phần nào cần refactor thì phải có regression test **trước**.

---

# 15. Legacy — mỏ, không phải rác

`legacy/` là **test corpus + tham chiếu semantics**, không phải nguồn copy.

**Mang qua:**

| | Vì sao |
| :--- | :--- |
| `field` (59) | Bảng thông điệp tiếng Việt map từ 9 cờ `ValidityState` — phần tốn công nhất, code chỉ là vỏ |
| `paginate` (24) | Thuần số học, không DOM, rủi ro bằng 0 |
| `qrlogin` (97) | QR + SSE + timeout + `isInternalLink` (lá chắn open-redirect) |
| `sharebox` (147) | `navigator.share` + fallback |
| `list` (203) + `helper/query` (201) | **url-as-state** + debounce + popstate — nên thành capability module, không phải component |

**Không mang:** evaluator rộng, per-node listener, component instance quá lớn, đồng bộ parent-child
hai chiều, mutate metadata vào item object, parser riêng cho từng directive, global access không an toàn.

---

# 16. Thứ tự thực thi

Mỗi pha tự trả công. Không pha nào phát minh cơ chế mới.

## Pha 0 — Nền (không đổi hành vi)

1. **Conformance suite** — nhắm ghi-xuyên-biên (§11). Nền an toàn cho mọi bước sau.
2. **Xoá code chết** — 9.002 dòng + `main copy*.js`.
3. **CI size check** — baseline đo thật, budget ≤ 12KB gzip core.

## Pha 1 — Năng lực còn thiếu

4. **Block Engine + `data-kit-for`** — tái dùng keyed reconciliation của `morph`. Chứng minh bằng
   tag editor: thêm/xoá chip, giữ focus, không round-trip. Đây là thứ biến "tăng cường HTML" thành
   "dựng app".
5. **Binding contract 3 nhóm** — sửa bug `checked` đang có.

## Pha 2 — Hợp nhất (sau khi chốt async)

6. **Cơ chế effect** — tổng quát hoá `api`/`live`.
7. **Xoá hệ action** — sau bước 6.
8. **Gộp một kiểu method** — xoá `typeof fn === "function"`.
9. **Bỏ IR client.**

## Pha 3 — Tách vai (theo §1: trích xuất, không viết mới)

10. Kéo `model`/`validate`/`live` ra khỏi kernel → kernel 1.050 → dưới 600.
11. Dispose trực tiếp; MutationObserver chỉ còn là fallback (`ideashipping.md` §15).
12. Port stdlib từ legacy (§15).

---

# 17. Bốn thứ CHƯA CHỐT

Ghi rõ ở đây thay vì đóng băng nhầm:

1. **Async** (§10) — A, B hay C. Đây là quyết định gốc, không phải chi tiết cuối.
2. **`$this` hay `$el`** — giữ `$this` thì phải viết được câu phân biệt "thẻ mang directive" vs "thứ
   vừa bấm" cho thật gọn.
3. **Phạm vi `$refs`** — theo component instance (đề xuất) hay theo app.
4. **`$error` lan truyền** — lỗi từ component con có nổi lên cha không? Không có câu trả lời thì
   error boundary chưa phải boundary.

---

# 18. Một câu cho toàn bộ

> Runtime này không cần đại tu. Nó cần **một directive** để thành framework app (`for`, tái dùng
> morph), **một bộ test** để đổi an toàn (conformance, nhắm ghi-xuyên-biên), **một cơ chế** để thành
> một-ngôn-ngữ (effect chung), và **một chuỗi phép trừ** để tách bốn vai ra khỏi kernel.
>
> Không bước nào phát minh cơ chế mới. Câu trả lời đã nằm trong code — việc còn lại là chốt bốn
> quyết định treo rồi ráp lại.
