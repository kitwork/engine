package runtime

import (
	"fmt"
	"reflect"

	"github.com/kitwork/engine/energy"
	"github.com/kitwork/engine/opcode"
	"github.com/kitwork/engine/value"
)

// Frame đại diện cho một khung thực thi (Activation Record)
type Frame struct {
	IP     int
	Vars   map[string]value.Value  // Local scope
	Fn     *value.ScriptFunction   // Hàm đang được thực thi
	Defers []*value.ScriptFunction // Deferred functions
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
	Spawner   func(s *value.ScriptFunction)
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

func (vm *Runtime) Defer(fn *value.ScriptFunction) {
	if vm.FrameIdx >= 0 {
		f := &vm.Frames[vm.FrameIdx]
		f.Defers = append(f.Defers, fn)
	}
}

func (vm *Runtime) Run() value.Value {
	//fmt.Printf("[VM Run] Starting execution, bytecode length: %d\n", len(vm.Bytecode))
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

		// Safety check for operations that read operands
		switch op {
		case opcode.PUSH, opcode.LOAD, opcode.STORE, opcode.JUMP, opcode.TRUE, opcode.FALSE, opcode.ITER:
			if f.IP+1 >= len(vm.Bytecode) {
				return value.Value{K: value.Invalid, V: "Bytecode truncated: expected operands"}
			}
		}

		// Tiêu thụ năng lượng
		vm.Energy += uint64(energy.Table[op])

		switch op {
		case opcode.PUSH:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			val := vm.Constants[idx]
			if sFn, ok := val.V.(*value.ScriptFunction); ok {
				// fmt.Printf("[VM PUSH] ScriptFunction from constants[%d] with Address: %d\n", idx, sFn.Address)
				closure := &value.ScriptFunction{
					Address:    sFn.Address,
					ParamNames: sFn.ParamNames,
					Scope:      f.Vars, // Use reference to support recursion and mutability
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
			name := vm.Constants[idx].V.(string)
			val := vm.peek()

			// Closure support: check if it exists in closure scope first
			if f.Fn != nil && f.Fn.Scope != nil {
				if _, ok := f.Fn.Scope[name]; ok {
					f.Fn.Scope[name] = val
					continue
				}
			}

			f.Vars[name] = val

		case opcode.GET:
			keyVal := vm.pop()
			target := vm.pop()
			if keyVal.K == value.Number {
				vm.push(target.At(int(keyVal.N)))
			} else {
				vm.push(target.Get(keyVal.Text()))
			}

		case opcode.DUP:
			vm.push(vm.peek())

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
		case opcode.TRUE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if vm.pop().Truthy() {
				f.IP = addr
			}
		case opcode.FALSE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if !vm.pop().Truthy() {
				f.IP = addr
			}
		case opcode.AND:
			b, a := vm.pop(), vm.pop()
			if !a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case opcode.OR:
			b, a := vm.pop(), vm.pop()
			if a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case opcode.NOT:
			a := vm.pop()
			vm.push(value.ToBool(!a.Truthy()))

		case opcode.ITER:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			idxVal := vm.pop()
			colVal := vm.peek()

			if int(idxVal.N) < colVal.Len() {
				item := colVal.At(int(idxVal.N))
				vm.push(value.New(idxVal.N + 1))
				vm.push(item)
			} else {
				vm.pop()
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

			if m == "len" || m == "length" {
				vm.push(value.New(float64(target.Len())))
				continue
			}

			if target.IsArray() && (m == "map" || m == "filter" || m == "each") && len(args) > 0 {
				if sFn, ok := args[0].V.(*value.ScriptFunction); ok {
					ptr := target.V.(*[]value.Value)
					arr := *ptr
					resArr := make([]value.Value, 0, len(arr))
					for _, item := range arr {
						val := vm.ExecuteLambda(sFn, []value.Value{item})
						if m == "map" {
							resArr = append(resArr, val)
						} else if m == "filter" && val.Truthy() {
							resArr = append(resArr, item)
						}
					}
					if m == "each" {
						vm.push(target)
					} else {
						vm.push(value.New(resArr))
					}
					continue
				}
			}

			vm.push(target.Invoke(m, args...))

		case opcode.CALL:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			args := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}
			fn := vm.pop()

			if fn.K == value.Func {
				if s, ok := fn.V.(*value.ScriptFunction); ok {
					vm.FrameIdx++
					newFrame := &vm.Frames[vm.FrameIdx]
					newFrame.IP = s.Address
					newFrame.Fn = s
					newFrame.Vars = make(map[string]value.Value) // Fresh map for each call
					for i, name := range s.ParamNames {
						if i < len(args) {
							newFrame.Vars[name] = args[i]
						}
					}
				} else if m, ok := fn.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, args...))
				} else if g, ok := fn.V.(func(...value.Value) value.Value); ok {
					vm.push(g(args...))
				} else if _, ok := fn.V.(reflect.Value); ok {
					vm.push(fn.Call(fn.Text(), args...))
				} else {
					vm.push(value.Value{K: value.Nil})
				}
			} else if fn.K == value.Proxy {
				if handler, ok := fn.V.(value.ProxyHandler); ok {
					// Use empty method name to signify a direct call to the proxy
					vm.push(handler.OnInvoke("", args...))
				}
			} else {
				vm.call(fn.Text(), args...)
			}

		case opcode.RETURN:
			res := vm.pop()
			for i := len(f.Defers) - 1; i >= 0; i-- {
				vm.ExecuteLambda(f.Defers[i], nil)
			}
			f.Defers = f.Defers[:0]

			if vm.FrameIdx == 0 {
				return res
			}
			vm.FrameIdx--
			vm.push(res)

		case opcode.HALT:
			return vm.pop()
		case opcode.DEFER:
			fn := vm.pop()
			if s, ok := fn.V.(*value.ScriptFunction); ok {
				f.Defers = append(f.Defers, s)
			}
		case opcode.SPAWN:
			fn := vm.pop()
			if s, ok := fn.V.(*value.ScriptFunction); ok && vm.Spawner != nil {
				vm.Spawner(s)
			}
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

func (vm *Runtime) push(v value.Value) { vm.Stack = append(vm.Stack, v) }

func (vm *Runtime) ExecuteLambda(s *value.ScriptFunction, args []value.Value) value.Value {
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
	for i, name := range s.ParamNames {
		if i < len(args) {
			f.Vars[name] = args[i]
		}
	}

	startFrame := vm.FrameIdx
	for vm.FrameIdx >= startFrame {
		f = &vm.Frames[vm.FrameIdx]

		if f.IP >= len(vm.Bytecode) {
			if vm.FrameIdx == startFrame {
				return vm.pop()
			}
			vm.FrameIdx--
			continue
		}

		op := opcode.Opcode(vm.Bytecode[f.IP])
		f.IP++

		// Safety check for operations that read operands
		switch op {
		case opcode.PUSH, opcode.LOAD, opcode.STORE, opcode.JUMP, opcode.TRUE, opcode.FALSE, opcode.ITER:
			if f.IP+1 >= len(vm.Bytecode) {
				return value.Value{K: value.Invalid, V: "Lambda Bytecode truncated"}
			}
		}

		vm.Energy += uint64(energy.Table[op])

		switch op {
		case opcode.PUSH:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			val := vm.Constants[idx]
			if sFn, ok := val.V.(*value.ScriptFunction); ok {
				closure := &value.ScriptFunction{
					Address:    sFn.Address,
					ParamNames: sFn.ParamNames,
					Scope:      f.Vars,
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
			name := vm.Constants[idx].V.(string)
			val := vm.peek()

			if f.Fn != nil && f.Fn.Scope != nil {
				if _, ok := f.Fn.Scope[name]; ok {
					f.Fn.Scope[name] = val
					continue
				}
			}

			f.Vars[name] = val
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
		case opcode.TRUE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if vm.pop().Truthy() {
				f.IP = addr
			}
		case opcode.FALSE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if !vm.pop().Truthy() {
				f.IP = addr
			}
		case opcode.AND:
			b, a := vm.pop(), vm.pop()
			if !a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case opcode.OR:
			b, a := vm.pop(), vm.pop()
			if a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case opcode.NOT:
			a := vm.pop()
			vm.push(value.ToBool(!a.Truthy()))
		case opcode.ITER:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			idxVal := vm.pop()
			colVal := vm.peek()
			if int(idxVal.N) < colVal.Len() {
				item := colVal.At(int(idxVal.N))
				vm.push(value.New(idxVal.N + 1))
				vm.push(item)
			} else {
				vm.pop()
				f.IP = addr
			}
		case opcode.DEFER:
			fn := vm.pop()
			if s, ok := fn.V.(*value.ScriptFunction); ok {
				f.Defers = append(f.Defers, s)
			}
		case opcode.SPAWN:
			fn := vm.pop()
			if s, ok := fn.V.(*value.ScriptFunction); ok && vm.Spawner != nil {
				vm.Spawner(s)
			}
		case opcode.RETURN:
			res := vm.pop()
			for i := len(f.Defers) - 1; i >= 0; i-- {
				vm.ExecuteLambda(f.Defers[i], nil)
			}
			f.Defers = f.Defers[:0]

			vm.FrameIdx--
			if vm.FrameIdx < startFrame {
				return res
			}
			vm.push(res)
		case opcode.CALL:
			// fmt.Printf("VM: OpCall Triggered at IP %d\n", f.IP-1)
			n := int(vm.Bytecode[f.IP])
			f.IP++
			fnArgs := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				fnArgs[i] = vm.pop()
			}
			fn := vm.pop()
			if fn.K == value.Func {
				if s, ok := fn.V.(*value.ScriptFunction); ok {
					vm.FrameIdx++
					nf := &vm.Frames[vm.FrameIdx]
					nf.IP = s.Address
					nf.Fn = s
					nf.Vars = make(map[string]value.Value) // Fresh map
					for i, name := range s.ParamNames {
						if i < len(fnArgs) {
							nf.Vars[name] = fnArgs[i]
						}
					}
				} else if m, ok := fn.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, fnArgs...))
				} else if g, ok := fn.V.(func(...value.Value) value.Value); ok {
					fmt.Printf("[VM CALL] Executing Go func (%T) with %d args\n", g, len(fnArgs))
					vm.push(g(fnArgs...))
				} else if _, ok := fn.V.(reflect.Value); ok {
					fmt.Printf("[VM CALL] Executing reflect.Value call with %d args\n", len(fnArgs))
					vm.push(fn.Call(fn.Text(), fnArgs...))
				} else {
					fmt.Printf("[VM CALL] Unknown func type: %T (Kind: %s)\n", fn.V, fn.K.String())
					vm.push(value.Value{K: value.Nil})
				}
			} else if fn.K == value.Proxy {
				if handler, ok := fn.V.(value.ProxyHandler); ok {
					vm.push(handler.OnInvoke("", fnArgs...))
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
			keyVal := vm.pop()
			target := vm.pop()
			if keyVal.K == value.Number {
				vm.push(target.At(int(keyVal.N)))
			} else {
				vm.push(target.Get(keyVal.Text()))
			}
		case opcode.DUP:
			vm.push(vm.peek())
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
func (vm *Runtime) compare(a, b value.Value, mode uint8) {
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
			if handler, ok := a.V.(value.ProxyHandler); ok {
				vm.push(handler.OnCompare(op, b))
				return
			}
		} else {
			if handler, ok := b.V.(value.ProxyHandler); ok {
				vm.push(handler.OnCompare(op, a))
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
func (vm *Runtime) call(name string, args ...value.Value) {
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
