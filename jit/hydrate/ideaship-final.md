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
| `data-kit-component` | Hành vi có tên, tái dùng | `="modal@1.2.0"` | ✅ — tên trần là dạng để viết (catalogue mới nhất, hoặc `_components/<tên>.js` của site). Ghim version chỉ để giữ một hợp đồng đứng yên (`dialog@v1.0.0`); component của site không có version để ghim |
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
| `data-kit-style:<property>` | MỘT thuộc tính một attribute, giá trị là MỘT biểu thức | ✅ kernel 23/09 (B7 đóng): cùng hình với `bind`/`seed` — đích nằm trong TÊN attribute. Kết quả nullish/false/rỗng → **trả lại đúng giá trị tác giả viết trong `style`**. Tên: thuộc tính CSS thường, hoặc custom `--x` (cấm `--kit-`/`--kitwork-`); cấm `css-text`/`csstext`/`behavior`/`-moz-binding`. Giá trị cấm: ký tự điều khiển, `;{}\@`, comment, `!important`, `url()`/`image-set()`/`src()`/`expression()`/`attr()`, `javascript:`/`vbscript:`/`data:text/html` — **`var()` ĐƯỢC PHÉP** (màu của site sống trong custom property). Runtime component giữ dạng list cũ cho tới khi bị trừ |

### 2.4 Cấu trúc & lỗi

| Attribute | Nghĩa | Trạng thái |
| :--- | :--- | :--- |
| `data-kit-for` + `data-kit-key` | Danh sách, viết **trên chính hàng**; hàng là blueprint, rời tài liệu; overlay `count first last even odd` lexical theo hàng (§7) | ✅ client (`<template>` vẫn được); server materialize **hoãn** tới khi có site cần (B6, 22/09) |
| `data-kit-if` | Mount / unmount subtree | ✅ |
| `data-kit-item` | Máy ghi trên hàng đã materialize, nhưng nó LÀ một boundary (nằm trong `SCOPE` của kernel cùng `scope`/`component`/`api`) nên mang tiền tố của ngữ pháp — xem quy ước ở §2.6; kernel bọc list `<!--kit-for:start id=fN-->`…`<!--kit-for:end-->` | ✅ hình dây; server chưa phát (B6: hoãn) |
| ~~`data-kit-error`~~ | ~~Error boundary với `$error`~~ | **XOÁ 02/10** (Quốc chốt): 0 site, 0 app viết — chỉ trang tài liệu kitjs.org. Lỗi directive (action, binding, init, `context.require`) giờ ra `console.error(error, directive, element)`. Lỗi người dùng cần thấy = promise của action bị từ chối → `error` trong scope (giữ); lỗi lập trình = việc của verify pass. Kernel nhẹ 84 byte gzip |

### 2.5 Sự kiện — một họ, một đường ống

`data-kit-<event>[:modifier…]="expr"` cho `click dblclick submit input change keydown keyup pointerdown pointerup focusin focusout`; dạng đầy đủ `data-kit-event:<tên>[:modifier…]` (02/10) nghe MỌI sự kiện theo tên — sự kiện component phát bằng `context.emit` (dữ liệu ở `$event.detail`) hoặc sự kiện DOM ngoài danh sách (`data-kit-event:paste`); dạng ngắn giữ làm viết tắt. **`data-kit-intersect`** (02/10, Quốc chốt): chạy biểu thức khi phần tử vào màn hình, `:leave` khi ở ngoài (cả lần báo đầu), `:once`, `:half`/`:full`, `:margin(x)` (một giá trị — tên thuộc tính không chứa dấu cách); kernel chỉ biết TÊN, observer là file `/kit/directives/intersect.<hash>.js` chỉ giao cho trang viết nó; thay component `visible`. Modifier viết thứ tự nào cũng được, chạy theo §4. Tổ hợp phím viết ngay trong tên: `data-kit-keydown:mod+k` — `mod` = Ctrl (Win/Linux) hoặc Cmd (macOS), ĐÚNG một trong hai, và mọi modifier không nêu phải TẮT. ✅ cả hai bên; kernel đã gỡ `data-kit-away` / `data-kit-escape` / `data-kit-guard` (→ `click:outside`, `keydown:escape:window`, `:prevent`).

