# Kitwork Standard Library Specification

Tài liệu này định nghĩa các API tiêu chuẩn cung cấp cho môi trường script JS của Kitwork.

## 1. Core Modules (Tích hợp sẵn)

### Console
Cung cấp các hàm in ấn cơ bản.
- `print(...args)`: In dữ liệu ra STDOUT (đã hiện thực).
- `debug(...args)`: In dữ liệu kèm theo timestamp và vị trí file.

### Worker (Linh hồn của Engine)
- `worker(config)`: Khởi tạo định nghĩa worker.
- `router(method, path)`: Định nghĩa endpoint.
- `retry(duration)`: Cấu hình cơ chế thử lại.

## 2. I/O Modules (Dự kiến)

### Module `fetch` (HTTP Client)
Cung cấp khả năng gọi API bên ngoài.
- `fetch(url, options)`: Trả về một Response object.
- Hỗ trợ methods: `GET`, `POST`, `JSON payload`.

### Module `sql` (Database Client)
Tích hợp trực tiếp với database của hệ thống.
- `sql.query(statement, args...)`: Trả về mảng dữ liệu.
- `sql.exec(statement, args...)`: Thực thi lệnh không lấy dữ liệu (Insert/Update).
- An toàn trước SQL Injection nhờ cơ chế `Prepared Statements` của Go.

## 3. Data Modules

### Module `json`
- `json.parse(string)`: Chuyển chuỗi thành Object JS.
- `json.stringify(object)`: Chuyển Object thành chuỗi JSON.

### Module `time`
- `time.now()`: Lấy thời gian hiện tại.
- `time.sleep(duration)`: Tạm dừng script (tận dụng `time.Sleep` của Go).

## 4. Bảo mật Standard Library
- **Safe Imports:** Chỉ các module được phép mới có thể được nạp vào script.
- **Go Context Awareness:** Mọi tác vụ I/O trong StdLib đều phải tuân thủ `context.Context` từ Go để có thể hủy bỏ (Cancel) khi cần thiết (ví dụ: request HTTP quá lâu).
