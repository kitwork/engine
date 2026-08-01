# KitJS V2 — Bảng quy chuẩn hợp nhất (FINAL)

> **Đây là cửa chính.** Nó hợp nhất 7 tài liệu thành một bảng tra cứu, giải các chỗ chúng nói khác
> nhau, và **tách rõ cái ĐÃ CHỐT với cái CÒN TREO** — vì một quyết định chưa chốt mà bị đóng băng sẽ
> thành lỗi cho người triển khai.
>
> Viết sau khi đọc từng dòng `kernel.js`, `eval.go`, `compile.go`, `morph.js`, `drive.js`,
> `bridge.js`, `modules/native.js`. Khi bảng này khác một tài liệu khác, **bảng này đúng**.

---

## 0. Vai trò 7 tài liệu — đọc cái nào khi nào

| File | Là gì | Mở khi |
| :--- | :--- | :--- |
| **`ideaship-final.md`** (bản này) | Cửa chính, đã giải mâu thuẫn | Cần câu trả lời cuối cùng |
| `ideaship-master.md` | 6 bảng bề mặt tác giả | Muốn tra nhanh cú pháp |
| `ideaship.md` | Tuyên ngôn + giải thích DX | Muốn hiểu VÌ SAO một cú pháp |
| `ideashipping.md` | Brief cho coding agent | Bắt tay sửa code |
| `ideaship-extension.md` | 6 tính năng nâng cao | Cân nhắc mở rộng (phần lớn HOÃN) |
| `commit.md` | Chẩn đoán "kernel trộn 4 vai" | Hiểu gốc rễ V2 |
| `idea.md` | API `$app` capability | Làm bridge / native |

---

## 1. Triết lý — 5 câu

| # | Nguyên tắc | Nghĩa |
| :-- | :--- | :--- |
| 1 | **HTML-first Reactive Islands** | HTML là cấu trúc. KitJS thêm state + hành vi vào đúng chỗ. Nền móng là **lexical scope**, không phải component |
| 2 | **Một grammar, một AST** | Nhiều cú pháp tiện lợi → cùng một cây IR mà `compile.go` sinh và `eval.go` chạy. Không có twin thì không hợp lệ |
| 3 | **Zero-eval** | Không `eval`, không `new Function`. An toàn *by construction* qua blocklist, không chỉ nhờ CSP |
| 4 | **Server twin** | `walk` (client) và `eval` (server) chạy **cùng** IR. Đây là viên ngọc — mọi quyết định phải giữ nó |
| 5 | **Ba tầng, không "mọi thứ là component"** | vô trạng thái → expression+`$app`; state dùng-một-lần → `data-kit-scope`; hành vi tái dùng → component |

> ⚠️ **Câu chịu lực chưa tài liệu nào ghi:** dirty-check (quét lại, không đồ thị phụ thuộc) **là điều
> kiện** để có server twin. Vue-style reactivity không chạy được trong Go. Giữ dirty-check.

---

## 2. Bảng 14 Directive

| Directive | Mục đích | Ví dụ | Semantics |
| :--- | :--- | :--- | :--- |
| `data-kit-component` | Khai báo component | `="modal"` | Hành vi có tên, tái dùng |
| `data-kit-scope` | Khởi tạo state cục bộ | `="{ qty: 1 }"` | Object literal (§6) |
| `data-kit-alias` | Tên truy cập **component instance** | `="$paymentModal"` | Là **scope object** (§5) |
| `data-kit-ref` | Tên truy cập **DOM element** | `="search"` | Là **element**, vào `$refs` (§5) |
| `data-kit-show` | Ẩn/hiện | `="isOpen"` | `element.hidden = !value` (giữ Flex/Grid) |
| `data-kit-text` | Binding text | `="qty * price"` | `textContent`. **Không** dùng `${}` |
| `data-kit-bind:<prop>` | Binding property (canonical) | `:disabled="loading"` | Theo 3 nhóm (§4) |
| `data-kit-attr:<name>` | Binding attribute thuần | `:aria-expanded="open"` | `setAttribute` — ARIA, data-* |
| `data-kit-model` | Binding 2 chiều | `="user.name"` | input/select/textarea |
| `data-kit-for` | Khai báo danh sách (**source**) | `="item, i of items"` | Chỉ ở HTML tác giả viết (§5-list) |
| `data-kit-item` | Item đã SSR materialize | `="items-1"` | Chỉ ở HTML server render ra |
| `data-kit-key` | Định danh ổn định | `="item.id"` | Giữ DOM khi insert/remove/reorder |
| `data-kit-if` | Mount/unmount subtree | `="isEditing"` | Dùng chung Block Engine với `for` |
| `data-kit-error` | Error boundary | `="handle($error)"` | Bắt lỗi component → `$error` |

