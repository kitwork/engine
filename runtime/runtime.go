package runtime

import (
	"context"

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
	Context   context.Context // Request execution context (for cancellation)
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

// FastReset loads a new program into this VM. It runs MID-REQUEST — a folder router FastResets the
// VM onto each folder's bytecode before running its lambdas (work.execTree) — so it must NOT clear
// Context: that would drop the request's cancellation signal the moment the handler is loaded.
// Clearing Context is pool hygiene and belongs to ResetForPool (STABILITY.md §1).
func (vm *VM) FastReset(code []byte, constants []value.Value, globals map[string]value.Value, sourceMap []int32) {
	vm.Bytecode = code
	vm.Constants = constants
	clear(vm.Stack)
	vm.Stack = vm.Stack[:0]
	vm.Globals = globals
	vm.SourceMap = sourceMap
	vm.FrameIdx = 0
	vm.Energy = 0

	root := &vm.Frames[0]
	if root.captured {
		// A top-level closure still owns the old map. Detach the VM instead of
		// clearing data that escaped with that closure.
		vm.Vars = make(map[string]value.Value)
		root.captured = false
	} else {
		clear(vm.Vars)
	}

	// Đồng bộ lại Frame gốc
	root.IP = 0
	root.Vars = vm.Vars
	root.Fn = nil
	clear(root.Defers)
	root.Defers = root.Defers[:0]
	root.StackBase = 0
}

// ResetForPool drops tenant-owned references before a VM becomes visible to
// another app. Captured frame maps are detached, never cleared.
func (vm *VM) ResetForPool() {
	vm.FastReset(nil, nil, nil, nil)
	vm.Context = nil // a pooled VM must never inherit the previous owner's request context
	vm.Builtins = nil
	vm.MaxEnergy = 0
	vm.Spawner = nil

	for i := 1; i < len(vm.Frames); i++ {
		f := &vm.Frames[i]
		f.IP = 0
		f.Fn = nil
		clear(f.Defers)
		f.Defers = f.Defers[:0]
		f.StackBase = 0
		if f.captured {
			f.Vars = nil
		} else {
			clear(f.Vars)
		}
		f.captured = false
	}
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
