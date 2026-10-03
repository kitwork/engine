# Hợp đồng component (chốt 01/10, sửa 02/10/2026)

Một component nói rõ nó cần gì từ markup và đưa gì lại — **ngay trong code của nó**, không file
riêng. Bản 01/10 đặt hợp đồng vào file JSON cạnh mỗi file JS (`7c1234b0`); 02/10 Quốc thấy rắc
rối và đã revert: JSON chép lại state và method mà file JS đã nói, rồi cần một test chỉ để giữ
hai bản khớp nhau. Phần thật sự mới — phần tử và thuộc tính bắt buộc — nằm gọn trong một lời gọi.

## Các quyết định

1. **Component tự nói nó cần gì: `context.require(name, ...attributes)`.** Gọi trong `init`. Sau
   lượt vẽ đầu, kernel kiểm host có phần tử `data-kit-element="<name>"` và mỗi phần tử có đủ các
   thuộc tính (viết thẳng hay bind đều được); thiếu thì báo ra console (kèm directive và phần tử), một lần, nói rõ component nào thiếu gì. Trả về các phần tử, để dùng luôn.

   ```js
   init: function (context) {
     context.require("item", "data-id");   // tree: thiếu → "tree needs data-kit-element=\"item\" …"
   }
   ```

   Chỉ dùng cho phần tử luôn phải có trong markup. Phần tử do `data-kit-for` sinh ra và có thể
   rỗng (option của combobox sau khi lọc, card của kanban) không `require`.

2. **Sự kiện: `context.emit(name, detail)` → `data-kit-event:<tên>`.** Phát một `CustomEvent` có
   nổi bọt trên host; markup nghe bằng `data-kit-event:done="$next.show()"`, dữ liệu ở
   `$event.detail`. `data-kit-event:<tên>` cũng nghe được sự kiện DOM ngoài danh sách viết tắt
   (`scroll`, `paste`); `data-kit-click`, `data-kit-input`… giữ làm dạng viết tắt. Component không
   phát sự kiện trùng tên sự kiện DOM gốc (`click`, `input`…).

3. **Một phần tử – một component.** Ghép bằng lồng nhau + sự kiện + alias.

4. **Version là hợp đồng.** Sửa lỗi không đổi những gì markup thấy (thuộc tính, phần tử, state,
   method, sự kiện) thì sửa tại chỗ — hash trong tên file lo cache. Đổi những thứ đó thì ra
   version mới.

5. **`data-kit-seed` chạy trước `init`.** State gieo từ markup đã có khi component khởi tạo, nên
   một mặc định suy ra từ nó (tab stop của tree) không phải dời vào `afterRender`.

## Lộ trình

1. ✅ 02/10 — revert hợp đồng JSON; `context.require` trong kernel, dùng ở các component có phần
   tử bắt buộc.
2. ✅ 02/10 — `context.emit` + `data-kit-event:<tên>` (kernel và verify pass phía Go: nhận
   directive, gắn kernel, kiểm biểu thức và modifier). `emit` trong lúc đang vẽ thì đợi vẽ xong.
   (Cùng ngày, sau khi bàn tên: component `visible` và sự kiện `seen` của nó được thay bằng
   directive `data-kit-intersect` — vào/ra màn hình là chuyện của ngữ pháp, đọc một chữ trên chính
   phần tử. apptop-impact giờ dùng `data-kit-intersect:once="start()"` trên host của nó.)
3. ✅ 02/10 — seed trước `init`: trong lượt gieo seed, các `init` được giữ lại và chạy sau khi mọi
   seed đã ghi. `tree` đặt tab stop từ `selected` ngay trong `init`.

4. ✅ 03/10 — `kit.context(this)`: method của component lấy lại đúng `context` mà `init` đã nhận
   (cùng một đối tượng), dù được gọi từ trong host, từ scope lồng bên trong, hay qua alias từ ngoài —
   không cần tự cất vào một ô `Symbol`. Thứ không phải `this` của component bị từ chối có tên.
   Kernel +101 byte gzip (11827 → 11928).

Kernel core sau ba bước: 12254 byte gzip, ngân sách 12288 — gần chạm trần. Sau đó (02/10) gỡ hai thứ
0 nơi dùng (`kit.fetchWithRetry`, thuộc tính `data-kit-progress`) và dời `data-kit-drag` ra file directive
riêng: 11827 byte, còn trống 461.
