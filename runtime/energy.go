package runtime

// Cost đại diện cho đơn vị năng lượng tiêu thụ thực tế trên hạ tầng.
type Cost uint64

// Table là bản đồ chi phí năng lượng được chuẩn soát dựa trên CPU Cycles và Memory Latency.
// Thiết kế mảng 256 phần tử đảm bảo tra cứu O(1) và tối ưu L1 Instruction Cache.
var Table = [256]Cost{
	// =====================
	// DATA FLOW
	// =====================
	PUSH:  1,  // Thao tác Stack (L1 Cache)
	POP:   1,  // Loại bỏ đỉnh Stack
	LOAD:  2,  // Đọc từ Slot (Memory Read)
	STORE: 2,  // Ghi vào Slot (Memory Write)
	GET:   12, // Đắt: Lookup thuộc tính (Hash/String overhead)
	DUP:   1,  // Thao tác Stack (L1 Cache)

	// =====================
	// ARITHMETIC
	// =====================
	ADD: 2,  // Phép cộng (Cơ bản nhất)
	SUB: 2,  // Phép trừ
	MUL: 6,  // Nhân: Tốn nhiều bóng bán dẫn và chu kỳ CPU hơn
	DIV: 25, // Rất đắt: Gây nghẽn Pipeline và rủi ro lỗi chia cho 0

	// =====================
	// LOGIC
	// =====================
	AND: 1, // Khuyến khích dùng để ngắt logic sớm (Short-circuit)
	OR:  1,
	NOT: 1,

	// =====================
	// CONTROL FLOW
	// =====================
	COMPARE: 4,  // So sánh giá trị và thiết lập flag
	JUMP:    3,  // Nhảy IP (Có rủi ro Branch Misprediction)
	TRUE:    3,  // Nhảy điều kiện
	FALSE:   3,  // Nhảy điều kiện
	ITER:    15, // Lệnh phức hợp: Phí cao vì gộp nhiều hành vi
	HALT:    0,  // Tự vệ hệ thống: Miễn phí
	YIELD:   0,  // Tự nguyện nhường tài nguyên: Miễn phí

	// =====================
	// STRUCTURE
	// =====================
	MAKE: 80, // Đắt nhất nội bộ: Quản lý Memory Pool & Allocation
	SET:  10, // Gán giá trị vào cấu trúc phức tạp

	// =====================
	// EXECUTION
	// =====================
	CALL:       150, // Phí hạ tầng: Context Switch sang Go Host
	INVOKE:     150, // Gọi phương thức (Dynamic Dispatch)
	LAMBDA:     60,  // Khởi tạo ngữ cảnh hàm con
	RETURN:     5,   // Thu hồi Frame và dọn dẹp Stack
	DEFER:      10,  // Bảo hiểm hệ thống: Đảm bảo thực thi dọn dẹp
	SPAWN:      200, // Đắt: Tạo Goroutine và Context mới
	POPFIN:     1,   // Như POP; phần finalize (nếu có) tính phí qua chính request/handler
	POPFINSOFT: 1,   // Như POP; finalize soft (chỉ request có handler)
}
