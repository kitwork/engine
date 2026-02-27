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
	Vars      map[string]value.Value // Module/Global scope
	Globals   map[string]value.Value // Engine Builtins
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
		Frames:    make([]Frame, 64), // Tối đa 64 tầng gọi hàm (đủ dùng)
	}
	// Khởi tạo Frame gốc (Main entry)
	vm.FrameIdx = 0
	vm.Frames[0] = Frame{IP: 0, Vars: make(map[string]value.Value)}
	return vm
}

func (vm *Runtime) FastReset(code []byte, constants []value.Value, globals map[string]value.Value) {
	vm.Bytecode = code
	vm.Constants = constants
	vm.Stack = vm.Stack[:0]
	vm.Globals = globals
	vm.FrameIdx = 0
	// Reset Frame 0
	vm.Frames[0].IP = 0
	vm.Frames[0].Defers = vm.Frames[0].Defers[:0]
	for k := range vm.Frames[0].Vars {
		delete(vm.Frames[0].Vars, k)
	}
	// vm.Vars stores reused builtins, do not clear them.
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
