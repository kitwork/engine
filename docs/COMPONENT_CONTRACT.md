# Hợp đồng component (01/10/2026)

Trạng thái: **ĐÃ CHỐT 01/10** — Quốc đồng ý cả 5 câu ở mục 9 đúng như đề xuất. Lộ trình ở mục 10:
bước 1 xong, các bước sau chưa làm.

## 1. Vì sao cần

Catalogue có 22 component kernel (`engine/jit/js/components`), mỗi cái có test trên trình duyệt
thật, và mọi site đã chạy trên kernel. Hai ngày làm việc trên đó cho thấy bốn chỗ hổng, đều cùng
một gốc: **component không khai báo nó cần gì và nó đưa ra gì.**

1. **Sai thì im lặng.** `tree` cần mỗi item có `data-id`, `split` cần `aria-valuemin/max/now`,
   `otp` cần các `data-kit-element="slot"`. Thiếu thì component không chạy và không ai báo gì.
   Verify pass không biết, vì không có chỗ nào nói component đó cần gì.
2. **Tài liệu viết tay ở ba nơi:** chú thích đầu file, `catalog.kitwork.js` (state, method, tip) và
   skill. Ngày 30/09 phải viết tay lại khoảng 30 mục catalogue; lệch nhau chỉ là chuyện sớm muộn.
3. **Markup không phản ứng được với component.** apptop-impact cần đếm số khi người xem cuộn tới.
   `visible` đã biết lúc đó (`seen` thành `true`) nhưng markup không có cách nói "đúng lúc đó, chạy
   `start()`" — nên apptop-impact vẫn tự viết `IntersectionObserver` riêng.
4. **Ba đường đưa dữ liệu vào, chạy theo thứ tự ngầm:** thuộc tính thường trên host, state mặc
   định, và `data-kit-seed` (chạy *sau* `init`). Lỗi tab stop của `tree` (30/09) sinh ra đúng từ
   thứ tự này.

Thêm một chuyện nhỏ hơn: 17 trong 22 component lặp cùng một đoạn để giữ `context` cho các method chạy
sau `init` (`var X = Symbol(...)`, `scope[X] = {context}`, `var own = scope[X]; if (!own) return;`).
Lỗi `context is not defined` của apprank.vn (sống từ 22/09 tới 01/10) là đoạn đó viết thiếu.

## 2. Ý chính

Mỗi component có một **hợp đồng**: một file JSON nằm cạnh file JS, nói component cần gì từ markup
và đưa ra gì cho markup.

- Trình duyệt **không tải** file này — nó chỉ dành cho máy chủ.
- **Verify pass** dùng nó để báo lỗi khi render, thay vì component im lặng.
- **Trang catalogue** sinh phần state/method/sự kiện từ nó, chỉ còn lời giới thiệu và tip là viết tay.
- **Test** kiểm hợp đồng khớp với code: khai báo một method mà file JS không có, hoặc ngược lại, là đỏ.

## 3. Hợp đồng trông thế nào

```
engine/jit/js/components/visible/v1.0.0.js     ← code, như hiện nay
engine/jit/js/components/visible/v1.0.0.json   ← hợp đồng, mới
```

```json
{
  "name": "visible",
  "version": "1.0.0",
  "summary": "Is this element on screen, and has it ever been?",

  "host": {
    "attributes": {
      "data-threshold": { "type": "number", "default": 0, "doc": "How much must show, 0 to 1." },
      "data-margin":    { "type": "string", "doc": "Grows or shrinks the screen, as CSS margin." }
    }
  },

  "elements": {},

  "state": {
    "visible": { "type": "boolean|null", "doc": "On screen now; null until the first answer." },
    "seen":    { "type": "boolean|null", "doc": "On screen at least once; stays true." }
  },

  "methods": {},

  "events": {
    "seen": { "doc": "Fires once, the first time the element reaches the screen." }
  }
}
```

Một component có phần tử con thì khai báo chúng, kèm thuộc tính bắt buộc:

```json
"elements": {
  "item": { "required": true, "attributes": { "data-id": { "required": true } },
            "doc": "One per tree item; data-id names it." }
}
```

