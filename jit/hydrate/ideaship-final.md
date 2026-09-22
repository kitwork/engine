# KitJS V2 — Bảng quy chuẩn hợp nhất (FINAL)

> **Đây là cửa chính.** Nó hợp nhất 7 tài liệu thành một bảng tra cứu, giải các chỗ chúng nói khác
> nhau, và **tách rõ cái ĐÃ CHỐT với cái CÒN TREO** — vì một quyết định chưa chốt mà bị đóng băng sẽ
> thành lỗi cho người triển khai.
>
> **Cập nhật 22/09/2026:** §2–§10 viết lại theo đúng những gì đã chạy sau đợt A (`ideaship-ledger.md`
> ghi từng mục và test). Cái mới so với bản 01/08: `data-kit-seed` (§2.2/§5), `data-kit-element`/`$element`
> thay `ref`/`$refs` (§3/§6), bảng attribute ngoài directive (§2.6), và §8 giờ là **10 câu hỏi B** có đề xuất.
>
> **Cùng ngày, chiều:** hệ `action` jitjs đã **trừ** (§9) — không có `effect`. Hành vi là component hoặc biểu
> thức, vận chuyển là Drive; ghi state ngoài một lượt vẽ (timer, `.then`, callback) tự xếp một lượt vẽ (§8 B10).
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

## 2. Bộ directive — đầy đủ, như đang chạy (22/09/2026)

> Bảng này thay bảng "14 Directive" cũ. Cột **Trạng thái**: ✅ chạy ở cả kernel `/kit.js` lẫn runtime
> component (+ scanner Go) · ◐ chỉ một bên (có lý do ghi tại chỗ). Chi tiết thi hành từng mục:
> `ideaship-ledger.md`.

### 2.1 Vùng & danh tính

| Attribute | Nghĩa | Ví dụ | Trạng thái |
| :--- | :--- | :--- | :--- |
| `data-kit-scope` | Mở một **vùng** với state khởi đầu; trên host component = state khởi đầu của component | `="qty: 1, price: 250"` · `="{ qty: 1 }"` | ✅ — object literal, ngoặc tuỳ chọn, dấu `,` (§6); attribute rỗng = vùng rỗng. Dạng tên trần `="cart"` và init `="a = 1; b = 2"` của kernel **đã bỏ** (B8, 22/09) |
| `data-kit-component` | Hành vi có tên, tái dùng | `="modal@1.2.0"` | ✅ — runtime component bắt buộc version chính xác; kernel chỉ cần tên |
| `data-kit-alias` | Tay cầm **thể hiện** component, toàn cục, một thể hiện một tên | `="$paymentModal"` | ✅ — runtime component: chỉ trong action; kernel: mọi biểu thức |
| `data-kit-element` | Phần tử **được đặt tên** trong vùng của nó | `="search"` | ✅ — `$element.search` (§3); code `context.element("search")` / `context.elements("slide")` (§6). Trước 21/09 là `data-kit-ref` |

### 2.2 Dữ liệu ↔ DOM — ba chiều, ba tên

| Attribute | Chiều | Ví dụ | Trạng thái |
| :--- | :--- | :--- | :--- |
| `data-kit-seed[:<name>]` | DOM → state, **một lần** (boot; vùng mới sau Drive swap) | `="title"` · `:value="user.email"` · `="tags[]"` | ✅ — vế phải là **đích state** (key · `a.b.c` · `list[]`), không phải biểu thức. Đọc text / JSON của `<script type="application/json">` / property-attribute theo 3 nhóm ngược (§5). DOM thắng scope literal. Server đọc cùng seed cho first paint |
| `data-kit-bind:<name>` | state → DOM, **mỗi lượt render** | `:disabled="loading"` · `:aria-expanded="open"` | ✅ — tên quyết định nhóm (§5). Một dạng duy nhất; `data-kit-attr` **không có** (Quốc, 21/09) |
| `data-kit-model` | Hai chiều, cho ô nhập | `="user.name"` | ✅ — = `seed:value` + `bind:value` + lắng `input`; số cho `number/range`; `data-kit-debounce="ms"` là bạn đồng hành duy nhất của nó |