Sự kiện là một họ riêng: `data-kit-<event>` (§3).

---

## 3. Bảng 7 Biến hệ thống — ĐÃ SỬA xung đột `$root`

| Biến | Là gì | Loại giá trị | Alias tương thích |
| :--- | :--- | :--- | :--- |
| `$this` | Thẻ **sở hữu directive** đang chạy | Element | `$el` |
| `$host` | Thẻ **boundary scope gần nhất** | Element | `$root` |
| `$event` | Native DOM event | Event | — |
| `$error` | Ngữ cảnh error boundary | Object | — |
| `$refs` | Registry **DOM element** có tên | Registry | — |
| `$app` | Cầu nối capability (camera, qr, clipboard…) | Bridge | — |
| `$` | **Root state** của trang/app | Object (state) | — |

> **Sửa so với master:** master gộp `$/$root` làm "root state" — **sai**. Trong code:
> `$` = state gốc (object), `$root` = **element boundary** (`kernel.js:489`), tức alias của `$host`,
> KHÔNG phải state. Muốn tên rõ hơn cho state → `$page`, đừng đụng `$root`.
>
> `$this` dưới event delegation: KitJS resolve từ directive owner, **khác** `event.currentTarget`
> (là `document`). Với form: dùng `$event.submitter`, **không** `$this` (form không có `disabled`).

---

## 4. Bảng Đường ống Modifier — thứ tự CỐ ĐỊNH

Tác giả viết thứ tự nào cũng được; runtime luôn chạy theo:

| # | Nhóm | Modifier | Việc |
| :-: | :--- | :--- | :--- |
| 1 | Target | `:window` `:document` | Gắn listener ở đâu |
| 2 | **Filter** | `:outside` `:escape` `:enter` | Điều kiện. FAIL → **dừng, event KHÔNG bị nuốt** |
| 3 | Prevent | `:prevent` | `preventDefault()` |
| 4 | Stop | `:stop` | `stopPropagation()` |
| 5 | Timing | `:debounce(n)` `:throttle(n)` | Hoãn / tiết chế |
| 6 | Lifecycle | `:once` | Chạy 1 lần rồi tự gỡ |
| 7 | Execute | | Chạy biểu thức |

> **Filter phải TRƯỚC Prevent/Stop** — nếu không, một click bị lọc ra vẫn lỡ nuốt mất event.
> **Hai bẫy trong demo cũ:** `keydown:escape` trên `<div>` không chạy (div không nhận focus) → dùng
> `:window`. `click:outside` phải đặt trên `.modal-box`, không phải backdrop phủ toàn màn hình.

---

## 5. Bảng Binding 3 nhóm — sửa bug `checked` đang có

`render()` hiện chỉ dùng `setAttribute` (`kernel.js:614`) → `data-kit-bind:checked` **hỏng thật**.

| Nhóm | Thuộc tính | Cơ chế |
| :--- | :--- | :--- |
| **Reflected Boolean** | `disabled` `required` `readonly` `multiple` `hidden` `open` | property **+** attribute |
| **Live State Property** | `checked` `selected` `value` `indeterminate` | property **only** (giữ attr cho form reset) |
| **Attribute-Only** | `data-kit-attr:*` (ARIA, data-*, custom) | `setAttribute` only |

Canonical: `data-kit-bind:<prop>`. Dạng ngắn `data-kit-disabled` normalize về canonical.

---

## 6. Bảng Scope & Alias/Ref

### Cú pháp scope — object literal, ngoặc tuỳ chọn (KHÔNG parser thứ hai)

| Cách viết | Ngoặc | Parser mới? |
| :--- | :--: | :--: |
| `{ qty: 1, price: 250 }` | có | không |
| `qty: 1, price: 250` | không | **không** ← nới lỏng `primary()` |
| ~~`qty: 1; price: 250`~~ | không | **CÓ** (token-depth) → **bác** |