Component riêng của site làm y như vậy: `_components/bid-stepper.js` có thể kèm
`_components/bid-stepper.json`. Với site, hợp đồng là **tuỳ chọn** — không có thì verify pass
không kiểm gì thêm, như hiện nay.

**Vì sao là JSON riêng, không nằm trong file JS:** Go cần đọc nó mà không chạy JavaScript.
Đọc một object literal trong file JS bằng regex là mong manh; JSON thì Go đọc bằng thư viện chuẩn.
Cái giá là hai file cho một component — được bù bằng test bắt lệch giữa hai file.

## 4. Verify pass dùng hợp đồng thế nào

Khi render (chỉ lúc phát triển, như hiện nay), với mỗi `data-kit-component="x"` trên trang:

| Viết trong markup | Báo |
|---|---|
| Thiếu phần tử bắt buộc (`tree` không có `data-kit-element="item"`) | lỗi |
| Phần tử thiếu thuộc tính bắt buộc (item không có `data-id`) | lỗi |
| Thuộc tính `data-*` trên host mà hợp đồng không có (`data-treshold` gõ sai) | cảnh báo |
| Biểu thức gọi method không có (`toggel()`) hoặc đọc state không có | cảnh báo |
| `data-kit-event:x` cho sự kiện component không phát | cảnh báo |

Biểu thức đã được verify pass phân tích sẵn (nó đã biết tên nào được khai báo), nên phần đọc
state/method chỉ là thêm tên vào bảng đó.

## 5. Sự kiện từ component: `data-kit-event:<tên>`

Component phát sự kiện bằng một khoá mới trong `context`:

```js
init: function (context) {
  …
  if (firstTime) context.emit("seen");          // phát một lần
}
```

Markup nghe bằng dạng đầy đủ của họ sự kiện:

```html
<section data-kit-component="visible" data-kit-event:seen="$impact.start()">
  <div data-kit-component="apptop-impact" data-kit-alias="$impact">…</div>
</section>
```

- `emit` phát một sự kiện DOM thật trên host (`CustomEvent`, có nổi bọt), nên phần tử bao ngoài
  cũng nghe được. Dữ liệu kèm theo đọc bằng `$event.detail`.
- `data-kit-event:<tên>` là dạng đầy đủ cho **mọi** sự kiện: của component (`seen`, `moved`), và
  của DOM nằm ngoài danh sách hiện có (`scroll`, `paste`). Modifier nối sau như cũ:
  `data-kit-event:keydown:enter:prevent`.
- `data-kit-click`, `data-kit-input`, `data-kit-keydown`… **giữ nguyên làm dạng viết tắt.** Chỉ
  riêng kitwork.io đã có hơn 170 chỗ `data-kit-click`; không đổi.
- Vì sao `event:` mà không `on:`: ngữ pháp đã có khuôn `data-kit-<việc>:<đích>` (`bind:`,
  `style:`, `seed:`), và tên sự kiện của component nằm sau `event:` thì không bao giờ đụng tên
  directive — một component phát sự kiện tên `show` không thể nhầm với `data-kit-show`.

Hợp đồng khai báo những sự kiện component phát (mục `events`). Một tên trùng sự kiện DOM gốc
(`click`, `input`…) bị test từ chối, để `data-kit-event:click` luôn có một nghĩa.

## 6. Ghép component: vẫn một component trên một phần tử

Ví dụ apptop-impact ở trên cho thấy ghép được bằng **lồng nhau + sự kiện + alias**: `visible` ở
ngoài, báo `seen`; apptop-impact ở trong, nhận `start()`. Không cần cho hai component cùng nằm
trên một phần tử.

Đề xuất **giữ luật một phần tử – một component**. Hai component trên một phần tử đặt ra câu hỏi
khó: biểu thức `seen` trên phần tử đó đọc của component nào? Khi một app thật cần mà lồng nhau
không giải được, lúc đó mới mở lại câu hỏi này.

## 7. Bớt mã khuôn mẫu: `kit.context(this)`

Kernel giữ `context` của mỗi component, và method lấy lại bằng một lời gọi:

```js
syncInputs: function () {
  var context = kit.context(this);       // null nếu component chưa gắn hoặc đã gỡ
  if (!context) return;
  context.element("amount").value = …;
}
```

Thay cho đoạn `Symbol` + `scope[X] = {context}` + `delete scope[X]` mà mỗi component tự viết.
Component cũ vẫn chạy; chuyển dần, không bắt buộc.

## 8. Ba đường đưa dữ liệu vào: gom thành một thứ tự rõ ràng

Hợp đồng gọi tên từng đường, và kernel chạy chúng theo **một** thứ tự cố định trước `init`:

1. **State mặc định** — từ định nghĩa component (và ghi trong hợp đồng).
2. **`data-kit-seed`** nằm trong host — **chuyển lên chạy trước `init`** (hiện chạy sau).
3. **`init(context)`** — đọc thuộc tính cấu hình trên host (đã khai báo trong hợp đồng), và lúc
   này state đã có đủ giá trị gieo.

Bước 2 sửa tận gốc kiểu lỗi của `tree` hôm 30/09: một mặc định suy ra từ `selected` sẽ thấy giá
trị đã gieo. Đây là thay đổi thứ tự trong kernel, nên cần test riêng cho mọi component đang dùng
seed.

## 9. Các câu đã chốt (01/10: đồng ý cả 5)

1. **Hợp đồng là file JSON riêng** cạnh file JS (đề xuất), hay khai báo bên trong file JS?
2. **`data-kit-event:<tên>`** cho sự kiện (đề xuất), giữ `data-kit-click`… làm dạng viết tắt?
3. **Một phần tử – một component** giữ nguyên (đề xuất), ghép bằng lồng nhau + sự kiện?
4. **Version mang nghĩa gì?** Đề xuất: version là *hợp đồng*. Sửa lỗi không đổi hợp đồng thì sửa
   tại chỗ (hash trong tên file lo chuyện cache, như đã làm với `tree`); đổi hợp đồng — thêm, bớt
   hay đổi nghĩa thuộc tính, phần tử, state, method, sự kiện — thì ra version mới.
5. **Seed chạy trước `init`** (mục 8) — đồng ý đổi thứ tự này?

## 10. Lộ trình khi đã chốt

Mỗi bước tự đứng được, có test, và không làm vỡ markup hiện có.

1. ✅ **Hợp đồng** (01/10): `jit/js/contract.go` (`ParseContract`, `ComponentContract`,
   `ComponentVersionFiles`) + 23 file hợp đồng (22 component, clipboard có hai version).
   Thêm so với bản phác ở mục 3: `host.tag` (dialog chỉ chạy trên `<dialog>`), `many` cho phần
   tử lặp, `computed` cho getter, `page` cho tên component đọc từ scope của trang (kanban
   `cards`), `internal` cho method trang không nên gọi (dialog `lockScroll`). Bộ đọc giữ thứ tự
   khoá như khi viết, và từ chối khoá lạ, khoá trùng, tên sự kiện DOM, tên kernel giữ (`init`).
   Test (`contract_test.go`): mỗi file JS có hợp đồng và ngược lại; mười kiểu hợp đồng sai đều bị
   từ chối đúng lý do, có ca đối chứng; và trên Chrome thật, state/getter/method khai báo khớp
   `kit.blueprints` — đã thử làm hỏng ba hợp đồng, test đỏ đúng ba lý do.
2. **Verify pass** dùng hợp đồng (mục 4).
3. **Catalogue** sinh state/method/sự kiện từ hợp đồng; xoá phần viết tay trùng lặp trong
   `catalog.kitwork.js`.
4. **Sự kiện:** `context.emit` + `data-kit-event:<tên>` trong kernel và trong bản Go (verify),
   kèm corpus cho cả hai. `visible` phát `seen`. Chuyển apptop-impact sang dùng `visible` — đó là
   bằng chứng ghép component chạy trên một app thật.
5. **`kit.context(this)`**, rồi chuyển dần các component sang.
6. **Seed trước `init`** (nếu anh đồng ý ở câu 5).
