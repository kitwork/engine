# KitJS V2 — Sổ theo dõi thi hành spec (LEDGER)

> `ideaship-final.md` là cửa chính: nó nói **cái gì** đã chốt. Sổ này nói **đã làm tới đâu** — cho
> từng mục đã chốt, trạng thái thật trong code ở ba nơi phải đồng ý với nhau: bộ kiểm Go
> (`jit/javascript/scan.go`, `jit/hydrate/compile.go`), hydrate kernel (`jit/hydrate/kernel.js`,
> `/kit.js`) và runtime component KitJS (`jit/javascript/src/*`). Cập nhật sổ trong cùng commit với
> code; một dòng ghi "xong" mà không có test đi kèm là dòng sai.
>
> Bắt đầu 21/09/2026, sau khi phát hiện runtime component được xây lệch spec (30+ component của
> catalogue kitwork.io/components làm theo runtime, không theo spec).

## Trạng thái từng mục đã chốt (§8 của FINAL)

| # | Mục đã chốt | Spec | Kernel `/kit.js` hôm nay | Runtime component hôm nay | Trạng thái |
| :-: | :--- | :--- | :--- | :--- | :--- |
| 1 | Binding 3 nhóm | `data-kit-bind:<name>` — **một dạng**, tên quyết định nhóm (Quốc chốt 21/09: bỏ `data-kit-attr`, vì hai tên trùng việc). Reflected boolean → property + attribute · live property → property · còn lại (aria-*, data-*, tên có gạch nối) → attribute | ✅ `writeBinding` trong kernel; SSR `PreRenderBind` bake theo cùng luật | ✅ `dom.js` `writeBinding`; scanner từ chối dạng list và đích không an toàn | ✅ 21/09 — test: `binding_groups_browser_test.go`, `bind_dom_test.go`, `scan_test.go`; 405 thuộc tính trên 12 site + fixture đã chuyển |
| 2 | Scope literal | `{ qty: 1, price: 250 }` hoặc `qty: 1, price: 250` — dấu `,`, **không** `;` | ✅ `boundaryScope` nhận dạng không ngoặc (`^tên:`) và bọc `{…}` rồi đưa vào CÙNG parser biểu thức — không parser thứ hai; dạng tên và dạng init `a = 1; b = 2` giữ nguyên | ✅ shorthand tách bằng `,`; gặp `;` báo `scope fields are separated by "," not ";"` (JS + `scope_seed.go`). Parser dữ liệu thuần vẫn giữ: giá trị phải là data, không biểu thức — hẹp hơn kernel, cùng ngữ pháp | ✅ 21/09 — test: `scope_literal_dom_test.go` (kernel), `scope_seed_test.go`, `scope_browser_test.go`; 89 thuộc tính trên 49 file (site, fixture, docs, cmd/gallery, cmd/kitui) đã chuyển |
| 3 | Alias = instance | `data-kit-alias="$modal"` | ✅ một cách viết: `data-kit-alias`; đuôi `name=$alias`, `data-kitwork-alias`, `data-alias` gỡ hẳn (`parseComponentTag` chỉ còn `name@version`; `ComponentName` Go chỉ cắt `@`; regex `jit/js/runtime.go` không nhận đuôi) | ✅ `data-kit-as` → `data-kit-alias` (src/component.js, drive.js, morph.js, directives.js); scanner từ chối `data-kit-as` là tên lạ | ✅ 21/09 — test: `app_surface_test.go` (kernel, kể cả 3 cách viết bị bác), `scan_test.go`, `prerender_bind_test.go`, `js/sidebar_test.go`; 247 chỗ trên 86 file + examples + huynhnhanquoc.com dashboard đã chuyển |
| 4 | Ref = element | `data-kit-ref="search"` → `$refs.search` (phạm vi: CÒN TREO, đề xuất theo instance) | ✅ `$refs` trong `elementScope` (cạnh `$el`/`$root`): registry của boundary sở hữu — `closest(SCOPE)` của phần tử đang hành động; tra cứu lúc đọc; tên thiếu → nullish | ✅ `$refs` là local của MỌI action (`executeAttribute`), `core.refsFor` = `ownedElements("[data-kit-ref]")` của host/scope gần nhất (không có → trang); action-only theo luật `$` sẵn có; element đi qua ngữ pháp đóng bằng bảng đọc/động từ (`ELEMENT_READS`/`ELEMENT_CALLS` trong evaluator.js) — không ghi, không đi cây; scanner nhận `data-kit-ref=<identifier>` | ✅ 21/09 (THI HÀNH THEO ĐỀ XUẤT: phạm vi = boundary gần nhất, boundary lồng không thấy nhau) — test: `refs_browser_test.go`, `refs_dom_test.go`, `scan_test.go` |
| 5 | 7 biến hệ thống | `$this`(~`$el`) `$host`(~`$root`) `$event` native `$error` `$refs` `$app` `$` | `$el`, `$root`(=element), `$`, `$app`, `$theme` | `$event` bản chụp, `$app`; `$this/$host/$refs/$error` chỉ dành riêng tên | ☐ |
| 6 | Modifier pipeline | `:window :document` → `:outside :escape :enter` → `:prevent` → `:stop` → `:debounce(n) :throttle(n)` → `:once` → chạy | directive riêng `data-kit-away`, `data-kit-escape`; không modifier | `self prevent stop once outside enter escape debounce(n)`; thiếu `window document throttle`; thứ tự chưa cố định | ☐ |
| 7 | `for` trên thẻ thường | `<li data-kit-for="item, i of items" data-kit-key="item.id">`; SSR ra `<!--kit-for:start id=x-->` + `data-kit-item="x"`; overlay `item index count first last even odd`; blueprint từ IR server; chung key resolver với morph | clone chính thẻ (được); overlay `item, index` | bắt `<template>`; overlay `item, index` | ☐ |
| 8 | `data-kit-error` + `$error` | boundary bắt lỗi component (lan truyền: CÒN TREO) | không | không | ☐ |
| 9 | `show` → `hidden` | | ✅ | ✅ | ✅ |
| 10 | dirty-check, không đồ thị phụ thuộc | | ✅ | ✅ (`core.invalidate` theo record) | ✅ |
| 11 | conformance `walk ≡ eval` | corpus dùng chung, CI | ✅ 14 ca (`conformance_corpus.json`) | — | ✅ giữ, thêm ca theo từng mục |
| 12 | Core ≤ 12KB gzip, kiểm bằng CI | | chưa có test | — | ☐ |

