# Kitwork Engine Roadmap: From AST to Bytecode VM

Dự án này được hướng tới việc xây dựng một Virtual Machine (VM) hiệu năng cao, nhúng bên trong Go để thực thi các kịch bản tự động hóa (Workflow) một cách an toàn và mạnh mẽ.

## Giai đoạn 1: Chuyển đổi sang Kiến trúc Bytecode (Hiện tại)
Mục tiêu: Thay thế `Evaluator` (AST Interpreter) bằng `Compiler` + `VM`.

- [ ] **Instruction Set (Opcode):** Thiết kế bộ lệnh stack-based (ví dụ: `PUSH`, `POP`, `ADD`, `CALL`, `GET_VAR`).
- [ ] **Bytecode Compiler:** Xây dựng bộ biên dịch duyệt AST và phát sinh mã Bytecode.
- [ ] **Virtual Machine (VM):** Hiện thực hóa vòng lặp thực thi lệnh (Fetch-Decode-Execute) với Stack Register.
- [ ] **Constant Pool:** Quản lý các giá trị hằng số (Chuỗi, Số thực) tách biệt với mã lệnh.

## Giai đoạn 2: Hiện thực hóa Standard Library (Mở rộng sức mạnh)
Mục tiêu: Cung cấp các công cụ để script có thể tương tác với thế giới bên ngoài.

- [ ] **Module HTTP:** Hiện thực hóa `fetch` (GET/POST/PUT/DELETE) hỗ trợ JSON.
- [ ] **Module Database:** Hỗ trợ cơ chế Database Driver (SQL) để script có thể truy vấn dữ liệu.
- [ ] **Module JSON/Formatting:** Native support cho việc parse và stringify dữ liệu phức tạp.
- [ ] **Module Time/Cron:** Xử lý thời gian và lập lịch tác vụ sâu bên trong VM.

## Giai đoạn 3: Tính năng nâng cao & Hiệu năng
Mục tiêu: Cạnh tranh với các nhúng engine khác (như Lua).

- [ ] **Serialization (.kbc):** Cho phép lưu trữ Bytecode đã biên dịch ra file nhị phân để chạy ngay lập tức.
- [ ] **Async/Await Support:** Tận dụng Goroutines để xử lý các tác vụ I/O không chặn.
- [ ] **Resource Limiting (Sandboxing):** Giới hạn CPU (số lượng instruction) và RAM cho mỗi script.
- [ ] **Native Bridge API:** Cung cấp cách thức dễ dàng nhất để người phát triển Go có thể đăng ký hàm vào VM.

## Giai đoạn 4: Hệ sinh thái & Công cụ
Mục tiêu: Giúp việc phát triển với Kitwork Engine trở nên dễ dàng.

- [ ] **CLI Tool:** Bộ công cụ dòng lệnh để biên dịch và chạy script.
- [ ] **LSP Support:** Hỗ trợ gợi ý code cho VS Code dựa trên DSL của Engine.
- [ ] **Documentation Engine:** Tự động tạo tài liệu từ các Prototype đã đăng ký.