### 2.3 Nội dung & hiển thị — bốn tên riêng (không đưa vào `bind`)

| Attribute | Ghi vào | Trạng thái |
| :--- | :--- | :--- |
| `data-kit-text` | `textContent` — server bake giữa hai thẻ | ✅ — **không** `${}`; `bind:text` bác vì `text` không phải property name |
| `data-kit-show` | `element.hidden = !value` (giữ Flex/Grid) | ✅ |
| `data-kit-class` | **Gộp** vào class tĩnh; string / object / array / ternary; tên viết đủ để jitcss thấy | ✅ |
| `data-kit-style` | Style declarations `a: x; b: y` | ◐ runtime component; kernel thêm khi có ca dùng (B7, 22/09) |

### 2.4 Cấu trúc & lỗi

| Attribute | Nghĩa | Trạng thái |
| :--- | :--- | :--- |
| `data-kit-for` + `data-kit-key` | Danh sách, viết **trên chính hàng**; hàng là blueprint, rời tài liệu; overlay `count first last even odd` lexical theo hàng (§7) | ✅ client (`<template>` vẫn được); server materialize **hoãn** tới khi có site cần (B6, 22/09) |
| `data-kit-if` | Mount / unmount subtree | ✅ |
| `data-kit-item` | Chỉ do máy ghi trên hàng đã materialize; kernel bọc list `<!--kit-for:start id=fN-->`…`<!--kit-for:end-->` | ✅ hình dây; server chưa phát (B6: hoãn) |
| `data-kit-error` | Error boundary: lỗi action/binding bên trong → chạy biểu thức với `$error`; không lan quá boundary đầu; không boundary → console | ✅ chốt 22/09: không lan quá boundary đầu; handler tự ném → console, không tái nhập; `$error.recover()` không có (B4) |

### 2.5 Sự kiện — một họ, một đường ống

`data-kit-<event>[:modifier…]="expr"` cho `click dblclick submit input change keydown keyup pointerdown pointerup focusin focusout`. Modifier viết thứ tự nào cũng được, chạy theo §4. ✅ cả hai bên; kernel đã gỡ `data-kit-away` / `data-kit-escape` / `data-kit-guard` (→ `click:outside`, `keydown:escape:window`, `:prevent`).

### 2.6 Ngoài bảng directive — có thật, đang chạy, spec phải nhìn thấy

| Nhóm | Attribute | Ghi chú |
| :--- | :--- | :--- |
| directive kernel | `data-kit-validate="expr"` → `data-state="valid\|invalid"` | chỉ kernel; gate submit |
| capability (`jit/js/capabilities`) | `data-kit-api` `data-kit-live` `data-kit-remember` `data-kit-drag` / `data-kit-no-drag` | khai báo, không phải biểu thức |
| Drive / morph | `data-kit-drive` `data-kit-retain` `data-kit-ignore` | chỉ dẫn vận chuyển |
| server-only | `data-kit-highlight` | JIT highlight tiêu thụ khi render |
| neo gốc | `data-kit-app` / `data-kit-hydrate` (hoặc `data-kitwork-`) | opt-in hydrate; nơi duy nhất cho phép tiền tố dài do tác giả viết |

`data-kitwork-*` là **của máy**: `-jit` (asset inject), `-hash/-plan/-runtime/-handoff` (staged delivery), `-ui` (overlay kernel: bar, announcer, toast), `-highlight`. Trên một directive hay boundary nó **trơ** — kernel không đọc, không giải mã IR (21/09, §9). `data-kit-action`/`data-kit-target`/`data-kit-trigger` không còn là attribute (22/09): jit/js chỉ còn component (`data-kit-component`, `jit/js/components`) và capability.

---

## 3. Bảng 7 Biến hệ thống — tất cả số ít, mỗi chữ một loại