## Còn treo (KHÔNG đóng băng — làm theo đề xuất, ghi rõ là đề xuất)

| Vấn đề | Đề xuất sẽ thi hành | Chờ Quốc |
| :--- | :--- | :--- |
| `$this` hay `$el` là chính | Chính: `$this`; `$el` là alias tương thích (như bảng §3) | tên cuối |
| Phạm vi `$refs` | ĐÃ THI HÀNH theo đề xuất (21/09): boundary gần nhất (host component / `data-kit-scope` / item, không thì trang); boundary lồng không thấy ref của nhau; trùng tên → phần tử đầu; tên thiếu → nullish (`$refs.x?.focus()`). Runtime component chỉ mở element qua bảng đọc/động từ đóng (như string/array) — không ghi | xác nhận phạm vi + bảng động từ |
| `$error` lan truyền | Không lan; boundary gần nhất bắt, không có boundary → `console.error` như nay | định nghĩa |
| Async | Không đụng trong đợt này | quyết định A/B |

## Ngoài spec — phát sinh từ catalogue, chờ bàn sau khi A xong

- Đặt tên phần tử của component (`data-kit-element` đề xuất) — spec chưa có; `data-kit-ref` là ref
  element cho biểu thức, không phải hợp đồng bộ phận của component.
- `data-kit-key` nguyên văn trên phần tử thường; `context.element()`; hợp đồng `contract` trong
  `kit.component`; scope lồng không leo lên host ngoài (`$alias` chỉ trong action).
- Hai runtime (`/kit.js` và runtime component) cùng đọc một ngữ pháp — spec §9 "chuỗi phép trừ"
  chưa làm; sổ này chỉ giữ hai bên **đồng ý về ngữ pháp**, chưa gộp.

## Nhật ký

- 21/09/2026 — mục 4 xong theo đề xuất (phạm vi = boundary gần nhất). Phát hiện: evaluator của runtime component là ngữ pháp ĐÓNG (chỉ own-property và bảng method cho string/number/array) nên element thô không đọc/gọi được gì — thêm bảng `ELEMENT_READS`/`ELEMENT_CALLS`; kernel thì đã mở `$el` thô từ trước. Hai bên khác độ mở, cùng ngữ pháp.
- 21/09/2026 — mục 3 xong (`data-kit-alias` là cách viết duy nhất ở cả hai runtime; alias trỏ INSTANCE, `data-kit-ref` (mục 4) sẽ trỏ element).
- 21/09/2026 — mục 2 xong (dấu `,`, ngoặc tuỳ chọn, cả hai runtime + scanner; `ideaship.md`/`ideaship-master.md`/`ideashipping.md` vẫn ghi ví dụ `;` — là hồ sơ, FINAL thắng; kernel giữ thêm dạng init `a = 1; b = 2` ngoài spec — bàn ở đợt B).
- 21/09/2026 — lập sổ; mục 1 xong (một dạng `data-kit-bind:<name>`; `data-kit-attr` bị bỏ theo quyết định của Quốc; danh sách `a: x; b: y` và dạng object của kernel gỡ hẳn — mọi site trong repo đã chuyển bằng script).
