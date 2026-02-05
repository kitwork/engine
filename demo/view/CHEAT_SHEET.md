# Kitwork Render Engine Syntax Guide

Vì Logic là JS, Template Engine được thiết kế để cảm giác "Gần gũi với JS" nhất có thể, nhưng chạy với tốc độ Go Native.

## 1. Truy cập Biến (Variable Access)
- **Local Variable**: `{{ user.name }}`
  - Không cần `test.user.name`, truy cập thẳng.
  - Tự động Escape HTML (An toàn XSS).
- **Global Variable (Root)**: `{{ $currentUser }}` hoặc `{{ $.config.siteName }}`
  - Dùng `$` để gọi biến từ Root Data bất kể đang ở vòng lặp nào.
- **Raw HTML**: `{{ raw(htmlContent) }}`
  - In nguyên gốc, không Escape (dùng cho Rich Text).

## 2. Vòng Lặp (Loops)
Hỗ trợ cú pháp hiện đại giống Python/Swift/Vue.

### Value Only (Gọn nhất, khuyên dùng)
```html
{{ for user in users }}
   <li>{{ user.name }}</li>
{{ end }}
```

### Key & Value (Tuple Syntax)
```html
{{ for (index, user) in users }}
   <li>#{{ index }}: {{ user.name }}</li>
{{ end }}
```
*Lưu ý: Index là 0-based.*


## 3. Điều Kiện (Conditionals)
Hỗ trợ so sánh lỏng (Loose comparison) giống JS.
```html
{{ if user.role == "admin" }}
    <button>Delete</button>
{{ else }}
    <span>View Only</span>
{{ end }}
```

## 4. Các tiện ích JS-like (Built-in)
Hệ thống tự động map các thuộc tính quen thuộc của JS:
- `{{ list.length }}`: Độ dài mảng.
- `{{ user.html() }}`: Nếu object có phương thức `html()` trả về Safe string.