### 2.6 Ngoài bảng directive — có thật, đang chạy, spec phải nhìn thấy

| Nhóm | Attribute | Ghi chú |
| :--- | :--- | :--- |
| ~~directive kernel~~ | ~~`data-kit-validate="expr"`~~ | **XOÁ 22/09** (Quốc chốt B): 0 site viết; `required`/`pattern`/`type` cho phản hồi sống sẵn có. **Cổng submit GIỮ** — kernel chỉ ĐỌC `data-state="invalid"` (server render, component, hay constraint validation ghi). **Nửa server GIỮ**: `ctx.validate(rule)` (`work/validate.go`) — trình duyệt nói gì cũng được, phán quyết ở server |
| ~~capability (`jit/js/capabilities`)~~ | ~~`data-kit-api` `data-kit-live` `data-kit-remember`~~ | **XOÁ 02/10**: 0 dùng api/live; remember chỉ buildinpublic.guide → thành component riêng của site (`_components/checklist.js`) |
| file directive (`jit/js/directives/drag.js`) | `data-kit-drag="true"` / `="false"` | khai báo, không phải biểu thức. **02/10 rời kernel**: chỉ thanh tiêu đề của cửa sổ desktop dùng, nên giờ là file `/kit/directives/drag.<hash>.js` chỉ giao cho trang viết nó (trang chỉ có `data-kit-drag` vẫn được kernel đi kèm) |
| ~~route~~ | ~~`/kit.js?components=…`~~ | **XOÁ 03/10**: không trang nào còn xin (mọi trang nhận file `/kit/<phần>.<hash>.js` qua `Deliver`), và nó không mang file directive (intersect, drag) nên trang nào lỡ đi đường này sẽ hỏng im lặng. `hydrate.Render` thành `hydrate.Check` — chỉ kiểm biểu thức, không chèn thẻ nữa. Cùng ngày `RuntimeFor`/`RuntimeJS`/`RuntimePath` cũng gỡ: test trình duyệt nạp đúng các file `/kit/…` (`deliverTestPage`), và lộ ra test sidebar xanh sai từ 23/09 (đọc chuỗi ghép, chữ `open:` là của kernel) trong khi nút đóng menu mobile của dashboard huynhnhanquoc.com gọi `close()` không tồn tại |
| ~~kernel~~ | ~~`data-kit-progress`~~ · ~~`kit.fetchWithRetry`~~ | **XOÁ 02/10**: 0 nơi dùng. Màu thanh tiến trình của Drive = `--kitwork-progress` trong CSS (kitwork.io đang dùng cách này) |
| ~~tải component từ CDN~~ | ~~`data-kit-indexed`~~ | **XOÁ 24/09**: `modules/component-loader.js` (4 386 byte, ship cho MỌI trang) tải component theo `kit.cdnComponents` — mặc định rỗng và **0 site nào set**, nên nó chưa từng tải được gì; `data-kit-indexed` chỉ bật cache IndexedDB cho chính nó. Host mang tên component không ai đăng ký giờ **báo lỗi có tên, đúng một lần** thay vì ngồi im chờ một fetch không bao giờ đến |
| Drive | `data-kit-reload="true"` | **24/09**: "nạp trang này theo cách thường" — đọc trên chính thẻ `<a>`/`<form>` GET hoặc trên tổ tiên. Là dạng viết DUY NHẤT: `data-kit-drive="false"` (29 chỗ) chưa từng được runtime sống đọc, còn `data-kitwork-hydrate="false"` (10 chỗ) chạy được nhưng bắt tác giả gõ từ của máy. `data-kit-retain` / `data-kit-ignore` **không tồn tại** — morph chỉ đọc `data-kit-key`; tên đã chốt cho việc "giữ nguyên vùng này" là `data-kit-keep="true"`, CHƯA CÀI vì 0 ca dùng thật trên kernel |
| server-only | `data-kit-highlight` | JIT highlight tiêu thụ khi render |