Cả hai dạng cho cùng `["{}", pairs]`. Dùng `,` (đã có trong grammar), **không** dùng `;` (buộc phải
viết bộ tách token thứ hai, hai lần, cho cả `kernel.js` và `compile.go`).

### Alias vs Ref — theo phân vai anh đã chốt

| | Trỏ tới | Loại | Truy cập |
| :--- | :--- | :--- | :--- |
| `data-kit-alias="$modal"` | Component **instance** | scope object | `$modal.open = true` |
| `data-kit-ref="search"` | **DOM element** | element | `$refs.search.focus()` |

> **Sửa so với master/ideaship:** bỏ `$modal === $refs.search`. Hai loại khác nhau → **không bao giờ
> bằng nhau**. Nhờ vậy phần lớn "collision rules" tự biến mất.

---

## 7. Bảng Danh sách — Source vs SSR Output

```
   HTML tác giả viết  ──►  Go Engine SSR  ──►  HTML browser nhận
```

| | Tác giả viết | Server render ra |
| :--- | :--- | :--- |
| Directive | `data-kit-for` + `data-kit-key` trên `<li>` mẫu | `<!--kit-for:start id=x-->` … `<!--kit-for:end-->` |
| Item | (không) | `data-kit-item="x"` + `data-kit-key` trên mỗi `<li>` |

**Ba luật bắt buộc:**
1. **Blueprint đến từ compiled server IR**, KHÔNG lấy item đầu đã hydrate (item đó có value/focus/state bẩn).
2. **Không mutate item object** để nhét metadata. Child scope là overlay: `item, index, count, first, last, even, odd`.
3. **Dùng chung key resolver với `morph`** (`morph.js:47` đã có keyed reconciliation). Không xây hai identity engine.

`data-kit-if` = mount/unmount, **cùng** Block Engine với `for`. `data-kit-show` = giữ DOM, đổi visibility.
Morph **tuyệt đối không gỡ** comment marker.

---

## 8. Bảng ĐÃ CHỐT vs CÒN TREO — mục quan trọng nhất

### ✅ Đã chốt — đóng băng được

HTML-first islands · một grammar/một AST · zero-eval + whitelist globals · `{{ }}` server / `data-kit-*`
client / **không `${}`** · **dirty-check giữ nguyên** · 7 biến hệ thống (bảng §3) · alias=instance /
ref=element (bảng §6) · scope object-literal ngoặc-tuỳ-chọn · modifier pipeline cố định (§4) · 3 nhóm
binding (§5) · `show`→`hidden` · Block Engine chung `if`+`for` + morph key (§7) · conformance
`walk≡eval` trong CI, nhắm **ghi-xuyên-biên** · core ≤ 12KB gzip CI-check.

### ⚠️ CÒN TREO — KHÔNG được đóng băng

| Vấn đề | Vì sao chưa chốt | Lựa chọn |
| :--- | :--- | :--- |
| **Async** (gốc rễ) | `async function` không chạy trên `eval.go` → §19 và §25 của ideashipping mâu thuẫn | **A** JS thật, ghi rõ "không twin" · **B** method đồng bộ, async qua `$app`+scope-patch (mẫu `api`/`live` đã có) · **C** twin chỉ cho expression. *Nghiêng B* — và bộ test tuân thủ (§11) đã CHỨNG MINH nhánh B khả thi: method IR-lambda mutate state chạy giống hệt trên `eval.go` (Go) và `walk` (JS). Server twin phủ được method. Còn phải chốt: async đẩy ra effect có đọc tự nhiên không |
| `$this` hay `$el` canonical | `$this` cần chú thích dài để khỏi bị hiểu là "thứ vừa bấm" | Giữ `$this` (kèm luật form dùng `$event.submitter`) HAY đổi `$el` |
| Phạm vi `$refs` | Hai modal cùng `ref="search"` sẽ đụng nếu global | Theo **component instance** (đề xuất) hay theo app |
| `$error` lan truyền | Lỗi component con có nổi lên cha? | Chưa định nghĩa = chưa phải boundary |

---

## 9. Bảng Giữ / Xoá / Hoãn