| Biến | Là gì | Loại | Ghi chú |
| :--- | :--- | :--- | :--- |
| `$this` | Thẻ **sở hữu directive** đang chạy | Element | không phải `event.currentTarget`; form → dùng `$event.submitter` |
| `$host` | Thẻ **vùng gần nhất** (host component / `data-kit-scope` / hàng `for`), không có → `<html>` | Element | không đổi thành `$component` — vùng không phải lúc nào cũng là component |
| `$element.<name>` | Thẻ **được đặt tên** `data-kit-element="<name>"` trong vùng | Element | phạm vi chốt 22/09 (B3): vùng gần nhất; thiếu → nullish (`$element.x?.focus()`); trùng → cái đầu; host lồng không thấy nhau |
| `$event` | Sự kiện của handler đang chạy | Event | kernel: native thật; runtime component: **native-shaped** — ảnh chụp + `target` `submitter` `relatedTarget` thật, vì ngữ pháp đóng không đọc object native (B2, 22/09) |
| `$error` | `{ cause, message, directive, element }` | Object | chỉ trong `data-kit-error` |
| `$app` | Cầu capability (theme, clipboard, camera…) | Bridge | |
| `$` | **Root state** của trang | Object | |

**`$el` / `$root` đã bỏ** (B1, 22/09): không còn resolve ở cả hai runtime; tên vẫn bị chặn để không ai đặt alias trùng. 0 site dùng lúc bỏ.
**Dành sẵn, chưa cấp**: `$component` = thẻ host component gần nhất (bỏ qua scope con) — thêm khi có ca dùng; tên đã được giữ ở cả hai runtime.

Runtime component có **ngữ pháp đóng**: phần tử (từ `$this` `$host` `$element` `$event.target`) chỉ trả lời một bảng đọc (`value checked open id dataset scrollTop…`) và động từ (`focus blur click select scrollIntoView showModal close reportValidity play getAttribute…`), **không ghi**, không đi cây. Kernel đưa phần tử thô.

---

## 4. Bảng Đường ống Modifier — thứ tự CỐ ĐỊNH

Tác giả viết thứ tự nào cũng được; runtime luôn chạy theo:

| # | Nhóm | Modifier | Việc |
| :-: | :--- | :--- | :--- |
| 1 | Target | `:window` `:document` | Nghe ở đâu — không cần focus (Escape trên `<div>`) |
| 2 | **Filter** | `:outside` `:escape` `:enter` `:self` | Điều kiện (`:self` = event rơi đúng lên thẻ, không phải con). FAIL → **dừng, event KHÔNG bị nuốt** |
| 3 | Prevent | `:prevent` | `preventDefault()` |
| 4 | Stop | `:stop` | `stopPropagation()` — kết thúc việc leo lên tổ tiên |
| 5 | Timing | `:debounce(n)` \| `:throttle(n)` | Hoãn tới lúc lặng / chạy mép đầu rồi nghỉ; một handler một trong hai |
| 6 | Lifecycle | `:once` | Chạy 1 lần rồi tự gỡ |
| 7 | Execute | | Chạy biểu thức |

Ràng buộc: `:escape/:enter` chỉ `keydown/keyup`, không cùng lúc; `:outside` chỉ `click dblclick pointerdown pointerup focusin`; `:window`/`:document` một trong hai; `:self` không đi với `:outside`/`:window`/`:document`; `n` = 1–60000 ms. Server (`render.go`) và scanner báo lỗi tại render; kernel vô hiệu handler sai thay vì bắn nhầm. ✅ 21/09; `:self` vào cả hai runtime 22/09 (B5).

---

## 5. Bảng Binding 3 nhóm — và `seed` là gương của nó

| Nhóm | Tên | `bind:<name>` ghi | `seed:<name>` đọc |
| :--- | :--- | :--- | :--- |
| **Reflected Boolean** | `disabled required readonly multiple hidden open` | property **+** attribute | boolean |
| **Live State Property** | `checked selected value indeterminate` | property **only** (attribute giữ giá trị reset) | boolean / string (số với input `number`/`range`) |
| **Attribute** | `aria-*`, `data-*`, mọi tên khác | attribute; aria ghi `"true"/"false"`; boolean khác có/không | string; vắng → `null` |

