package energy

import "github.com/kitwork/engine/opcode"

// Cost đại diện cho đơn vị năng lượng tiêu thụ thực tế trên hạ tầng.
type Cost uint64

// Table là bản đồ chi phí năng lượng được chuẩn soát dựa trên CPU Cycles và Memory Latency.
// Thiết kế mảng 256 phần tử đảm bảo tra cứu O(1) và tối ưu L1 Instruction Cache.
var Table = [256]Cost{
	// =====================
	// DATA FLOW
	// =====================
	opcode.PUSH:  1,  // Thao tác Stack (L1 Cache)
	opcode.POP:   1,  // Loại bỏ đỉnh Stack
	opcode.LOAD:  2,  // Đọc từ Slot (Memory Read)
	opcode.STORE: 2,  // Ghi vào Slot (Memory Write)
	opcode.GET:   12, // Đắt: Lookup thuộc tính (Hash/String overhead)
	opcode.DUP:   1,  // Thao tác Stack (L1 Cache)

	// =====================
	// ARITHMETIC
	// =====================
	opcode.ADD: 2,  // Phép cộng (Cơ bản nhất)
	opcode.SUB: 2,  // Phép trừ
	opcode.MUL: 6,  // Nhân: Tốn nhiều bóng bán dẫn và chu kỳ CPU hơn
	opcode.DIV: 25, // Rất đắt: Gây nghẽn Pipeline và rủi ro lỗi chia cho 0

	// =====================
	// LOGIC
	// =====================
	opcode.AND: 1, // Khuyến khích dùng để ngắt logic sớm (Short-circuit)
	opcode.OR:  1,
	opcode.NOT: 1,

	// =====================
	// CONTROL FLOW
	// =====================
	opcode.COMPARE: 4,  // So sánh giá trị và thiết lập flag
	opcode.JUMP:    3,  // Nhảy IP (Có rủi ro Branch Misprediction)
	opcode.TRUE:    3,  // Nhảy điều kiện
	opcode.FALSE:   3,  // Nhảy điều kiện
	opcode.ITER:    15, // Lệnh phức hợp: Phí cao vì gộp nhiều hành vi
	opcode.HALT:    0,  // Tự vệ hệ thống: Miễn phí
	opcode.YIELD:   0,  // Tự nguyện nhường tài nguyên: Miễn phí

	// =====================
	// STRUCTURE
	// =====================
	opcode.MAKE: 80, // Đắt nhất nội bộ: Quản lý Memory Pool & Allocation
	opcode.SET:  10, // Gán giá trị vào cấu trúc phức tạp

	// =====================
	// EXECUTION
	// =====================
	opcode.CALL:   150, // Phí hạ tầng: Context Switch sang Go Host
	opcode.INVOKE: 150, // Gọi phương thức (Dynamic Dispatch)
	opcode.LAMBDA: 60,  // Khởi tạo ngữ cảnh hàm con
	opcode.RETURN: 5,   // Thu hồi Frame và dọn dẹp Stack
	opcode.DEFER:  10,  // Bảo hiểm hệ thống: Đảm bảo thực thi dọn dẹp
	opcode.SPAWN:  200, // Đắt: Tạo Goroutine và Context mới
}
