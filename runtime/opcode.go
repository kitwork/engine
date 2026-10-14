package runtime

// Opcode đại diện cho một chỉ thị bytecode trong Kitwork Engine (stack-based VM).
type Opcode uint8

const (
	// =====================
	// DATA FLOW
	// =====================

	PUSH    Opcode = iota // Đẩy literal/hằng số lên Stack
	POP                   // Loại bỏ giá trị trên đỉnh Stack
	LOAD                  // Tải giá trị từ Vars/Context lên Stack (theo index)
	STORE                 // Lưu giá trị từ Stack vào Vars/Context (theo index)
	GET                   // Lấy thuộc tính/phần tử (obj.prop)
	DUP                   // Sao chép giá trị trên cùng của stack
	BUILTIN               // Tải hàm hệ thống theo index (Siêu nhanh)

	// =====================
	// ARITHMETIC
	// =====================

	ADD // Cộng (+)
	SUB // Trừ (-)
	MUL // Nhân (*)
	DIV // Chia (/)

	// =====================
	// LOGIC
	// =====================

	AND // Logic AND: chọn toán hạng khi hai vế đã tính (compiler chỉ phát khi vế phải là hằng hay tên; && đoản mạch bằng DUP/FALSE/POP)
	OR  // Logic OR: như AND (|| đoản mạch bằng DUP/TRUE/POP)
	NOT // Logic NOT (Đảo ngược boolean)

	// =====================
	// CONTROL FLOW
	// =====================

	COMPARE // So sánh (==, !=, <, >, ...) - hành vi định nghĩa qua operand
	JUMP    // Nhảy không điều kiện đến Instruction Pointer
	TRUE    // Nhảy nếu đỉnh Stack là True
	FALSE   // Nhảy nếu đỉnh Stack là False
	ITER    // Vòng lặp tối ưu: Kiểm tra + Lấy phần tử + Nhảy
	HALT    // Dừng thực thi ngay lập tức (Cưỡng bức)
	YIELD   // Tạm dừng thực thi (Nhường tài nguyên/Hồi phục)

	// =====================
	// STRUCTURE
	// =====================

	MAKE  // Tạo cấu trúc dữ liệu mới (Map, Array từ Pool)
	SET   // Gán giá trị vào cấu trúc (key/index → value)
	MERGE // Gộp các phần tử từ object nguồn vào object đích (Spread)

	// =====================
	// EXECUTION
	// =====================

	CALL   // Gọi hàm host (Go) / Builtin
	INVOKE // Gọi phương thức đối tượng (obj.method)
	LAMBDA // Khởi tạo/Kích hoạt hàm ẩn danh
	RETURN // Kết thúc hàm, dọn dép stack frame
	DEFER  // Đảm bảo dọn dẹp/hoàn trả năng lượng khi kết thúc
	SPAWN  // Chạy lambda trong Goroutine riêng (Fire & Forget)

	// Appended opcodes keep the numeric values of the original instruction set stable.
	MOD    // Remainder (%)
	COMMIT // Commit a deferred effect at an expression boundary; leave it on the stack.

	_RESERVED // Reserved former POPFINSOFT numeric slot; never execute or reuse it.

	// Wide forms (bytecode 3, 14/10/2026): the same instruction with a three-byte operand, for a
	// constant index above 65,535 or a jump in a program longer than 65,535 bytes. A program that fits
	// two-byte operands never holds one (the verifier refuses it), so it is written as in bytecode 2.
	PUSH_WIDE
	LOAD_WIDE
	STORE_WIDE
	JUMP_WIDE
	TRUE_WIDE
	FALSE_WIDE
	ITER_WIDE

	_LIMIT // One past the final assigned opcode slot; never execute it.
)

// Wide is the three-byte form of PUSH, LOAD, STORE, JUMP, TRUE, FALSE and ITER; any other opcode is
// its own.
func (op Opcode) Wide() Opcode {
	switch op {
	case PUSH:
		return PUSH_WIDE
	case LOAD:
		return LOAD_WIDE
	case STORE:
		return STORE_WIDE
	case JUMP:
		return JUMP_WIDE
	case TRUE:
		return TRUE_WIDE
	case FALSE:
		return FALSE_WIDE
	case ITER:
		return ITER_WIDE
	}
	return op
}

// Narrow is the instruction a wide form is: PUSH for PUSH_WIDE. Any other opcode is its own.
func (op Opcode) Narrow() Opcode {
	switch op {
	case PUSH_WIDE:
		return PUSH
	case LOAD_WIDE:
		return LOAD
	case STORE_WIDE:
		return STORE
	case JUMP_WIDE:
		return JUMP
	case TRUE_WIDE:
		return TRUE
	case FALSE_WIDE:
		return FALSE
	case ITER_WIDE:
		return ITER
	}
	return op
}

// IsWide reports a wide form.
func (op Opcode) IsWide() bool {
	return op != op.Narrow()
}