Cấm ở cả hai chiều: `on*`, `data-kit*`, `srcdoc`, `style`, `innerhtml`.
`data-kit-seed="key"` (không tên) đọc `textContent` (trim); trên `<script type="application/json">` đọc JSON. Đích: `key` · `a.b.c` (tự tạo object) · `list[]` (một phần tử một mục, theo thứ tự tài liệu, dựng lại khi có thành viên mới; **không** `items[1]`, không mảng trong attribute — cấu trúc để ở đảo JSON hoặc `for`). Một lần. DOM thắng scope literal. Không đoán kiểu. Host component: field phải khai báo. ✅ 21/09 cả server.

---

## 6. Bảng Scope, Alias, Element

### Cú pháp scope — object literal, ngoặc tuỳ chọn (KHÔNG parser thứ hai)

| Cách viết | Ngoặc | Parser mới? |
| :--- | :--: | :--: |
| `{ qty: 1, price: 250 }` | có | không |
| `qty: 1, price: 250` | không | **không** — bọc `{}` rồi vào cùng parser |
| ~~`qty: 1; price: 250`~~ | không | **bác** — báo lỗi rõ `separated by "," not ";"` |

Runtime component giữ parser dữ liệu thuần (giá trị phải là data, không biểu thức) — hẹp hơn kernel, cùng ngữ pháp. ✅ 21/09.

### Alias vs Element — hai loại, không bao giờ bằng nhau

| | Trỏ tới | Loại | Truy cập |
| :--- | :--- | :--- | :--- |
| `data-kit-alias="$modal"` | Component **instance** | scope object | `$modal.open()` — toàn cục, một thể hiện một tên |
| `data-kit-element="track"` | **DOM element** | element | biểu thức `$element.track` · code `context.element("track")` (cái đầu) / `context.elements("slide")` (danh sách) — cùng bộ lọc `owned()` |

`element` thay `ref` (21/09): `ref` là chữ viết tắt; số ít như 6 biến còn lại; **một** cơ chế đặt tên cho cả biểu thức lẫn code component (thay 20 marker tự chế `data-carousel-track`… của catalogue, chuyển dần).

### Cha–con: không `state`/`props` (chốt 21/09)

Lệnh xuyên vùng = `$alias` trong action. Dữ liệu: kernel có scope chuỗi lexical (đọc rơi lên, ghi về chủ sở hữu); runtime component cố ý cô lập. Liên kết cha–con vì thế là câu **một kernel** (§8), không phải một directive mới. Con tái dùng nhiều thể hiện phải **tự đủ** (seed của mình, DOM của mình); cái con "cần biết thêm" thì server template đã bake vào markup.

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
2. **Không mutate item object** để nhét metadata. Child scope là overlay: `item, index, count, first, last, even, odd` — năm từ overlay **lexical theo hàng**, không lọt vào host component lồng trong hàng (host giữ `count` riêng); tên item/index tác giả đặt thì lọt (21/09).
3. **Dùng chung key resolver với `morph`**. Không xây hai identity engine.

**Trạng thái 22/09**: nửa **client** xong ở cả hai runtime (thẻ thường là blueprint; overlay đủ 7 tên; kernel phát đúng hình dây `kit-for:start/end` + `data-kit-item`). Nửa **server** hoãn (B6, 22/09): `PreRender` chưa có nguồn dữ liệu list (scope server chỉ từ `data-kit-model` + `data-kit-seed`); list SSR hôm nay là `{{ for }}` của template engine. Làm khi có site cần; client `for` = ca reactive, không SEO-trọng.

`data-kit-if` = mount/unmount, **cùng** Block Engine với `for`. `data-kit-show` = giữ DOM, đổi visibility. Morph **tuyệt đối không gỡ** comment marker.

---

## 8. Bảng ĐÃ CHỐT vs CÒN TREO — mục quan trọng nhất

### ✅ Đã chốt — đóng băng được (và đã chạy, ledger 21–22/09)

HTML-first islands · một grammar/một AST · zero-eval + whitelist globals · `{{ }}` server / `data-kit-*` client / **không `${}`** · **dirty-check giữ nguyên** · 7 biến hệ thống số ít (§3) · alias = instance / element = phần tử có tên (§6) · scope object-literal ngoặc-tuỳ-chọn dấu `,` · modifier pipeline cố định (§4) · `bind:<name>` một dạng, 3 nhóm · **`seed` gương của bind** · `model` = seed + bind + input · `text show class style` giữ tên riêng · `show`→`hidden` · `for` trên thẻ thường + overlay 7 tên lexical · `data-kit-error` boundary gần nhất · Block Engine chung `if`+`for` + morph key · conformance `walk≡eval` trong CI · core ≤ 12 KiB gzip (đo bản minified production, CI) · `data-kitwork-*` trơ trên directive · không `state`/`props`.

