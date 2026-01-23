package vm

import (
	"fmt"

	"github.com/kitwork/engine/opcode"
	"github.com/kitwork/engine/value"
)

// Frame đại diện cho một khung thực thi (Activation Record)
type Frame struct {
	IP   int
	Vars map[string]value.Value // Local scope
	Fn   *value.ScriptFunction  // Hàm đang được thực thi
}

type VM struct {
	Bytecode  []byte
	Constants []value.Value
	Stack     []value.Value
	Vars      map[string]value.Value // Module/Global scope
	Globals   map[string]value.Value // Engine Builtins
	Frames    []Frame                // Call Stack
	FrameIdx  int                    // Hiện tại đang ở Frame nào
}

func NewVM(code []byte, constants []value.Value) *VM {
	vm := &VM{
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

func (vm *VM) FastReset(code []byte, constants []value.Value, globals map[string]value.Value) {
	vm.Bytecode = code
	vm.Constants = constants
	vm.Stack = vm.Stack[:0]
	vm.Globals = globals
	vm.FrameIdx = 0
	// Reset Frame 0
	vm.Frames[0].IP = 0
	for k := range vm.Frames[0].Vars {
		delete(vm.Frames[0].Vars, k)
	}
	for k := range vm.Vars {
		delete(vm.Vars, k)
	}
}

func (vm *VM) Run() value.Value {
	for vm.FrameIdx >= 0 {
		f := &vm.Frames[vm.FrameIdx]
		if f.IP >= len(vm.Bytecode) {
			if vm.FrameIdx == 0 {
				break
			}
			vm.FrameIdx--
			continue
		}

		op := opcode.Opcode(vm.Bytecode[f.IP])
		f.IP++

		// DEBUG
		// fmt.Printf("[VM] Frame:%d IP:%d OP:%d StackSize:%d\n", vm.FrameIdx, f.IP-1, op, len(vm.Stack))

		switch op {
		case opcode.PUSH:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			val := vm.Constants[idx]
			// AUTO-CLOSURE: Nếu là hàm script, ta "đóng gói" (capture) các biến local hiện tại
			if sFn, ok := val.V.(*value.ScriptFunction); ok {
				closure := &value.ScriptFunction{
					Address:    sFn.Address,
					ParamNames: sFn.ParamNames,
					Scope:      make(map[string]value.Value),
				}
				// Capture local variables from current frame
				for k, v := range f.Vars {
					closure.Scope[k] = v
				}
				vm.push(value.New(closure))
			} else {
				vm.push(val)
			}

		case opcode.LOAD:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			// Ưu tiên: 1. Cục bộ -> 2. Closure -> 3. Module -> 4. Builtins
			if v, ok := f.Vars[name]; ok {
				vm.push(v)
			} else if f.Fn != nil && f.Fn.Scope != nil {
				if v, ok := f.Fn.Scope[name]; ok {
					vm.push(v)
				} else if v, ok := vm.Vars[name]; ok {
					vm.push(v)
				} else if v, ok := vm.Globals[name]; ok {
					vm.push(v)
				} else {
					vm.push(value.Value{K: value.Nil})
				}
			} else if v, ok := vm.Vars[name]; ok {
				vm.push(v)
			} else if v, ok := vm.Globals[name]; ok {
				vm.push(v)
			} else {
				vm.push(value.Value{K: value.Nil})
			}

		case opcode.STORE:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			val := vm.peek()
			// Nếu trong hàm, lưu vào cục bộ. Nếu ở main, lưu vào module vars.
			if vm.FrameIdx > 0 {
				f.Vars[name] = val
			} else {
				vm.Vars[name] = val
			}

		case opcode.GET:
			key := vm.pop().Text()
			target := vm.pop()
			vm.push(target.Get(key))

		case opcode.ADD:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Add(b))
		case opcode.SUB:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Sub(b))
		case opcode.MUL:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Mul(b))
		case opcode.DIV:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Div(b))

		case opcode.COMPARE:
			mode := vm.Bytecode[f.IP]
			f.IP++
			b, a := vm.pop(), vm.pop()
			vm.compare(a, b, mode)

		case opcode.JUMP:
			f.IP = int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
		case opcode.UNLESS:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if !vm.pop().Truthy() {
				f.IP = addr
			}

		case opcode.MAKE:
			t := vm.Bytecode[f.IP]
			f.IP++
			if t == 0 {
				vm.push(value.New(make(map[string]value.Value)))
			} else {
				vm.push(value.New(&[]value.Value{}))
			}

		case opcode.SET:
			val, key, target := vm.pop(), vm.pop(), vm.pop()
			if target.IsMap() {
				target.V.(map[string]value.Value)[key.Text()] = val
			} else if target.IsArray() {
				ptr := target.V.(*[]value.Value)
				*ptr = append(*ptr, val)
			}
			vm.push(target)

		case opcode.INVOKE:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			m := vm.pop().Text()
			args := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}
			target := vm.pop()

			// --- SPECIAL INTRINSICS (map, filter) ---
			if target.IsArray() && (m == "map" || m == "filter") && len(args) > 0 {
				if sFn, ok := args[0].V.(*value.ScriptFunction); ok {
					ptr := target.V.(*[]value.Value)
					arr := *ptr
					resArr := make([]value.Value, 0, len(arr))
					for _, item := range arr {
						val := vm.ExecuteLambda(sFn, []value.Value{item})
						if m == "map" {
							resArr = append(resArr, val)
						} else if val.Truthy() {
							resArr = append(resArr, item)
						}
					}
					vm.push(value.New(resArr))
					continue
				}
			}
			vm.push(target.Invoke(m, args...))

		case opcode.CALL:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			fn := vm.pop()
			args := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}

			if fn.K == value.Func {
				if s, ok := fn.V.(*value.ScriptFunction); ok {
					// PUSH FRAME MỚI
					vm.FrameIdx++
					newFrame := &vm.Frames[vm.FrameIdx]
					newFrame.IP = s.Address
					newFrame.Fn = s
					if newFrame.Vars == nil {
						newFrame.Vars = make(map[string]value.Value)
					} else {
						for k := range newFrame.Vars {
							delete(newFrame.Vars, k)
						}
					}
					// Gán tham số
					for i, name := range s.ParamNames {
						if i < len(args) {
							newFrame.Vars[name] = args[i]
						}
					}
				} else if m, ok := fn.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, args...))
				} else if g, ok := fn.V.(func(...value.Value) value.Value); ok {
					vm.push(g(args...))
				} else {
					vm.push(value.Value{K: value.Nil})
				}
			} else {
				vm.call(fn.Text(), args...)
			}

		case opcode.RETURN:
			res := vm.pop()
			if vm.FrameIdx == 0 {
				return res
			}
			vm.FrameIdx--
			vm.push(res)

		case opcode.HALT:
			return vm.pop()
		case opcode.POP:
			vm.pop()

		default:
			fmt.Printf("Unknown OP: %d at IP %d\n", op, f.IP-1)
			return value.Value{K: value.Invalid}
		}
	}
	if len(vm.Stack) > 0 {
		return vm.pop()
	}
	return value.Value{K: value.Nil}
}

