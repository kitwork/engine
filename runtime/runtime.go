package runtime

import (
	"github.com/kitwork/engine/value"
)

// Frame đại diện cho một khung thực thi (Activation Record)
type Frame struct {
	IP     int
	Vars   map[string]value.Value // Local scope
	Fn     *value.Lambda          // Hàm đang được thực thi
	Defers []*value.Lambda        // Deferred functions
	// captured = true khi Vars của frame này đã bị một closure giữ tham chiếu
	// (Scope: f.Vars). Lúc đó slot KHÔNG được tái dùng/xoá map cũ — phải cấp map
	// mới — nếu không closure sẽ mất biến sau khi frame return (bug closure).
	captured  bool
	StackBase int // Stack depth when the function call started
}

type VM struct {
	Bytecode  []byte
	Constants []value.Value
	Stack     []value.Value
	Vars      map[string]value.Value // Biến của từng Request (Cho phép ghi)
	Globals   map[string]value.Value // Chung cho toàn bộ Host (Chỉ đọc)
	Builtins  []value.Value          // Mảng các hàm hệ thống (Siêu nhanh - Index lookup)
	Frames    []Frame                // Call Stack
	FrameIdx  int                    // Hiện tại đang ở Frame nào
	Energy    uint64                 // Năng lượng tiêu thụ
	MaxEnergy uint64                 // Giới hạn năng lượng
	SourceMap []int32                // Bản đồ dòng lệnh nguồn (IP -> Line)
	Spawner   func(s *value.Lambda)
}

func New(code []byte, constants []value.Value) *VM {
	vm := &VM{
		Bytecode:  code,
		Constants: constants,
		Stack:     make([]value.Value, 0, 1024),
		Vars:      make(map[string]value.Value),
		Globals:   make(map[string]value.Value),
		Frames:    make([]Frame, 64), // Tối đa 64 tầng gọi hàm (đủ dùng)
	}
	// Khởi tạo Frame gốc (Main entry)
	vm.FrameIdx = 0
	vm.Frames[0] = Frame{IP: 0, Vars: vm.Vars, StackBase: 0} // TRANG BỊ VŨ KHÍ: Frame 0 chính là vm.Vars
	return vm
}

func (vm *VM) FastReset(code []byte, constants []value.Value, globals map[string]value.Value, sourceMap []int32) {
	vm.Bytecode = code
	vm.Constants = constants
	vm.Stack = vm.Stack[:0]
	vm.Globals = globals
	vm.SourceMap = sourceMap
	vm.FrameIdx = 0
	vm.Energy = 0

	// RECYCLE MAP: Trình dọn dẹp Map cực nhanh không tốn RAM
	for k := range vm.Vars {
		delete(vm.Vars, k)
	}

	// Đồng bộ lại Frame gốc
	vm.Frames[0].IP = 0
	vm.Frames[0].Vars = vm.Vars
	vm.Frames[0].Defers = vm.Frames[0].Defers[:0]
	vm.Frames[0].StackBase = 0
}

func (vm *VM) Stop() {
	vm.FrameIdx = -1
}

// Helper methods for Stack manipulation
func (vm *VM) push(v value.Value) { vm.Stack = append(vm.Stack, v) }

func (vm *VM) pop() value.Value {
	var base int
	if vm.FrameIdx >= 0 && vm.FrameIdx < len(vm.Frames) {
		base = vm.Frames[vm.FrameIdx].StackBase
	}
	if len(vm.Stack) <= base {
		return value.Value{K: value.Nil}
	}
	v := vm.Stack[len(vm.Stack)-1]
	vm.Stack = vm.Stack[:len(vm.Stack)-1]
	return v
}

func (vm *VM) peek() value.Value {
	var base int
	if vm.FrameIdx >= 0 && vm.FrameIdx < len(vm.Frames) {
		base = vm.Frames[vm.FrameIdx].StackBase
	}
	if len(vm.Stack) <= base {
		return value.Value{K: value.Nil}
	}
	return vm.Stack[len(vm.Stack)-1]
}