**Neo gốc đã BIẾN MẤT (24/09).** Không còn `data-kit-app` / `data-kit-hydrate` / dạng `data-kitwork-`. Một trang là trang Kitwork vì **nó dùng ngữ pháp** — một directive tác giả viết là bằng chứng; trang chỉ IN ngữ pháp trong `<pre>`/`<code>` thì được trả về nguyên xi. Số đo lúc chốt: toàn repo 3 file phụ thuộc phân biệt ví-dụ/dùng-thật, 157 trang dùng thật mà chưa từng được soát vì thiếu neo.

**Công tắc nằm ở router, không nằm trong markup:**

| Công tắc | Mặc định | Nghĩa |
| :--- | :--- | :--- |
| `router.drive()` | TẮT | Điều hướng mượt (fetch + morph). Engine tự đánh dấu `data-kitwork-drive` trên thẻ runtime của chính nó; markup không mang gì |
| `router.javascript({ prerender: true })` | TẮT | Server tính sẵn `text`/`show` ở page scope rồi ghi vào HTML. Tốn một lượt quét mỗi response, chỉ với tới page scope (đo: đổi 3/105 trang) — **không khuyến khích**; viết `hidden` vào markup rẻ hơn |
| `ALLOW_LOCAL` | — | Máy soát lúc render (compile, modifier, tên thuộc tính `style:`, đích `seed`, tên-không-ai-khai) chỉ in khi dev |

`data-kit-action`/`data-kit-target`/`data-kit-trigger` không còn là attribute (22/09): jit/js chỉ còn component (`data-kit-component`, `jit/js/components`) và capability.

**QUY ƯỚC TIỀN TỐ — viết lại 24/09.** `data-kit-*` = **NGỮ PHÁP**, kể cả khi chính máy ghi ra một mục của
ngữ pháp (`data-kit-item` trên hàng đã materialize — nó là boundary thật, nằm trong `SCOPE` của kernel cùng
`scope`/`component`/`api`). Phép thử là attribute ấy **nghĩa là gì**, không phải ai gõ nó. `data-kitwork-*` =
**VẬN CHUYỂN**: `-jit` (asset inject), `-drive` (câu trả lời của `router.drive()`), `-ui` (overlay kernel: bar,
announcer, toast), `-layout`, `-hash/-plan/-runtime/-handoff` (staged delivery), `-highlight`. Tác giả không bao
giờ viết một cái `data-kitwork-*`, và kernel không bao giờ đọc một cái như directive.

---

## 3. Bảng 7 Biến hệ thống — tất cả số ít, mỗi chữ một loại

| Biến | Là gì | Loại | Ghi chú |
| :--- | :--- | :--- | :--- |
| `$this` | Thẻ **sở hữu directive** đang chạy | Element | không phải `event.currentTarget`; form → dùng `$event.submitter` |
| `$host` | Thẻ **vùng gần nhất** (host component / `data-kit-scope` / hàng `for`), không có → `<html>` | Element | không đổi thành `$component` — vùng không phải lúc nào cũng là component |
| `$element.<name>` | Thẻ **được đặt tên** `data-kit-element="<name>"` trong vùng | Element | phạm vi chốt 22/09 (B3): vùng gần nhất; thiếu → nullish (`$element.x?.focus()`); trùng → cái đầu; host lồng không thấy nhau |
| `$event` | Sự kiện của handler đang chạy | Event | kernel: native thật; runtime component: **native-shaped** — ảnh chụp + `target` `submitter` `relatedTarget` thật, vì ngữ pháp đóng không đọc object native (B2, 22/09) |
| ~~`$error`~~ | ~~`{ cause, message, directive, element }`~~ | ~~Object~~ | **XOÁ 02/10** cùng `data-kit-error` |
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
| 2 | **Filter** | `:outside` `:escape` `:enter` `:self` `:<tổ hợp phím>` | Điều kiện (`:self` = event rơi đúng lên thẻ, không phải con). FAIL → **dừng, event KHÔNG bị nuốt** |
| 3 | Prevent | `:prevent` | `preventDefault()` |
| 4 | Stop | `:stop` | `stopPropagation()` — kết thúc việc leo lên tổ tiên |
| 5 | Timing | `:debounce(n)` \| `:throttle(n)` | Hoãn tới lúc lặng / chạy mép đầu rồi nghỉ; một handler một trong hai |
| 6 | Lifecycle | `:once` | Chạy 1 lần rồi tự gỡ |
| 7 | Execute | | Chạy biểu thức |