func (vm *VM) push(v value.Value) { vm.Stack = append(vm.Stack, v) }

// ExecuteLambda runs a script function to completion in a dedicated sub-frame
func (vm *VM) ExecuteLambda(s *value.ScriptFunction, args []value.Value) value.Value {
	vm.FrameIdx++
	f := &vm.Frames[vm.FrameIdx]
	f.IP = s.Address
	f.Fn = s
	if f.Vars == nil {
		f.Vars = make(map[string]value.Value)
	} else {
		for k := range f.Vars {
			delete(f.Vars, k)
		}
	}
	// Bind params
	for i, name := range s.ParamNames {
		if i < len(args) {
			f.Vars[name] = args[i]
		}
	}

	startFrame := vm.FrameIdx
	// Run only until THIS frame is popped
	for vm.FrameIdx >= startFrame {
		// Logic same as Run loop but local
		f = &vm.Frames[vm.FrameIdx]
		op := opcode.Opcode(vm.Bytecode[f.IP])
		f.IP++

		switch op {
		case opcode.PUSH:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			val := vm.Constants[idx]
			if sFn, ok := val.V.(*value.ScriptFunction); ok {
				closure := &value.ScriptFunction{Address: sFn.Address, ParamNames: sFn.ParamNames, Scope: make(map[string]value.Value)}
				for k, v := range f.Vars {
					closure.Scope[k] = v
				}
				vm.push(value.New(closure))
			} else {
				vm.push(val)
			}
		case opcode.LOAD:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			if v, ok := f.Vars[name]; ok {
				vm.push(v)
			} else if f.Fn != nil && f.Fn.Scope != nil {
				if v, ok := f.Fn.Scope[name]; ok {
					vm.push(v)
				} else if v, ok := vm.Vars[name]; ok {
					vm.push(v)
				} else if v, ok := vm.Globals[name]; ok {
					vm.push(v)
				} else {
					vm.push(value.Value{K: value.Nil})
				}
			} else if v, ok := vm.Vars[name]; ok {
				vm.push(v)
			} else if v, ok := vm.Globals[name]; ok {
				vm.push(v)
			} else {
				vm.push(value.Value{K: value.Nil})
			}
		case opcode.STORE:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			f.Vars[vm.Constants[idx].V.(string)] = vm.peek()
		case opcode.ADD:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Add(b))
		case opcode.SUB:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Sub(b))
		case opcode.MUL:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Mul(b))
		case opcode.DIV:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Div(b))
		case opcode.COMPARE:
			mode := vm.Bytecode[f.IP]
			f.IP++
			b, a := vm.pop(), vm.pop()
			vm.compare(a, b, mode)
		case opcode.JUMP:
			f.IP = int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
		case opcode.UNLESS:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if !vm.pop().Truthy() {
				f.IP = addr
			}
		case opcode.RETURN:
			res := vm.pop()
			vm.FrameIdx--
			if vm.FrameIdx < startFrame {
				return res
			}
			vm.push(res)
		case opcode.CALL:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			fn := vm.pop()
			fnArgs := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				fnArgs[i] = vm.pop()
			}
			if fn.K == value.Func {
				if s, ok := fn.V.(*value.ScriptFunction); ok {
					vm.FrameIdx++
					nf := &vm.Frames[vm.FrameIdx]
					nf.IP = s.Address
					nf.Fn = s
					if nf.Vars == nil {
						nf.Vars = make(map[string]value.Value)
					} else {
						for k := range nf.Vars {
							delete(nf.Vars, k)
						}
					}
					for i, name := range s.ParamNames {
						if i < len(fnArgs) {
							nf.Vars[name] = fnArgs[i]
						}
					}
				} else if m, ok := fn.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, fnArgs...))
				} else if g, ok := fn.V.(func(...value.Value) value.Value); ok {
					vm.push(g(fnArgs...))
				} else {
					vm.push(value.Value{K: value.Nil})
				}
			} else {
				vm.call(fn.Text(), fnArgs...)
			}
		case opcode.INVOKE:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			m := vm.pop().Text()
			ivArgs := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				ivArgs[i] = vm.pop()
			}
			target := vm.pop()
			vm.push(target.Invoke(m, ivArgs...))
		case opcode.GET:
			key := vm.pop().Text()
			target := vm.pop()
			vm.push(target.Get(key))
		case opcode.MAKE:
			t := vm.Bytecode[f.IP]
			f.IP++
			if t == 0 {
				vm.push(value.New(make(map[string]value.Value)))
			} else {
				vm.push(value.New(&[]value.Value{}))
			}
		case opcode.SET:
			val, key, target := vm.pop(), vm.pop(), vm.pop()
			if target.IsMap() {
				target.V.(map[string]value.Value)[key.Text()] = val
			} else if target.IsArray() {
				ptr := target.V.(*[]value.Value)
				*ptr = append(*ptr, val)
			}
			vm.push(target)
		case opcode.POP:
			vm.pop()
		}

	}
	return vm.pop()
}

