package opcode

// Opcode đại diện cho một chỉ thị bytecode trong Kitwork Engine (stack-based VM).
type Opcode uint8

const (
	// =====================
	// DATA FLOW
	// =====================

	PUSH  Opcode = iota // Đẩy literal/hằng số lên Stack
	POP                 // Loại bỏ giá trị trên đỉnh Stack
	LOAD                // Tải giá trị từ Vars/Context lên Stack
	STORE               // Lưu giá trị từ Stack vào Vars/Context
	GET                 // Lấy thuộc tính/phần tử (obj.prop)

	// =====================
	// ARITHMETIC
	// =====================

	ADD // Cộng (+)
	SUB // Trừ (-)
	MUL // Nhân (*)
	DIV // Chia (/)

	// =====================
	// CONTROL FLOW
	// =====================

	COMPARE // So sánh hai giá trị trên Stack (hành vi do operand quyết định)
	JUMP    // Nhảy không điều kiện đến instruction pointer
	UNLESS  // Nhảy nếu giá trị Boolean trên Stack là false = IF_FALSE / IF_NOT
	HALT    // Dừng thực thi ngay lập tức

	// =====================
	// STRUCTURE
	// =====================

	MAKE // Tạo cấu trúc dữ liệu mới (Map, Array, ...)
	SET  // Gán giá trị vào cấu trúc (key/index → value)

	// =====================
	// EXECUTION
	// =====================

	CALL   // Gọi hàm host / builtin
	INVOKE // Gọi phương thức đối tượng (obj.method)
	LAMBDA // Tạo hoặc kích hoạt lambda/function
	RETURN // Kết thúc hàm và quay về caller
)