Ràng buộc: `:escape/:enter` chỉ `keydown/keyup`, không cùng lúc; `:outside` chỉ `click dblclick pointerdown pointerup focusin`; `:window`/`:document` một trong hai; `:self` không đi với `:outside`/`:window`/`:document`; `n` = 1–60000 ms. Server (`render.go`) và scanner báo lỗi tại render; kernel vô hiệu handler sai thay vì bắn nhầm. ✅ 21/09; `:self` vào cả hai runtime 22/09 (B5).

**Tổ hợp phím (23/09)** — `data-kit-keydown:mod+k="open()"`. `mod` = Control trên Windows/Linux, Command trên macOS, **đúng MỘT trong hai** (Ctrl+Cmd không phải phím tắt). Modifier tác giả KHÔNG nêu thì phải TẮT, nên `mod+k` và `mod+shift+k` là hai phím tắt khác nhau trên cùng một trang. Từ chối: phím giữ (`repeat`), bộ gõ đang dựng chữ (`isComposing`, `keyCode 229`). Cú pháp: `[mod|ctrl|meta|shift|alt]+…+<phím>` — một phím, tên phím so sánh không phân biệt hoa thường với `event.key` (`escape` và `space` có tên riêng). Chỉ `keydown/keyup`; không đi cùng `:escape`/`:enter`; `mod` không trộn với `ctrl`/`meta`. Sai cú pháp → `render.go` gọi tên lỗi tại chỗ và kernel tắt handler thay vì bắn nhầm.

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

### Code component: `kit.component(name, { state…, method…, init(context) })` — ✅ kernel 22/09 (B9 bước 1)

| `context.` | Là gì | Ghi chú |
| :--- | :--- | :--- |
| `host` | phần tử mang `data-kit-component` / `data-kit-scope` | |
| `owned(selector)` | các phần tử khớp dưới host, **kể cả host**; cây của host lồng (component/scope) **không** thuộc về mình | cùng hàng rào với runtime component |
| `element(name)` / `elements(name)` | `data-kit-element="name"` host sở hữu — cái đầu / tất cả; thiếu → `null` / `[]`; tên phải là identifier | cùng tên với `$element.name` trong biểu thức |
| `listen(target, type, fn, options)` | listener được gỡ cùng host | trả về cancel |
| `cleanup(fn)` | chạy khi morph gỡ host (`this` = scope); **cancel trả về = chạy ngay**, một lần | |
| `afterRender(fn)` | chạy **một lần** sau lượt vẽ kế, **ngoài** lượt — ghi state trong đó tự xếp lượt vẽ mới (kernel không có gotcha "gán trong afterRender không vẽ lại" của runtime component) | host đã bị gỡ → bỏ qua |

