package vm

import (
	"fmt"

	"github.com/kitwork/engine/opcode"
	"github.com/kitwork/engine/value"
)

// VM là bộ não thực thi của Kitwork
type VM struct {
	Bytecode  []byte
	Constants []value.Value
	IP        int           // Instruction Pointer (con trỏ lệnh)
	Stack     []value.Value // Ngăn xếp dữ liệu
	Vars      map[string]value.Value
	CallStack []int // Lưu vết IP để RETURN quay lại
}

func NewVM(code []byte, constants []value.Value) *VM {
	return &VM{
		Bytecode:  code,
		Constants: constants,
		IP:        0,
		Stack:     make([]value.Value, 0, 1024),
		Vars:      make(map[string]value.Value),
		CallStack: make([]int, 0, 64),
	}
}

// Reset xóa sạch trạng thái để tái sử dụng VM từ sync.Pool
func (vm *VM) Reset(code []byte, constants []value.Value) {
	vm.Bytecode = code
	vm.Constants = constants
	vm.IP = 0
	vm.Stack = vm.Stack[:0]
	// Xóa Vars nhưng giữ lại map
	for k := range vm.Vars {
		delete(vm.Vars, k)
	}
	vm.CallStack = vm.CallStack[:0]
}

func (vm *VM) Run() value.Value {
	for vm.IP < len(vm.Bytecode) {
		op := opcode.Opcode(vm.Bytecode[vm.IP])
		vm.IP++

		switch op {
		case opcode.PUSH:
			idx := vm.readUint16()
			vm.push(vm.Constants[idx])

		case opcode.POP:
			vm.pop()

		case opcode.LOAD:
			idx := vm.readUint16()
			name := vm.Constants[idx].Text()
			val, ok := vm.Vars[name]
			if ok {
				vm.push(val)
			} else {
				vm.push(value.Value{K: value.Nil})
			}

		case opcode.STORE:
			idx := vm.readUint16()
			name := vm.Constants[idx].Text()
			val := vm.pop()
			vm.Vars[name] = val
			vm.push(val) // Gán là một biểu thức, trả về giá trị đã gán

		case opcode.GET:
			key := vm.pop().Text()
			target := vm.pop()
			vm.push(target.Get(key))

		case opcode.ADD:
			b := vm.pop()
			a := vm.pop()
			vm.push(a.Add(b))

		case opcode.SUB:
			b := vm.pop()
			a := vm.pop()
			vm.push(a.Sub(b))

		case opcode.MUL:
			b := vm.pop()
			a := vm.pop()
			vm.push(a.Mul(b))

		case opcode.DIV:
			b := vm.pop()
			a := vm.pop()
			vm.push(a.Div(b))

		case opcode.COMPARE:
			mode := vm.readUint8()
			right := vm.pop()
			left := vm.pop()
			vm.compare(left, right, mode)

		case opcode.JUMP:
			vm.IP = int(vm.readUint16())

		case opcode.UNLESS:
			addr := int(vm.readUint16())
			cond := vm.pop()
			if !cond.Truthy() {
				vm.IP = addr
			}

		case opcode.MAKE:
			typeID := vm.readUint8()
			if typeID == 0 {
				vm.push(value.New(make(map[string]value.Value)))
			} else {
				vm.push(value.New(make([]value.Value, 0)))
			}

		case opcode.SET:
			val := vm.pop()
			key := vm.pop()
			target := vm.pop()

			if target.IsMap() {
				m := target.V.(map[string]value.Value)
				m[key.Text()] = val
				vm.push(target)
			} else if target.IsArray() {
				arr := target.V.([]value.Value)
				target.V = append(arr, val)
				vm.push(target)
			}

		case opcode.INVOKE:
			argCount := int(vm.readUint8())
			methodName := vm.pop().Text()

			args := make([]value.Value, argCount)
			for i := argCount - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}

			target := vm.pop()
			result := target.Invoke(methodName, args...)
			vm.push(result)

		case opcode.CALL:
			argCount := int(vm.readUint8())
			fnVal := vm.pop()

			args := make([]value.Value, argCount)
			for i := argCount - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}

			if fnVal.K == value.Func {
				if goFn, ok := fnVal.V.(func(...value.Value) value.Value); ok {
					vm.push(goFn(args...))
				} else if goMethod, ok := fnVal.V.(func(value.Value, ...value.Value) value.Value); ok {
					vm.push(goMethod(value.Value{K: value.Nil}, args...))
				} else if m, ok := fnVal.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, args...))
				}
			} else {
				vm.call(fnVal.Text(), args...)
			}

		case opcode.LAMBDA:
			addr := int(vm.pop().N)
			vm.CallStack = append(vm.CallStack, vm.IP)
			vm.IP = addr

		case opcode.RETURN:
			if len(vm.CallStack) > 0 {
				last := len(vm.CallStack) - 1
				vm.IP = vm.CallStack[last]
				vm.CallStack = vm.CallStack[:last]
			} else {
				if len(vm.Stack) > 0 {
					return vm.pop()
				}
				return value.Value{K: value.Nil}
			}

		case opcode.HALT:
			if len(vm.Stack) > 0 {
				return vm.pop()
			}
			return value.Value{K: value.Nil}

		default:
			fmt.Printf("Unknown opcode: %v\n", op)
			return value.Value{K: value.Invalid}
		}
	}
	if len(vm.Stack) > 0 {
		return vm.pop()
	}
	return value.Value{K: value.Nil}
}

// --- Helpers ---

func (vm *VM) readUint8() uint8 {
	res := vm.Bytecode[vm.IP]
	vm.IP++
	return res
}

func (vm *VM) readUint16() uint16 {
	res := uint16(vm.Bytecode[vm.IP])<<8 | uint16(vm.Bytecode[vm.IP+1])
	vm.IP += 2
	return res
}

func (vm *VM) push(v value.Value) {
	vm.Stack = append(vm.Stack, v)
}

func (vm *VM) pop() value.Value {
	if len(vm.Stack) == 0 {
		return value.Value{K: value.Nil}
	}
	v := vm.Stack[len(vm.Stack)-1]
	vm.Stack = vm.Stack[:len(vm.Stack)-1]
	return v
}

func (vm *VM) compare(a, b value.Value, mode uint8) {
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
	switch name {
	case "PRINT":
		for _, arg := range args {
			fmt.Print(arg.Text(), " ")
		}
		fmt.Println()
		vm.push(value.Value{K: value.Nil})
	default:
		fmt.Printf("Unknown service: %s\n", name)
		vm.push(value.Value{K: value.Nil})
	}
}