| | Việc | Điều kiện |
| :--- | :--- | :--- |
| **XOÁ** | 9.002 dòng `runtime*.js` (không được embed — code chết) | — |
| **XOÁ** | Hệ `action` (`lib/`, `behaviors`, `fire`, `compat.js`) | **effect trước** (copy/more thành effect) |
| **XOÁ** | Nhánh `typeof fn === "function"` (`kernel.js:310`) | sau khi chốt async |
| **XOÁ** | IR client `data-kitwork-*` (parser luôn ship) | — |
| **XOÁ** | `legacy/core/{lexer,ast,evaluate,reactive}` + 8 file `main copy*.js` | — |
| **GIỮ** | Inventory 18 năng lực (ideashipping §4): zero-eval, walk, blockedKey, scope, morph, remember, SSE, `$app`… | regression test **trước** khi sửa |
| **HOÃN** | Extension 1 Proxy dependency engine | **đe doạ server twin** — dùng read-set-từ-walk nếu cần |
| **HOÃN** | Extension 5 virtualization | chưa site nào có list 10.000 dòng + vỡ SSR |
| **HOÃN** | 6 build profile → chỉ `core` + `full` | cắt theo nhu cầu, không theo sơ đồ |
| **KHÔNG LÀM** | `switch/case`, `teleport`, transition framework, state manager mới | non-goals (ideashipping §31) |

---

## 10. Bảng Sự thật kỹ thuật (cho agent — kẻo sửa nhầm)

| Câu hỏi | Sự thật trong code | Nguồn |
| :--- | :--- | :--- |
| Source-of-truth ở đâu? | `kernel.js` + `modules/*` theo danh sách embed. **KHÔNG phải `runtime.js`** (nó là code chết) | `render.go`, `ARCHITECTURE.md` |
| `render()` cập nhật thế nào? | Quét lại toàn document mỗi tick (dirty-check) | `kernel.js:607` |
| Server và client lệch ở đâu? | Server scope **phẳng**, client scope **chuỗi lexical** → ghi-xuyên-biên | `eval.go:63` |
| `data-kit-show` hôm nay? | Đã `el.hidden` (đúng) | `kernel.js:610` |
| List client hôm nay? | **Chưa có `for`** — list lớn lên bằng fragment HTML (`more`) | grep `render()` |
| morph có sẵn key? | Có, `data-kitwork-key` | `morph.js:47` |
| Whitelist globals | 14 object: Math, Date, JSON, Number, String, Boolean, Array, Object.keys, parseInt/Float, encode/decodeURIComponent, isNaN, isFinite | extension §4 |

---

## 11. Việc đầu tiên — ĐÃ LÀM ✅

**Bộ test tuân thủ `walk` ≡ `eval`** đã dựng và chạy: `jit/hydrate/conformance_corpus.json` (14 ca dùng
chung) + `conformance_test.go` (hai runner: `Eval` Go / `walk` JS trong node, cả hai kiểm cùng một
`want`). `go test ./jit/hydrate -run TestConformance` → **14 ca xanh, client khớp server.**

Ba thứ nó chứng minh bằng thứ chạy được (không phải lập luận):

1. **Fork async giải được về phía B** — ca `count = 0; add = () => count = count + 1; add(); add(); count`
   → 2 chạy giống hệt trên `eval.go` và `walk`. Method IR-lambda mutate state **có server twin**.
2. **`walk ≡ eval` trên 14 ca**, gồm số+chuỗi (`'total ' + (qty*price)` → `"total 6"`, chỗ dễ lệch
   format) và **ghi-xuyên-biên** (`$.count = $.count + 1; $.count` → 42, chỗ `eval.go:63` khớp-bằng-tay).
   Cả hai đồng ý.
3. **Test đỏ được vì lý do đúng** — lật `want` thì cả hai runner độc lập báo cùng giá trị đã tính,
   tức đỏ đó tự nó chứng minh hai engine tính giống nhau.

Đây là lưới. Mọi bước sau (bỏ IR client, rename sâu, tách vai khỏi kernel) giờ **chứng minh được là
không đổi ngữ nghĩa**: chạy suite này, còn xanh thì đúng. Và nó là tiền đề bắt buộc cho capsule.

---

> **Một câu:** runtime này cần **một directive** (`for`, cưỡi morph), **một bộ test** (tuân thủ),
> **một cơ chế** (effect), và **một chuỗi phép trừ** để tách 4 vai khỏi kernel. Không bước nào phát
> minh cơ chế mới — câu trả lời đã nằm trong code, việc còn lại là chốt 4 quyết định treo ở §8 rồi ráp.