`this` trong `init`/method/cleanup/afterRender = scope của component. Host **luôn mount** khi qua lượt vẽ (seed + init) dù bên trong không có directive nào — component chỉ-init (favicon fallback, bộ đếm sống) vẫn khởi động. `init` ném → tới `data-kit-error` gần nhất với `$error.directive === "init"`, component vẫn mount với state đã seed. Chứng minh: 2 component riêng của apptop (`favicon-fallback.js`, `stats-live.js`) chạy **nguyên văn** trên kernel trong Chrome thật (`jit/js/tenant_component_browser_test.go`).

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
| B7 | `data-kit-style` | giữ ở runtime component; kernel **thêm khi có ca** | ✅ ĐÓNG 23/09: ca dùng = bộ component (thanh tiến trình, slider, popover, carousel đều cần giá trị liên tục). Kernel nhận dạng `data-kit-style:<property>` — MỘT thuộc tính một attribute, KHÔNG phải dạng list của runtime component (dạng list đã bị bỏ ở `bind` từ 21/09) |
| B8 | dạng tên trần / init của `data-kit-scope` | **cắt** — một literal, một parser; attribute rỗng = vùng rỗng | ✅ kernel; 0 site dùng |
| B9 | **một kernel hay hai runtime** | **MỘT KERNEL** — Quốc chốt 22/09 tối: "một kernel, markup do chủ site viết". Hệ quả: biểu thức trong markup KHÔNG phải ranh giới an toàn (chủ site viết, server render) → evaluator **mở** của kernel là chuẩn; evaluator đóng, đồ thị version `name@x.y.z`, giao hàng staged `/jit/<hash>` + SRI của KitJS không mang sang. Đường đi = **phép trừ**, không gộp một phát: `jit/javascript` đóng băng (không ngữ pháp mới), 10 tenant đang chạy giữ nguyên; port từng component sang kiểu kernel khi site cần; xoá `jit/javascript` khi tenant cuối rời `.jitjs()` | ✅ quyết định. Bước code theo thứ tự: (1) ✅ đêm 22/09 kernel `init(context)` cùng hình `context` đã chốt (`host owned element elements listen cleanup afterRender`, bảng ở §6) — 2 component apptop chạy nguyên văn; (2) ✅ đêm 22/09 component riêng của tenant trên kernel: **`_components/<name>.js`** cạnh site (và cấp identity dùng chung, walk-up như `_core`), **KHÔNG version** (file LÀ hợp đồng; `name@x.y.z` từ nay chỉ có thể là component của engine), zero-config, phát qua `/kit.js?components=` chỉ cho trang gọi tên; (3) port apptop (9 file, 1 461 dòng + `progress-bar`/`theme`/`shortcut` catalogue) → họ apptop rời `.jitjs()`; (4) kitjs.org/kitwork.io/studio/kitdesign/lofiwithme cuối cùng vì chính họ là catalogue |
| B10 | async | **nhánh B**: biểu thức đồng bộ; async sống trong **component** (method JS thật) hoặc **Drive**; suite tuân thủ đã chứng minh method IR-lambda có twin | ✅ 22/09: kernel — method trả Promise → vẽ lại khi settle (đã có); **ghi scope ngoài một lượt vẽ** (timer, `.then` không trả, callback) → xếp một lượt vẽ gộp; trong lượt (handler, model, render) không xếp thêm. Không có `effect`. Test: `write_repaint_test.go`, `jit/js/migration_browser_test.go` |

> Khi B9 cần đảo lại (giữ hai runtime độc lập), nói một câu là đủ — chưa có code nào phụ thuộc vào nó ngoài việc *không* thêm ngữ pháp riêng cho runtime component từ nay.

## 9. Bảng Giữ / Xoá / Hoãn — chuỗi phép trừ

