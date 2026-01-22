# Kitwork VM Architecture Specification

Tài liệu này mô tả thiết kế kỹ thuật của Kitwork Virtual Machine (KVM).

## 1. Kiến trúc máy thực thi
KVM sử dụng kiến trúc **Stack-based VM** (mô hình giống như JVM hoặc Lua 5.0 trở về trước) vì tính đơn giản trong biên dịch và khả năng di động cao.

- **Stack:** Một mảng các `value.Value`. Mọi phép toán (Cộng, Nhân, Gọi hàm) đều lấy dữ liệu từ Stack và đẩy kết quả ngược lại Stack.
- **Instruction Pointer (IP):** Con trỏ trỏ đến vị trí lệnh hiện tại trong dãy Bytecode.
- **Environment Stack:** Lưu trữ các biến địa phương (Locals) và các biến đóng (Upvalues).

## 2. Cấu trúc Bytecode
Mã chương trình được biên dịch thành một mảng byte (`[]byte`). Mỗi lệnh (Instruction) bao gồm 1 byte Opcode, theo sau là các đối số (Operands) nếu có.

Ví dụ phép tính `10 + 20`:
```text
OP_CONSTANT 0  // Đẩy 10 (từ Constant Pool index 0) lên Stack
OP_CONSTANT 1  // Đẩy 20 (từ Constant Pool index 1) lên Stack
OP_ADD         // Lấy 2 giá trị, cộng lại, đẩy kết quả 30 lên Stack
```

## 3. Danh sách OpCode dự kiến
| OpCode | Operands | Mô tả |
| :--- | :--- | :--- |
| `OP_CONSTANT` | 2 bytes (Index) | Nạp hằng số từ Constant Pool lên stack |
| `OP_GET_GLOBAL`| 2 bytes (Index) | Lấy biến toàn cục theo tên |
| `OP_SET_GLOBAL`| 2 bytes (Index) | Gán biến toàn cục |
| `OP_ADD` / `OP_SUB` | None | Các phép toán số học |
| `OP_CALL` | 1 byte (ArgCount) | Gọi hàm với số lượng đối số xác định |
| `OP_JUMP` | 2 bytes (Offset) | Nhảy tới vị trí khác (cho If/Loop) |
| `OP_RETURN` | None | Trả về từ hàm |
| `OP_MEMBER` | 2 bytes (Index) | Truy cập thuộc tính (object.property) |

## 4. Constant Pool
Để tối ưu bộ nhớ, các giá trị tĩnh như chuỗi văn bản dài ("OrderSystem") hoặc số thực sẽ được lưu trong một bảng hằng số. Bytecode chỉ lưu lại chỉ số (Index) dẫn đến bảng này.

## 5. Tích hợp với Go (Interop)
VM sẽ giao tiếp với Go thông qua hệ thống `value.Value` hiện tại.
- Hàm Go sẽ được bọc (wrap) thành `OP_CALL` đặc biệt.
- Khi gặp một phương thức của Struct Go, VM sẽ thực hiện `Reference Lookup` tương tự như logic `Evaluator` hiện nay nhưng ở mức độ chỉ danh đã được tối ưu hóa.
