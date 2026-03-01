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
}

type Runtime struct {
	Bytecode  []byte
	Constants []value.Value
	Stack     []value.Value
	Vars      map[string]value.Value // Biến của từng Request (Cho phép ghi)
	Globals   map[string]value.Value // Chung cho toàn bộ Host (Chỉ đọc)
	Builtins  []value.Value          // Mảng các hàm hệ thống (Siêu nhanh - Index lookup)
	Frames    []Frame                // Call Stack
	FrameIdx  int                    // Hiện tại đang ở Frame nào
	Energy    uint64                 // Năng lượng tiêu thụ
	Spawner   func(s *value.Lambda)
}

func New(code []byte, constants []value.Value) *Runtime {
	vm := &Runtime{
		Bytecode:  code,
		Constants: constants,
		Stack:     make([]value.Value, 0, 1024),
		Vars:      make(map[string]value.Value),
		Globals:   make(map[string]value.Value),
		Frames:    make([]Frame, 64), // Tối đa 64 tầng gọi hàm (đủ dùng)
	}
	// Khởi tạo Frame gốc (Main entry)
	vm.FrameIdx = 0
	vm.Frames[0] = Frame{IP: 0, Vars: vm.Vars} // TRANG BỊ VŨ KHÍ: Frame 0 chính là vm.Vars
	return vm
}

func (vm *Runtime) FastReset(code []byte, constants []value.Value, globals map[string]value.Value) {
	vm.Bytecode = code
	vm.Constants = constants
	vm.Stack = vm.Stack[:0]
	vm.Globals = globals
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
}

func (vm *Runtime) Stop() {
	vm.FrameIdx = -1
}

// Helper methods for Stack manipulation
func (vm *Runtime) push(v value.Value) { vm.Stack = append(vm.Stack, v) }

func (vm *Runtime) pop() value.Value {
	if len(vm.Stack) == 0 {
		return value.Value{K: value.Nil}
	}
	v := vm.Stack[len(vm.Stack)-1]
	vm.Stack = vm.Stack[:len(vm.Stack)-1]
	// fmt.Printf("VM Pop: %v\n", v) // Debug hook enabled
	return v
}

func (vm *Runtime) peek() value.Value {
	if len(vm.Stack) == 0 {
		return value.Value{K: value.Nil}
	}
	return vm.Stack[len(vm.Stack)-1]
}