| | Việc | Điều kiện | Trạng thái |
| :--- | :--- | :--- | :--- |
| **XOÁ** | IR client `data-kitwork-<directive>` + read-alias tiền tố dài cho directive/boundary | — | ✅ 21/09 (C1) |
| **XOÁ** | `data-kit-away` `data-kit-escape` `data-kit-guard` (→ modifier) | — | ✅ 21/09 |
| **XOÁ** | `data-kit-as`, đuôi `name=$alias`, `data-kitwork-alias`, `data-alias` (→ `data-kit-alias`) | — | ✅ 21/09 |
| **XOÁ** | `data-kitwork-key` trong `morph.js` (`jit/js/lib/more.js` đã xoá cùng hệ action); và 24/09 xoá nốt alias `data-key` | key là của tác giả và chỉ có MỘT chữ: `data-kit-key` (chữ của `for`). `data-key` là tên bất kỳ thư viện nào trên trang cũng có thể đang dùng — 6 chỗ ở apptop đã đổi |
| **XOÁ** | Hệ `action` jitjs (`lib/`, `behaviors`, `fire`, `compat.js`, `data-kit(work)-action/target`, `data-kit-trigger`) | không cần `effect`: 23 file site chuyển sang component/biểu thức/Drive; `more` 0 chỗ dùng | ✅ 22/09 |
| ~~XOÁ~~ **GIỮ** | Nhánh `typeof fn === "function"` (kernel call) | B10 chốt async sống trong **method JS thật** của component — nhánh này chính là chỗ method đó chạy (`fn.apply(scope)`); dòng XOÁ viết khi còn nghĩ async đi qua `$app` + scope-patch | ✅ 22/09 lật thành GIỮ |
| **XOÁ** | 9.002 dòng `runtime*.js`, `legacy/core/*`, `main copy*.js` | — | ✅ đã không còn (kiểm 22/09: `find` toàn `jit/` không ra file nào, thư mục `legacy` không tồn tại) |
| **GIỮ** | Inventory 18 năng lực (ideashipping §4) | regression test **trước** khi sửa | — |
| **XOÁ** | `jit/javascript` (runtime KitJS 357 KB nguồn, evaluator đóng, đồ thị staged) | B9 ✅ một kernel; điều kiện: 10 tenant `.jitjs()` port xong (apptop ×6, kitjs.org, lofiwithme, kitdesign, studio) | ☐ theo 4 bước ở §8 B9 |
| **HOÃN** | Extension 1 Proxy dependency engine | **đe doạ server twin** | — |
| **HOÃN** | Extension 5 virtualization | chưa site nào cần | — |
| **HOÃN** | 6 build profile → chỉ `core` + `full` | cắt theo nhu cầu | ✅ đã là 2: `assemble.go` chỉ còn `kit` + `hydrate` (kiểm 22/09) |
| **KHÔNG LÀM** | `switch/case`, `teleport`, transition framework, state manager mới, `state`/`props` | non-goals | — |

---

## 10. Bảng Sự thật kỹ thuật (cho agent — kẻo sửa nhầm) — cập nhật 22/09