### ✅ 10 câu B — Quốc chốt 22/09 ("vậy làm đi" = theo cột đề xuất)

| # | Câu | Chốt | Thi hành |
| :-: | :--- | :--- | :--- |
| B1 | `$el`/`$root` | **bỏ** | ✅ kernel + runtime component không resolve nữa; tên giữ trong danh sách chặn |
| B2 | `$event` ở runtime component | **native-shaped**: ảnh chụp + `target` `submitter` `relatedTarget` thật; native thật ở kernel | ✅ chữ trong §3 |
| B3 | phạm vi `$element` | **vùng gần nhất**, host lồng không thấy nhau, trùng → cái đầu, thiếu → nullish | ✅ đang chạy |
| B4 | `$error.recover()` | **không có** tới khi có ca dùng | ✅ không còn trong spec |
| B5 | `:self` | **vào nhóm Filter** §4 | ✅ kernel thêm `:self` (+ ràng buộc), runtime component đã có |
| B6 | server materialize `for` | **hoãn** tới khi có site cần | ✅ ghi §7 |
| B7 | `data-kit-style` | giữ ở runtime component; kernel **thêm khi có ca** | ✅ ghi §2.3 |
| B8 | dạng tên trần / init của `data-kit-scope` | **cắt** — một literal, một parser; attribute rỗng = vùng rỗng | ✅ kernel; 0 site dùng |
| B9 | **một kernel hay hai runtime** | **chính sách**: mọi ngữ pháp mới chỉ vào kernel; runtime component chỉ *nhận* qua cùng ngữ pháp, không tự mọc; gộp thành mảnh lazy là việc dài hơi, làm theo chuỗi phép trừ §9 | ✅ chính sách; chưa có bước code |
| B10 | async | **nhánh B**: biểu thức đồng bộ; async sống trong **component** (method JS thật) hoặc **Drive**; suite tuân thủ đã chứng minh method IR-lambda có twin | ✅ 22/09: kernel — method trả Promise → vẽ lại khi settle (đã có); **ghi scope ngoài một lượt vẽ** (timer, `.then` không trả, callback) → xếp một lượt vẽ gộp; trong lượt (handler, model, render) không xếp thêm. Không có `effect`. Test: `write_repaint_test.go`, `jit/js/migration_browser_test.go` |

> Khi B9 cần đảo lại (giữ hai runtime độc lập), nói một câu là đủ — chưa có code nào phụ thuộc vào nó ngoài việc *không* thêm ngữ pháp riêng cho runtime component từ nay.

## 9. Bảng Giữ / Xoá / Hoãn — chuỗi phép trừ

