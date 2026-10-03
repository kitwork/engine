# Kiến trúc component của Kitwork (bản nháp 03/10/2026)

Một cách duy nhất để làm mọi phần giao diện có tên trong Kitwork — catalogue của engine lẫn
`_components/` của từng site, trên web, desktop và mobile. Tài liệu này nói **luật**; cách viết chi
tiết nằm ở skill `kitwork-javascript`. Mục đánh dấu **CHỜ QUYẾT** là chỗ Quốc chưa chốt.

## 1. Đơn vị và tầng

Mỗi phần giao diện có tên là một **đơn vị**, thuộc đúng một tầng:

| Tầng | Dùng khi | Gồm |
|---|---|---|
| Chất liệu | chỉ cần hình dạng | markup + class |
| Mẫu | hành vi markup nói được | markup + ngữ pháp `data-kit-*` |
| Component | hành vi markup không nói được: focus, phím, đo, hẹn giờ | markup + một file JS |
| Dịch vụ | cần nền tảng: camera, file, cửa sổ | `kit.<tên>`, không có markup riêng |

**Luật nâng tầng.** Đơn vị bắt đầu ở tầng thấp nhất. Chỉ lên tầng trên khi chỉ ra được đúng một việc
tầng dưới không làm được, và ghi việc đó ở đầu file. Ví dụ: counter là **mẫu**
(`data-kit-scope="count: 0"` + `data-kit-click="count++"`); nó chỉ thành component khi cần, chẳng
hạn, giữ-nút-để-tăng bằng hẹn giờ.

Tên bốn tầng — **CHỜ QUYẾT**.

## 2. Một hình dạng thư mục

```text
<tên>/
  <tên>.html       markup chuẩn — hợp đồng, ví dụ và trang test là MỘT file
  <version>.js     chỉ tầng component
  <tên>_test.go    test trên trình duyệt thật, nạp đúng file production giao
```

Catalogue engine (`engine/jit/js/components/<tên>/`) và site (`_components/`) cùng hình dạng.
Lấy `<tên>.html` làm nguồn duy nhất cho hợp đồng, ví dụ và test — **CHỜ QUYẾT**.

## 3. Hợp đồng — viết trong code, server đọc được

Một component công bố đúng sáu thứ, và chỉ sáu thứ:

1. **State** — khoá trong blueprint (`count: 0`).
2. **Hành động** — method (`increment`).
3. **Sự kiện** — `kit.context(this).emit("<tên>", detail)`, trang nghe bằng `data-kit-event:<tên>`.
   **Sự kiện chỉ dành cho việc vừa xảy ra mà không phải state** — thứ trang cần *phản ứng*
   (`submitted`, `dismissed`, `reached-end`, dialog `closed` kèm kết quả người dùng chọn). Thứ trang
   cần *biết giá trị* thì đọc thẳng state qua alias (`$counter.count`), không phát sự kiện để trang
   chép sang một biến khác: một giá trị ở hai nơi cần thêm một lối nối, và lối nối là chỗ sai.
   Tên không được trùng sự kiện DOM (`change`, `click`, `input`, `select`, `close`, `toggle`… bị
   từ chối). Vì vậy động từ hiện tại hay đụng tên DOM; quá khứ (`changed`, `selected`, `closed`)
   thì không. Thì của tên sự kiện — **CHỜ QUYẾT** (đề xuất: quá khứ).
4. **Phần tử bắt buộc** — `data-kit-element` + `context.require`. **Chỉ đặt tên phần tử khi JS phải
   tự tay chạm vào nó** (focus, cuộn, đo, `showModal()`); mọi thứ khác markup tự bind từ state.
5. **Móc style** — state; markup tự bind ra `data-*`/ARIA. Component không bao giờ ghi class,
   style hay attribute lên markup.
6. **Bàn phím và ARIA** — theo mẫu WAI-ARIA của đúng loại đó, ghi ở đầu file.

Không có file khai báo song song (JSON đã thử 01/10 và bỏ: nó chép lại code). Server đọc tên từ
chính file (`jit/js/component_names.go`): khoá literal + mọi property code gán. Khoá tính toán
(`this[key] = …`, `Object.assign(this, …)`, spread) làm component **không đọc được** — tránh, vì
khi đó lỗi viết sai bên trong nó không được báo. Ô riêng thì khoá bằng hằng `Symbol` của file.

**Version là hợp đồng.** Thêm mà không đổi nghĩa cái cũ → số phụ (1.1.0); đổi nghĩa → số chính
(2.0.0); sửa lỗi không đổi gì markup thấy → tại chỗ. Luật này — **CHỜ QUYẾT** (nó cũng trả lời
`calendar@1.0.0` có thêm `focus()`).