func (vm *VM) pop() value.Value {
	if len(vm.Stack) == 0 {
		return value.Value{K: value.Nil}
	}
	v := vm.Stack[len(vm.Stack)-1]
	vm.Stack = vm.Stack[:len(vm.Stack)-1]
	return v
}
func (vm *VM) peek() value.Value {
	if len(vm.Stack) == 0 {
		return value.Value{K: value.Nil}
	}
	return vm.Stack[len(vm.Stack)-1]
}
func (vm *VM) compare(a, b value.Value, mode uint8) {
	// MAGIC PROXY SUPPORT: Nếu một trong 2 là Proxy, ta hỏi Handler
	if a.K == value.Proxy || b.K == value.Proxy {
		var op string
		switch mode {
		case 0:
			op = "="
		case 1:
			op = "!="
		case 2:
			op = ">"
		case 3:
			op = "<"
		case 4:
			op = ">="
		case 5:
			op = "<="
		}

		if a.K == value.Proxy {
			if d, ok := a.V.(*value.ProxyData); ok && d.Handler != nil {
				vm.push(d.Handler.OnCompare(op, b))
				return
			}
		} else {
			if d, ok := b.V.(*value.ProxyData); ok && d.Handler != nil {
				vm.push(d.Handler.OnCompare(op, a))
				return
			}
		}
	}

	var res bool
	switch mode {

	case 0:
		res = a.Equal(b)
	case 1:
		res = a.NotEqual(b)
	case 2:
		res = a.Greater(b)
	case 3:
		res = a.Less(b)
	case 4:
		res = a.GreaterEqual(b)
	case 5:
		res = a.LessEqual(b)
	}
	vm.push(value.ToBool(res))
}
func (vm *VM) call(name string, args ...value.Value) {
	if name == "log" || name == "PRINT" {
		for _, arg := range args {
			fmt.Print(arg.Text(), " ")
		}
		fmt.Println()
		vm.push(value.Value{K: value.Nil})
	} else {
		vm.push(value.Value{K: value.Nil})
	}
}