| | Việc | Điều kiện | Trạng thái |
| :--- | :--- | :--- | :--- |
| **XOÁ** | IR client `data-kitwork-<directive>` + read-alias tiền tố dài cho directive/boundary | — | ✅ 21/09 (C1) |
| **XOÁ** | `data-kit-away` `data-kit-escape` `data-kit-guard` (→ modifier) | — | ✅ 21/09 |
| **XOÁ** | `data-kit-as`, đuôi `name=$alias`, `data-kitwork-alias`, `data-alias` (→ `data-kit-alias`) | — | ✅ 21/09 |
| **XOÁ** | `data-kitwork-key` trong `morph.js` (`jit/js/lib/more.js` đã xoá cùng hệ action) | key là của tác giả: `data-kit-key` (chữ của `for`) hoặc `data-key` (site apptop viết tay); tiền tố máy không ai phát | ✅ 22/09 — test `morph_key_test.go` (key tác giả DỜI node, `data-kitwork-key` trơ = khớp theo vị trí; có disable-check) |
| **XOÁ** | Hệ `action` jitjs (`lib/`, `behaviors`, `fire`, `compat.js`, `data-kit(work)-action/target`, `data-kit-trigger`) | không cần `effect`: 23 file site chuyển sang component/biểu thức/Drive; `more` 0 chỗ dùng | ✅ 22/09 |
| ~~XOÁ~~ **GIỮ** | Nhánh `typeof fn === "function"` (kernel call) | B10 chốt async sống trong **method JS thật** của component — nhánh này chính là chỗ method đó chạy (`fn.apply(scope)`); dòng XOÁ viết khi còn nghĩ async đi qua `$app` + scope-patch | ✅ 22/09 lật thành GIỮ |
| **XOÁ** | 9.002 dòng `runtime*.js`, `legacy/core/*`, `main copy*.js` | — | ✅ đã không còn (kiểm 22/09: `find` toàn `jit/` không ra file nào, thư mục `legacy` không tồn tại) |
| **GIỮ** | Inventory 18 năng lực (ideashipping §4) | regression test **trước** khi sửa | — |
| **HOÃN** | Extension 1 Proxy dependency engine | **đe doạ server twin** | — |
| **HOÃN** | Extension 5 virtualization | chưa site nào cần | — |
| **HOÃN** | 6 build profile → chỉ `core` + `full` | cắt theo nhu cầu | ✅ đã là 2: `assemble.go` chỉ còn `kit` + `hydrate` (kiểm 22/09) |
| **KHÔNG LÀM** | `switch/case`, `teleport`, transition framework, state manager mới, `state`/`props` | non-goals | — |

---

## 10. Bảng Sự thật kỹ thuật (cho agent — kẻo sửa nhầm) — cập nhật 22/09

| Câu hỏi | Sự thật trong code | Nguồn |
| :--- | :--- | :--- |
| Source-of-truth ở đâu? | `kernel.js` + `modules/*` theo danh sách embed trong `Runtime()` | `render.go` |
| `render()` cập nhật thế nào? | Quét lại toàn document mỗi tick (dirty-check): `seedElements` → `seedModels` → for/if → text/show/bind/class/validate | `kernel.js` `render()` |
| Server và client lệch ở đâu? | Server scope **phẳng**, client scope **chuỗi lexical** → ghi-xuyên-biên (corpus có ca) | `eval.go` |
| Server biết state gì lúc render? | `data-kit-model` `value=` + **`data-kit-seed`** ngoài boundary; không biết list | `prerender.go`, `prerender_seed.go` |
| List client hôm nay? | **Có `for`** ở cả hai runtime, keyed, overlay 7 tên; server chưa materialize | `kernel.js` `renderFor`, `structure.js` |
| morph có sẵn key? | Có: `data-kit-key` (và `data-kitwork-key` chờ trừ) | `morph.js` |
| Hai runtime khác gì? | Kernel: evaluator mở, phần tử thô, scope lexical. Runtime component: ngữ pháp đóng (own-property + bảng method/element), scope cô lập, `$alias` action-only, closed graph có version | `evaluator.js`, `kernel.js` |
| Kích thước | kernel minified gzip ≈ 10.1 KB / 12 KiB (gate `size_budget_test.go`); `/kit.js` production đi qua `utilities/minifier.JS` | `work/jithydrate.go` |
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

> **Một câu (01/08, giữ làm sử):** runtime này cần **một directive** (`for`, cưỡi morph), **một bộ test** (tuân thủ),
> ~~**một cơ chế** (effect)~~, và **một chuỗi phép trừ** để tách 4 vai khỏi kernel. Không bước nào phát
> minh cơ chế mới — câu trả lời đã nằm trong code, việc còn lại là chốt 4 quyết định treo ở §8 rồi ráp.

> **Một câu, 22/09:** `for` đã cưỡi morph, bộ test tuân thủ đã chạy, ngữ pháp đã về một bộ (§2), hệ jitjs đã trừ **không cần effect** (component-first: hành vi = component hoặc biểu thức, vận chuyển = Drive). Còn lại một việc lớn: **câu B9** — một kernel hay hai runtime — vì mọi ngữ pháp thêm sau này sẽ trả giá đôi cho tới khi nó được chốt.
