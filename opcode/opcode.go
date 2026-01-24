package opcode

// Opcode đại diện cho một chỉ thị bytecode trong Kitwork Engine (stack-based VM).
type Opcode uint8

const (
	// =====================
	// DATA FLOW
	// =====================

	PUSH  Opcode = iota // Đẩy literal/hằng số lên Stack
	POP                 // Loại bỏ giá trị trên đỉnh Stack
	LOAD                // Tải giá trị từ Vars/Context lên Stack (theo index)
	STORE               // Lưu giá trị từ Stack vào Vars/Context (theo index)
	GET                 // Lấy thuộc tính/phần tử (obj.prop)
	DUP                 // Sao chép giá trị trên cùng của stack

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

	AND // Logic AND (Hỗ trợ short-circuit)
	OR  // Logic OR (Hỗ trợ short-circuit)
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

	MAKE // Tạo cấu trúc dữ liệu mới (Map, Array từ Pool)
	SET  // Gán giá trị vào cấu trúc (key/index → value)

	// =====================
	// EXECUTION
	// =====================

	CALL   // Gọi hàm host (Go) / Builtin
	INVOKE // Gọi phương thức đối tượng (obj.method)
	LAMBDA // Khởi tạo/Kích hoạt hàm ẩn danh
	RETURN // Kết thúc hàm, dọn dép stack frame
	DEFER  // Đảm bảo dọn dẹp/hoàn trả năng lượng khi kết thúc
	SPAWN  // Chạy lambda trong Goroutine riêng (Fire & Forget)
)