| Câu hỏi | Sự thật trong code | Nguồn |
| :--- | :--- | :--- |
| Source-of-truth ở đâu? | `kernel.js` + `modules/*` theo danh sách embed trong `Runtime()` | `render.go` |
| `render()` cập nhật thế nào? | Quét lại toàn document mỗi tick (dirty-check): `seedElements` → `seedModels` → for/if → text/show/bind/class | `kernel.js` `render()` |
| Server và client lệch ở đâu? | Server scope **phẳng**, client scope **chuỗi lexical** → ghi-xuyên-biên (corpus có ca) | `eval.go` |
| Server biết state gì lúc render? | `data-kit-model` `value=` + **`data-kit-seed`** ngoài boundary; không biết list | `prerender.go`, `prerender_seed.go` |
| List client hôm nay? | **Có `for`** ở cả hai runtime, keyed, overlay 7 tên; server chưa materialize | `kernel.js` `renderFor`, `structure.js` |
| morph có sẵn key? | Có: `data-kit-key` (và `data-kitwork-key` chờ trừ) | `morph.js` |
| Hai runtime khác gì? | Kernel: evaluator mở, phần tử thô, scope lexical. Runtime component: ngữ pháp đóng (own-property + bảng method/element), scope cô lập, `$alias` action-only, closed graph có version | `evaluator.js`, `kernel.js` |
| Kích thước | kernel minified gzip ≈ 10.1 KB / 12 KiB (gate `size_budget_test.go`); `/kit.js` production đi qua `utilities/minifier.JS` | `work/jithydrate.go` |
| Whitelist globals | 14: Math, Date, JSON, Number, String, Boolean, Array, `Object` (= chỉ `{ keys }`), parseInt, parseFloat, encode/decodeURIComponent, isNaN, isFinite — **có thật từ 29/09**. Trước đó dòng này ghi như sự thật trong khi code KHÔNG có: `Math.ceil(7/4)` ra `undefined` ở cả hai engine, im lặng. Global nằm CUỐI chuỗi tra tên (tên trang khai thắng). Không có `new` → `Date` chỉ `now/parse/UTC`. Server trả phần tất định; `Date` và `Math.random` trả nil (trả lời ở trình duyệt). Đường tới `Function` vẫn đóng nhờ blocklist `.constructor`/`.prototype` | `globals.go`, `kernel.js` `expressionGlobals`, corpus `global-*`, `globals_test.go` |
| `++` / `--` có không? | **Có** (29/09, port từ KitJS 1.0.0): tiền tố và hậu tố, đích giống `=` — tên, hoặc `$.name`; `obj.x++` bị từ chối như `obj.x = v`. **Op riêng, không phải đường tắt của `x = x + 1`**: giá trị ép sang SỐ trước như JS, nên `"5"++` là `6` còn phép gán sẽ nối thành `"51"` (giá trị từ `data-kit-model` là chuỗi). Hậu tố trả số cũ, tiền tố trả số mới. `+=` **không** có — KitJS cũng không | `compile.go` `updateNode`, `kernel.js` `update`, corpus `update-*` |
| Method mảng với lambda? | **Có** (29/09, port từ KitJS 1.0.0): `join includes indexOf slice map filter find some every`, thêm `concat` (30/09 — danh sách LỚN LÊN trong ngữ pháp mà không `push` sửa tại chỗ: `tags = tags.concat([draft])`; mảng trải một tầng như JS); callback nhận `(item, index, array)`. Trước đó kernel PARSE được `items.filter((x) => x.done)` rồi ném `object is not a function` (lambda là cây IR, không phải hàm), twin Go không có method mảng nào và trả `null`. Kernel: `callbacks()` biến lambda thành hàm thật chạy lại cây trong scope nơi gọi; Go: `callArrayWithLambda` + `invokeLambda` dùng chung với lời gọi trần. Callback **dùng chung ngân sách** của cả biểu thức ở cả hai phía — `filter` trên 20.000 phần tử bị chặn | `kernel.js` `callbacks`, `eval.go` `callArrayWithLambda`, corpus `array-*`, `array_methods_test.go` |
| Chuỗi có escape không? | **Có** (30/09; KitJS 1.0.0 ghi "JSON-style escapes"): `\n \t \r \b \f \v \0`, `\uXXXX`, và mọi ký tự khác sau `\` là chính nó (`\'`, `\"`, `\\`). Trước đó KHÔNG có: `'a\nb'` là 5 ký tự, `value.split('\n')` tách theo dấu gạch chéo, `'it\'s'` gãy parse. Twin Go thêm string `split` cùng lúc (code-editor đếm dòng bằng nó) | `compile.go` lex, `kernel.js` lex, corpus `string-escape-*` |
| Binding có được ghi state? | **Không** — binding (`text` `show` `if` `class` `bind:` `style:`) chạy mỗi lần vẽ, ghi trong đó là đổi dữ liệu mỗi khung hình. Runtime **không chặn** (không có phân biệt action/binding lúc chạy); **verify pass báo** mọi phép ghi (`=` `++` `--`), kể cả trong thân lambda — `items.map((x) => n++)` gọi nó mỗi lần vẽ | `verify.go` `checkBindingWrites` |

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

> **Một câu, 22/09:** `for` đã cưỡi morph, bộ test tuân thủ đã chạy, ngữ pháp đã về một bộ (§2), hệ jitjs đã trừ **không cần effect** (component-first: hành vi = component hoặc biểu thức, vận chuyển = Drive). **B9 chốt tối 22/09: một kernel** (markup do chủ site viết → evaluator mở là chuẩn); KitJS đóng băng, trừ dần theo 4 bước ở §8. Từ đây mọi ngữ pháp chỉ có MỘT chỗ để sống.
