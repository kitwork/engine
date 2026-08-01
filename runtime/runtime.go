package runtime

import (
	"context"

	"github.com/kitwork/engine/value"
)

const (
	initialStackCapacity   = 1_024
	maxPooledStackCapacity = 4_096
	maxReusableVariables   = 1_024
	maxReusableDefers      = 64
	maxReusableGlobals     = 1_024
	maxReusableBuiltins    = 256
)

// Frame đại diện cho một khung thực thi (Activation Record)
type Frame struct {
	IP     int
	LastIP int
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
	program   *Program
	Stack     []value.Value
	Vars      map[string]value.Value // Biến của từng Request (Cho phép ghi)
	Globals   map[string]value.Value // Chung cho toàn bộ Host (Chỉ đọc)
	Builtins  []value.Value          // Mảng các hàm hệ thống (Siêu nhanh - Index lookup)
	Frames    []Frame                // Call Stack
	FrameIdx  int                    // Hiện tại đang ở Frame nào
	Energy    uint64                 // Năng lượng tiêu thụ
	MaxEnergy uint64                 // Giới hạn năng lượng
	Spawner   func(s *value.Lambda)

	instructions   uint64
	frameHighWater int

	reusableGlobals  map[string]value.Value
	reusableBuiltins []value.Value
	hostStateOwned   bool
}

// VMStats is a point-in-time execution snapshot. Read it after execution; a VM
// is intentionally single-owner and is not safe for concurrent inspection.
type VMStats struct {
	Instructions   uint64
	Energy         uint64
	StackDepth     int
	StackCapacity  int
	FrameDepth     int
	PeakFrameDepth int
}

func New(program *Program) *VM {
	vm := &VM{
		program: program,
		Stack:   make([]value.Value, 0, initialStackCapacity),
		Vars:    make(map[string]value.Value),
		Globals: make(map[string]value.Value),
		Frames:  make([]Frame, 64), // Tối đa 64 tầng gọi hàm (đủ dùng)
	}
	// Khởi tạo Frame gốc (Main entry)
	vm.FrameIdx = 0
	vm.Frames[0] = Frame{IP: 0, Vars: vm.Vars, StackBase: 0} // TRANG BỊ VŨ KHÍ: Frame 0 chính là vm.Vars
	vm.Frames[0].LastIP = -1
	return vm
}

// FastReset loads a new Program and caller-owned globals. It does not clear the
// execution context; pool hygiene belongs to ResetForPool. Production request
// paths use PrepareHostState followed by FastResetPrepared so container
// ownership remains explicit.
func (vm *VM) FastReset(program *Program, globals map[string]value.Value) {
	vm.resetExecution(program)
	vm.Globals = globals
	vm.hostStateOwned = false
}

// FastResetPrepared resets execution while retaining the VM-owned host state
// installed by PrepareHostState.
func (vm *VM) FastResetPrepared(program *Program) {
	vm.resetExecution(program)
}

func (vm *VM) resetExecution(program *Program) {
	vm.program = program
	vm.resetStack()
	vm.FrameIdx = 0
	vm.Energy = 0
	vm.instructions = 0

	for index := 1; index <= vm.frameHighWater; index++ {
		resetFrame(&vm.Frames[index])
	}
	vm.frameHighWater = 0

	root := &vm.Frames[0]
	if root.captured || len(vm.Vars) > maxReusableVariables {
		// A top-level closure still owns the old map. Detach the VM instead of
		// clearing data that escaped with that closure.
		vm.Vars = make(map[string]value.Value)
		root.captured = false
	} else {
		clear(vm.Vars)
	}

	// Đồng bộ lại Frame gốc
	root.IP = 0
	root.LastIP = -1
	root.Vars = vm.Vars
	root.Fn = nil
	resetDefers(root)
	root.StackBase = 0
}

// PrepareHostState copies one owner's host environment into VM-owned reusable
// storage. ResetForPool clears every value before retaining only the empty
// containers, so the pool can reduce allocations without exposing references
// from the previous app.
func (vm *VM) PrepareHostState(
	globals map[string]value.Value,
	builtins []value.Value,
) {
	if vm == nil {
		return
	}

	ownedGlobals := vm.reusableGlobals
	vm.reusableGlobals = nil
	if ownedGlobals == nil {
		ownedGlobals = make(map[string]value.Value, len(globals)+1)
	} else {
		clear(ownedGlobals)
	}
	for name, item := range globals {
		ownedGlobals[name] = item
	}
	vm.Globals = ownedGlobals

	ownedBuiltins := vm.reusableBuiltins
	vm.reusableBuiltins = nil
	if cap(ownedBuiltins) < len(builtins) {
		ownedBuiltins = make([]value.Value, len(builtins))
	} else {
		ownedBuiltins = ownedBuiltins[:len(builtins)]
	}
	copy(ownedBuiltins, builtins)
	vm.Builtins = ownedBuiltins
	vm.hostStateOwned = true
}

// ResetForPool drops tenant-owned references before a VM becomes visible to
// another app. Captured frame maps are detached, never cleared.
func (vm *VM) ResetForPool() {
	globals := vm.Globals
	builtins := vm.Builtins
	hostStateOwned := vm.hostStateOwned
	vm.resetExecution(nil)
	vm.Globals = nil
	vm.Context = nil // a pooled VM must never inherit the previous owner's request context
	vm.Builtins = nil
	vm.MaxEnergy = 0
	vm.Spawner = nil
	vm.hostStateOwned = false

	if hostStateOwned && len(globals) <= maxReusableGlobals {
		clear(globals)
		vm.reusableGlobals = globals
	} else if hostStateOwned {
		vm.reusableGlobals = nil
	}
	if hostStateOwned && cap(builtins) <= maxReusableBuiltins {
		clear(builtins)
		vm.reusableBuiltins = builtins[:0]
	} else if hostStateOwned {
		vm.reusableBuiltins = nil
	}

	for i := 1; i < len(vm.Frames); i++ {
		resetFrame(&vm.Frames[i])
	}
}

// Program reports the immutable program currently loaded by the VM.
func (vm *VM) Program() *Program {
	if vm == nil {
		return nil
	}
	return vm.program
}

func (vm *VM) Stats() VMStats {
	if vm == nil {
		return VMStats{}
	}
	frameDepth := 0
	if vm.FrameIdx >= 0 {
		frameDepth = vm.FrameIdx + 1
	}
	return VMStats{
		Instructions:   vm.instructions,
		Energy:         vm.Energy,
		StackDepth:     len(vm.Stack),
		StackCapacity:  cap(vm.Stack),
		FrameDepth:     frameDepth,
		PeakFrameDepth: vm.frameHighWater + 1,
	}
}

func (vm *VM) Stop() {
	vm.FrameIdx = -1
}

func (vm *VM) resetStack() {
	if cap(vm.Stack) > maxPooledStackCapacity {
		vm.Stack = make([]value.Value, 0, initialStackCapacity)
		return
	}
	clear(vm.Stack)
	vm.Stack = vm.Stack[:0]
}

func resetFrame(frame *Frame) {
	frame.IP = 0
	frame.LastIP = -1
	frame.Fn = nil
	frame.StackBase = 0

	if frame.captured || len(frame.Vars) > maxReusableVariables {
		frame.Vars = nil
	} else {
		clear(frame.Vars)
	}
	frame.captured = false
	resetDefers(frame)
}

func resetDefers(frame *Frame) {
	if cap(frame.Defers) > maxReusableDefers {
		frame.Defers = nil
		return
	}
	clear(frame.Defers)
	frame.Defers = frame.Defers[:0]
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