**Tham số từ markup** — chưa có cách chuẩn: muốn đổi `min`/`max` của counter phải sửa JS.
Hướng nghiên cứu: `data-kit-seed` trên host, hoặc một dạng tham số riêng — **CHỜ QUYẾT**.

## 4. Ghép

- Một phần tử mang một component.
- Ghép bằng lồng nhau; bên ngoài đọc state và gọi hành động qua alias `$tên`; con báo lên bằng sự
  kiện (nổi bọt) chỉ khi có việc vừa xảy ra.
- Tên trong markup tra theo chuỗi: component → các scope bao quanh → trang. Alias `$tên` tra theo
  chuỗi của **host** của nó.
- Không có state toàn cục nào ngoài alias.

## 5. Style

Token (`canvas`, `paper`, `ink`, `line`, `brand`) → class chất liệu → biến thể theo state
(`data-[state=open]:…`, `aria-[expanded=true]:…`). Dùng thẻ HTML đúng nghĩa trước khi thêm ARIA
(`<dialog>` cho hộp thoại, `<button>` cho nút). Không dùng `<output>`: con số hiển thị là `<span>`.

## 6. Nền tảng

Một markup cho web, desktop, mobile. Khác biệt nền tảng chỉ đi qua dịch vụ `kit.<tên>`; component
không tự rẽ nhánh theo nền tảng mà hỏi dịch vụ có sẵn không. Trên web, dịch vụ native trả
`UNAVAILABLE`.

## 7. Vòng đời

`seed` → `init(context)` → vẽ → `context.afterRender` → `context.cleanup` khi bị gỡ.

- `context` có 9 khoá: `host owned element elements listen cleanup afterRender require emit`.
- Method lấy lại đúng `context` đó bằng **`kit.context(this)`** — từ trong host, từ scope lồng bên
  trong, hay qua alias. Không cần tự cất context.
- Mọi listener/hẹn giờ đăng ký qua `context` để được dọn khi host bị gỡ.
- Drive giữ component qua lượt chuyển trang khi nó vẫn là cùng một thứ: cùng thẻ, cùng component,
  cùng scope literal.

## 8. Giao

Mỗi phần một file `/kit/<phần>.<hash>.js`, cache vĩnh viễn; trang chỉ nhận thứ nó dùng (kernel, Drive
nếu site bật, mỗi directive/dịch vụ/component nó gọi tên). Site ghim version bằng
`router.javascript({ components: { tên: "x.y.z" } })`. Kernel lõi có ngân sách 12 288 byte gzip:
máy móc ít trang dùng là file riêng, không phải byte kernel.

## 9. Định nghĩa "xong"

Một đơn vị vào bộ khi đủ cả năm:

1. markup chuẩn chạy được;
2. test trên trình duyệt thật nạp đúng file production, kèm phép thử ngược đỏ đúng lý do;
3. server đọc được tên (không khoá tính toán);
4. bàn phím và ARIA đúng mẫu của loại đó;
5. trang trưng bày lấy thẳng từ markup chuẩn — ví dụ luôn là mã nguồn thật.

## Ví dụ: counter

Tầng **mẫu** — không cần JS:

```html
<div data-kit-scope="count: 0">
  <button data-kit-click="count--" data-kit-bind:disabled="count <= 0" aria-label="Giảm">−</button>
  <span data-kit-text="count">0</span>
  <button data-kit-click="count++" data-kit-bind:disabled="count >= 10" aria-label="Tăng">+</button>
</div>
```

Tầng **component** — khi cần một luật markup không nói được: ở đây là kẹp giá trị trong khoảng:

```js
kit.component("counter", {
  count: 0, min: 0, max: 10, step: 1,
  increment: function () { this.set(this.count + this.step); },
  decrement: function () { this.set(this.count - this.step); },
  set: function (value) {
    var next = Math.min(this.max, Math.max(this.min, value));
    this.count = next;
  }
});
```

```html
<div data-kit-component="counter" data-kit-alias="$counter">
  <button data-kit-click="decrement()" data-kit-bind:disabled="count <= min" aria-label="Giảm">−</button>
  <span data-kit-text="count">0</span>
  <button data-kit-click="increment()" data-kit-bind:disabled="count >= max" aria-label="Tăng">+</button>
</div>

<p>Đã chọn: <span data-kit-text="$counter.count">0</span></p>   <!-- đọc state qua alias -->
<button data-kit-click="$counter.set(0)">Đặt lại</button>       <!-- gọi hành động qua alias -->
```

Counter không phát sự kiện nào: trang muốn biết con số thì đọc `$counter.count`.

Server báo lúc render nếu viết `incremnt()` trong host, hoặc `$counter.reset()` ở ngoài.
