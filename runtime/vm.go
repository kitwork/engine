package runtime

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kitwork/engine/value"
)

func (vm *VM) Defer(fn *value.Lambda) {
	if vm.FrameIdx >= 0 {
		f := &vm.Frames[vm.FrameIdx]
		f.Defers = append(f.Defers, fn)
	}
}

// finalizeStatement is the POPFIN behaviour: a bare expression statement whose value is a
// value.StatementFinalizer (a lazy http request) fires here — at the END of the statement — and runs
// its .then()/.catch() handler on THIS vm (re-entrant, exactly like an array-map callback). This is
// what lets the http chain be written flat in any order: nothing fires until the statement ends. For
// every other value POPFIN is just POP.
func (vm *VM) finalizeStatement(v value.Value, soft bool) {
	if v.K != value.Struct {
		return
	}
	if f, ok := v.V.(value.StatementFinalizer); ok {
		if h, arg, run := f.FinalizeStatement(soft); run && h != nil {
			vm.ExecuteLambda(h, []value.Value{arg})
		}
	}
}

// lookupScopeChain tìm biến dọc theo chuỗi closure bao ngoài (lexical scoping).
// Cho phép lambda lồng nhiều cấp đọc biến của mọi hàm bao ngoài, đúng ngữ nghĩa JS.
func lookupScopeChain(fn *value.Lambda, name string) (value.Value, bool) {
	for ; fn != nil; fn = fn.Parent {
		if fn.Scope != nil {
			if v, ok := fn.Scope[name]; ok {
				return v, true
			}
		}
	}
	return value.Value{}, false
}

// storeScopeChain ghi đè biến ĐÃ TỒN TẠI ở scope bao ngoài gần nhất (nếu có).
// Trả về true nếu đã ghi — false nghĩa là biến mới, lưu cục bộ tại frame hiện hành.
func storeScopeChain(fn *value.Lambda, name string, val value.Value) bool {
	for ; fn != nil; fn = fn.Parent {
		if fn.Scope != nil {
			if _, ok := fn.Scope[name]; ok {
				fn.Scope[name] = val
				return true
			}
		}
	}
	return false
}

func (vm *VM) Run() value.Value {
	//fmt.Printf("[VM Run] Starting execution, bytecode length: %d\n", len(vm.Bytecode))
	for vm.FrameIdx >= 0 {
		f := &vm.Frames[vm.FrameIdx]
		// if f.IP < len(vm.Bytecode) {
		// 	op := vm.Bytecode[f.IP]
		// 	fmt.Printf("[VM TRACE] IP: %-3d | Op: %-3d | FrameIdx: %d | Stack: %+v\n", f.IP, op, vm.FrameIdx, vm.Stack)
		// }

		if f.IP >= len(vm.Bytecode) {
			if vm.FrameIdx == 0 {
				break
			}
			vm.FrameIdx--
			continue
		}

		op := Opcode(vm.Bytecode[f.IP])
		f.IP++

		// Safety check for operations that read operands
		switch op {
		case PUSH, LOAD, STORE, JUMP, TRUE, FALSE, ITER:
			if f.IP+1 >= len(vm.Bytecode) {
				return value.Value{K: value.Invalid, V: "Bytecode truncated: expected operands"}
			}
		}

		// Tiêu thụ năng lượng
		vm.Energy += uint64(Table[op])
		if vm.MaxEnergy > 0 && vm.Energy > vm.MaxEnergy {
			line := vm.currentLine(f.IP - 1)
			return value.Value{
				K: value.Invalid,
				V: fmt.Sprintf("Energy Limit Exceeded: Execution halted (at line %d)", line),
			}
		}

		switch op {
		case PUSH:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			val := vm.Constants[idx]
			if sFn, ok := val.V.(*value.Lambda); ok {
				// fmt.Printf("[VM PUSH] ScriptFunction from constants[%d] with Address: %d\n", idx, sFn.Address)
				closure := &value.Lambda{
					Address: sFn.Address,
					Params:  sFn.Params,
					Scope:   f.Vars, // Use reference to support recursion and mutability
					Parent:  f.Fn,   // Scope chain: thấy được biến của các hàm bao ngoài
				}
				f.captured = true // map này đã escape vào closure → đừng tái dùng/xoá
				vm.push(value.New(closure))
			} else {
				vm.push(val)
			}

		case LOAD:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			// Thứ tự tra cứu: biến cục bộ → chuỗi scope closure → biến
			// top-level (vm.Vars) → Globals hệ thống.
			if v, ok := f.Vars[name]; ok {
				vm.push(v)
			} else if v, ok := lookupScopeChain(f.Fn, name); ok {
				vm.push(v)
			} else if v, ok := vm.Vars[name]; ok {
				vm.push(v)
			} else if v, ok := vm.Globals[name]; ok {
				vm.push(v)
			} else {
				vm.push(value.Value{K: value.Nil})
			}

		case BUILTIN:
			idx := vm.Bytecode[f.IP]
			f.IP++
			if int(idx) < len(vm.Builtins) {
				vm.push(vm.Builtins[idx])
			} else {
				vm.push(value.Value{K: value.Nil})
			}

		case STORE:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			val := vm.peek()

			// Logic lưu biến thông minh:
			// 1. Biến đã tồn tại ở scope bao ngoài (chuỗi closure) -> ghi vào đó
			if storeScopeChain(f.Fn, name, val) {
				continue
			}

			// 2. Nếu ở Frame gốc (main script của request) -> lưu vào vm.Vars
			if vm.FrameIdx == 0 {
				vm.Vars[name] = val
			} else {
				// 3. Nếu ở trong hàm -> lưu vào biến cục bộ của hàm đó
				f.Vars[name] = val
			}

		case GET:
			keyVal := vm.pop()
			target := vm.pop()
			if keyVal.K == value.Number {
				vm.push(target.At(int(keyVal.N)))
			} else {
				vm.push(target.Get(keyVal.Text()))
			}

		case DUP:
			vm.push(vm.peek())

		case ADD:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Add(b))
		case SUB:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Sub(b))
		case MUL:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Mul(b))
		case DIV:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Div(b))
		case MOD:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Mod(b))

		case COMPARE:
			mode := vm.Bytecode[f.IP]
			f.IP++
			b, a := vm.pop(), vm.pop()
			vm.compare(a, b, mode)

		case JUMP:
			f.IP = int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
		case TRUE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if vm.pop().Truthy() {
				f.IP = addr
			}
		case FALSE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if !vm.pop().Truthy() {
				f.IP = addr
			}
		case AND:
			b, a := vm.pop(), vm.pop()
			if !a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case OR:
			b, a := vm.pop(), vm.pop()
			if a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case NOT:
			a := vm.pop()
			vm.push(value.ToBool(!a.Truthy()))

		case ITER:
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

		case MAKE:
			t := vm.Bytecode[f.IP]
			f.IP++
			if t == 0 {
				vm.push(value.New(make(map[string]value.Value)))
			} else {
				vm.push(value.New(&[]value.Value{}))
			}

		case SET:
			val, key, target := vm.pop(), vm.pop(), vm.pop()
			if target.IsMap() {
				target.V.(map[string]value.Value)[key.Text()] = val
			} else if target.IsArray() {
				ptr := target.V.(*[]value.Value)
				*ptr = append(*ptr, val)
			}
			vm.push(target)

		case INVOKE:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			m := vm.pop().Text()
			ivArgs := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				ivArgs[i] = vm.pop()
			}
			target := vm.pop()

			handled := false
			// Special Handling for cache.get(key, callback, ttl)
			if cacheObj, ok := target.V.(value.TenantCache); ok && m == "get" && len(ivArgs) > 0 {
				key := ivArgs[0].Text()
				if val, found := cacheObj.GetCache(key); found {
					vm.push(val)
					handled = true
				} else if len(ivArgs) > 1 && ivArgs[1].K == value.Func {
					callback := ivArgs[1].V.(*value.Lambda)
					val := vm.ExecuteLambda(callback, nil)
					var ttl value.Value
					if len(ivArgs) > 2 {
						ttl = ivArgs[2]
					}
					cacheObj.SetCache(key, val, ttl)
					vm.push(val)
					handled = true
				}
			}

			// Special Handling for Functional Methods (Map, Filter, Find)
			if !handled && target.K == value.Array && len(ivArgs) > 0 && ivArgs[0].K == value.Func {
				callback := ivArgs[0].V.(*value.Lambda)
				arr := *target.V.(*[]value.Value)

				switch m {
				case "map":
					resArr := make([]value.Value, len(arr))
					for i, item := range arr {
						resArr[i] = vm.ExecuteLambda(callback, []value.Value{item, value.New(float64(i))})
					}
					vm.push(value.New(resArr))
					handled = true
				case "filter":
					resArr := []value.Value{}
					for i, item := range arr {
						if vm.ExecuteLambda(callback, []value.Value{item, value.New(float64(i))}).Truthy() {
							resArr = append(resArr, item)
						}
					}
					vm.push(value.New(resArr))
					handled = true
				case "find":
					for i, item := range arr {
						if vm.ExecuteLambda(callback, []value.Value{item, value.New(float64(i))}).Truthy() {
							vm.push(item)
							handled = true
							break
						}
					}
					if !handled {
						vm.push(value.Value{K: value.Nil})
						handled = true
					}
				}
			}

			if !handled && target.K == value.Array {
				if res, ok := vm.arrayCallbackMethod(target, m, ivArgs); ok {
					vm.push(res)
					handled = true
				}
			}

			if !handled && target.K == value.Map {
				// obj.method() where the member is a SCRIPT lambda (obj = { f: () => … })
				// must be executed by the VM — value.Invoke/Call cannot run a *value.Lambda.
				if member := target.Get(m); member.K == value.Func {
					if lambda, ok := member.V.(*value.Lambda); ok {
						vm.push(vm.ExecuteLambda(lambda, ivArgs))
						handled = true
					}
				}
			}

			if !handled {
				vm.push(target.Invoke(m, ivArgs...))
			}

		case CALL:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			args := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				args[i] = vm.pop()
			}
			fn := vm.pop()
			if fn.K == value.Func {
				if s, ok := fn.V.(*value.Lambda); ok {
					if vm.FrameIdx+1 >= len(vm.Frames) {
						return value.Value{
							K: value.Invalid,
							V: fmt.Sprintf("Stack overflow: Call stack limit exceeded (at line %d)", vm.currentLine(f.IP-1)),
						}
					}
					vm.FrameIdx++
					nf := &vm.Frames[vm.FrameIdx]
					nf.IP = s.Address
					nf.Fn = s

					// OPTIMIZATION: Recycle Map (Zero-Alloc Strategy). NHƯNG nếu map lần
					// dùng trước đã bị closure giữ (captured), phải cấp map MỚI — không
					// được xoá map mà closure còn tham chiếu.
					if nf.Vars == nil || nf.captured {
						nf.Vars = make(map[string]value.Value)
					} else {
						// Optimized map clear (compiler optimization ensures no re-alloc)
						for k := range nf.Vars {
							delete(nf.Vars, k)
						}
					}
					nf.captured = false

					for i, name := range s.Params {
						if i < len(args) {
							nf.Vars[name] = args[i]
						}
					}
				} else if m, ok := fn.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, args...))
				} else if g, ok := fn.V.(func(...value.Value) value.Value); ok {
					// fmt.Printf("[VM CALL] Executing Go func (%T) with %d args\n", g, len(args))
					vm.push(g(args...))
				} else if fo, ok := fn.V.(*value.FuncObject); ok {
					// Constructor-style call: Date(), new Date(...)
					vm.push(fo.Fn(args...))
				} else if _, ok := fn.V.(reflect.Value); ok {
					// fmt.Printf("[VM CALL] Executing reflect.Value call with %d args\n", len(args))
					vm.push(fn.Call(fn.Text(), args...))
				} else {
					// fmt.Printf("[VM CALL] Unknown func type: %T (Kind: %s)\n", fn.V, fn.K.String())
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

		case RETURN:
			res := vm.pop()
			// A returned http request WITH a .then()/.catch() handler (e.g. an arrow-body
			// `ctx => http.get(url).then(A)`) fires + runs its handler here instead of leaking unfired.
			vm.finalizeStatement(res, true)
			for i := len(f.Defers) - 1; i >= 0; i-- {
				vm.ExecuteLambda(f.Defers[i], nil)
			}
			f.Defers = f.Defers[:0]

			if vm.FrameIdx == 0 {
				return res
			}
			vm.FrameIdx--
			vm.push(res)

		case HALT:
			return vm.pop()
		case DEFER:
			fn := vm.pop()
			if s, ok := fn.V.(*value.Lambda); ok {
				f.Defers = append(f.Defers, s)
			}
		case SPAWN:
			fn := vm.pop()
			if s, ok := fn.V.(*value.Lambda); ok && vm.Spawner != nil {
				vm.Spawner(s)
			}
		case MERGE:
			src, target := vm.pop(), vm.peek()
			if target.IsMap() && src.IsMap() {
				targetMap := target.V.(map[string]value.Value)
				srcMap := src.Map()
				for k, v := range srcMap {
					targetMap[k] = v
				}
			}
		case POP:
			vm.pop()

		case POPFIN:
			vm.finalizeStatement(vm.pop(), false)

		case POPFINSOFT:
			vm.finalizeStatement(vm.pop(), true)

		default:
			fmt.Printf("Unknown OP: %d at IP %d\n", op, f.IP-1)
			return value.Value{K: value.Invalid}
		}

		// Kiểm tra lỗi phát sinh sau khi thực thi instruction
		if len(vm.Stack) > 0 && vm.peek().K == value.Invalid {
			errVal := vm.pop()
			line := vm.currentLine(f.IP - 1)
			errMsg := errVal.String()
			if !strings.Contains(errMsg, "(at line") {
				errMsg = fmt.Sprintf("%s (at line %d)", errMsg, line)
			}
			return value.Value{K: value.Invalid, V: errMsg}
		}
	}
	if len(vm.Stack) > 0 {
		return vm.pop()
	}
	return value.Value{K: value.Nil}
}

// arrayCallbackMethod xử lý các method Array nhận callback — forEach, some,
// every, findIndex, reduce, sort(comparator) — chỉ VM mới thực thi được Lambda.
// Trả về (kết quả, true) nếu method được xử lý tại đây; ngược lại (zero, false)
// để rơi xuống prototype table (vd: sort() không comparator).
func (vm *VM) arrayCallbackMethod(target value.Value, m string, ivArgs []value.Value) (value.Value, bool) {
	var arr []value.Value
	if ptr, ok := target.V.(*[]value.Value); ok {
		arr = *ptr
	} else if a, ok := target.V.([]value.Value); ok {
		arr = a
	} else {
		return value.Value{}, false
	}

	var cb *value.Lambda
	if len(ivArgs) > 0 && ivArgs[0].K == value.Func {
		cb, _ = ivArgs[0].V.(*value.Lambda)
	}
	if cb == nil {
		return value.Value{}, false
	}

	switch m {
	case "forEach":
		for i, item := range arr {
			vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
		}
		// JS forEach trả về undefined
		return value.Value{K: value.Nil}, true

	case "some":
		for i, item := range arr {
			if vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))}).Truthy() {
				return value.TRUE, true
			}
		}
		return value.FALSE, true

	case "every":
		for i, item := range arr {
			if !vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))}).Truthy() {
				return value.FALSE, true
			}
		}
		return value.TRUE, true

	case "findIndex":
		for i, item := range arr {
			if vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))}).Truthy() {
				return value.New(float64(i)), true
			}
		}
		return value.Value{K: value.Number, N: -1}, true

	case "reduce":
		start := 0
		var acc value.Value
		if len(ivArgs) > 1 {
			acc = ivArgs[1]
		} else {
			if len(arr) == 0 {
				return value.Value{K: value.Invalid, V: "reduce: empty array with no initial value"}, true
			}
			acc = arr[0]
			start = 1
		}
		for i := start; i < len(arr); i++ {
			acc = vm.ExecuteLambda(cb, []value.Value{acc, arr[i], value.New(float64(i))})
		}
		return acc, true

	case "sort":
		// sort(comparator) — sắp xếp tại chỗ, trả về chính mảng (chuẩn JS)
		sortByComparator(arr, func(a, b value.Value) bool {
			return vm.ExecuteLambda(cb, []value.Value{a, b}).N < 0
		})
		return target, true

	case "group", "groupBy":
		groups := make(map[string]value.Value)
		for i, item := range arr {
			keyVal := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			keyStr := keyVal.Text()

			var groupArr []value.Value
			if existing, ok := groups[keyStr]; ok {
				groupArr = *existing.V.(*[]value.Value)
			}
			groupArr = append(groupArr, item)
			groups[keyStr] = value.Value{K: value.Array, V: &groupArr}
		}
		return value.New(groups), true

	case "sortBy":
		type pair struct {
			item value.Value
			key  value.Value
		}
		pairs := make([]pair, len(arr))
		for i, item := range arr {
			key := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			pairs[i] = pair{item: item, key: key}
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			ki := pairs[i].key
			kj := pairs[j].key
			if ki.IsNumeric() && kj.IsNumeric() {
				return ki.N < kj.N
			}
			return ki.Text() < kj.Text()
		})
		for i, p := range pairs {
			arr[i] = p.item
		}
		return target, true

	case "unique":
		seen := make(map[any]bool)
		resArr := []value.Value{}
		for i, item := range arr {
			keyVal := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			key := keyVal.Interface()
			if !seen[key] {
				seen[key] = true
				resArr = append(resArr, item)
			}
		}
		if ptr, ok := target.V.(*[]value.Value); ok {
			*ptr = resArr
		}
		return target, true
	}

	return value.Value{}, false
}

// sortByComparator — insertion sort ổn định, tránh import sort để giữ vm.go gọn.
// Mảng tenant thường nhỏ; comparator do user cung cấp chạy qua VM.
func sortByComparator(a []value.Value, less func(x, y value.Value) bool) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && less(a[j], a[j-1]); j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (vm *VM) ExecuteLambda(s *value.Lambda, args []value.Value) value.Value {
	if s == nil {
		return value.Value{K: value.Nil}
	}
	if vm.FrameIdx+1 >= len(vm.Frames) {
		return value.Value{
			K: value.Invalid,
			V: "Stack overflow: Call stack limit exceeded",
		}
	}
	vm.FrameIdx++
	f := &vm.Frames[vm.FrameIdx]
	f.IP = s.Address
	f.Fn = s
	f.StackBase = len(vm.Stack)

	// OPTIMIZATION: Recycle Map — fresh map nếu lần trước bị closure capture.
	if f.Vars == nil || f.captured {
		f.Vars = make(map[string]value.Value)
	} else {
		for k := range f.Vars {
			delete(f.Vars, k)
		}
	}
	f.captured = false
	for i, name := range s.Params {
		if i < len(args) {
			f.Vars[name] = args[i]
		}
	}

	// Isolate this lambda's value-stack usage from the caller's. ExecuteLambda runs on
	// the SHARED vm.Stack; without this, a block-body lambda that leaves the stack
	// balanced (e.g. a forEach callback with no explicit return) would make a
	// fall-through `pop` steal the caller's PENDING value (e.g. the `300` in
	// `f() * 100 + g()`). Capture the base depth and never return below it.
	base := len(vm.Stack)
	defer func() {
		if len(vm.Stack) > base {
			vm.Stack = vm.Stack[:base] // drop any leftovers the lambda didn't consume
		}
	}()

	startFrame := vm.FrameIdx
	for vm.FrameIdx >= startFrame {
		f = &vm.Frames[vm.FrameIdx]

		if f.IP >= len(vm.Bytecode) {
			if vm.FrameIdx == startFrame {
				if len(vm.Stack) > base {
					return vm.Stack[len(vm.Stack)-1]
				}
				return value.Value{K: value.Nil}
			}
			vm.FrameIdx--
			continue
		}

		op := Opcode(vm.Bytecode[f.IP])
		// fmt.Printf("[VM] IP: %d, OP: %d\n", f.IP, op)
		f.IP++

		// Safety check for operations that read operands
		switch op {
		case PUSH, LOAD, STORE, JUMP, TRUE, FALSE, ITER:
			if f.IP+1 >= len(vm.Bytecode) {
				return value.Value{K: value.Invalid, V: "Lambda Bytecode truncated"}
			}
		}

		vm.Energy += uint64(Table[op])
		if vm.MaxEnergy > 0 && vm.Energy > vm.MaxEnergy {
			line := vm.currentLine(f.IP - 1)
			return value.Value{
				K: value.Invalid,
				V: fmt.Sprintf("Energy Limit Exceeded: Execution halted (at line %d)", line),
			}
		}

		switch op {
		case PUSH:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			val := vm.Constants[idx]
			if sFn, ok := val.V.(*value.Lambda); ok {
				closure := &value.Lambda{
					Address: sFn.Address,
					Params:  sFn.Params,
					Scope:   f.Vars,
					Parent:  f.Fn, // Scope chain: thấy được biến của các hàm bao ngoài
				}
				f.captured = true // map này đã escape vào closure → đừng tái dùng/xoá
				vm.push(value.New(closure))
			} else {
				vm.push(val)
			}
		case LOAD:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			// Thứ tự tra cứu: biến cục bộ → chuỗi scope closure → biến
			// top-level (vm.Vars) → Globals hệ thống.
			if v, ok := f.Vars[name]; ok {
				vm.push(v)
			} else if v, ok := lookupScopeChain(f.Fn, name); ok {
				vm.push(v)
			} else if v, ok := vm.Vars[name]; ok {
				vm.push(v)
			} else if v, ok := vm.Globals[name]; ok {
				vm.push(v)
			} else {
				vm.push(value.Value{K: value.Nil})
			}
		case BUILTIN:
			idx := vm.Bytecode[f.IP]
			f.IP++
			if int(idx) < len(vm.Builtins) {
				vm.push(vm.Builtins[idx])
			} else {
				vm.push(value.Value{K: value.Nil})
			}

		case STORE:
			idx := uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1])
			f.IP += 2
			name := vm.Constants[idx].V.(string)
			val := vm.peek()

			// Biến đã tồn tại ở scope bao ngoài (chuỗi closure) -> ghi vào đó
			if storeScopeChain(f.Fn, name, val) {
				continue
			}

			f.Vars[name] = val
		case ADD:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Add(b))
		case SUB:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Sub(b))
		case MUL:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Mul(b))
		case DIV:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Div(b))
		case MOD:
			b, a := vm.pop(), vm.pop()
			vm.push(a.Mod(b))
		case COMPARE:
			mode := vm.Bytecode[f.IP]
			f.IP++
			b, a := vm.pop(), vm.pop()
			vm.compare(a, b, mode)
		case JUMP:
			f.IP = int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
		case TRUE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if vm.pop().Truthy() {
				f.IP = addr
			}
		case FALSE:
			addr := int(uint16(vm.Bytecode[f.IP])<<8 | uint16(vm.Bytecode[f.IP+1]))
			f.IP += 2
			if !vm.pop().Truthy() {
				f.IP = addr
			}
		case AND:
			b, a := vm.pop(), vm.pop()
			if !a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case OR:
			b, a := vm.pop(), vm.pop()
			if a.Truthy() {
				vm.push(a)
			} else {
				vm.push(b)
			}
		case NOT:
			a := vm.pop()
			vm.push(value.ToBool(!a.Truthy()))
		case ITER:
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
		case DEFER:
			fn := vm.pop()
			if s, ok := fn.V.(*value.Lambda); ok {
				f.Defers = append(f.Defers, s)
			}
		case SPAWN:
			fn := vm.pop()
			if s, ok := fn.V.(*value.Lambda); ok && vm.Spawner != nil {
				vm.Spawner(s)
			}
		case RETURN:
			res := vm.pop()
			// A returned http request WITH a .then()/.catch() handler (e.g. an arrow-body
			// `ctx => http.get(url).then(A)`) fires + runs its handler here instead of leaking unfired.
			vm.finalizeStatement(res, true)
			for i := len(f.Defers) - 1; i >= 0; i-- {
				vm.ExecuteLambda(f.Defers[i], nil)
			}
			f.Defers = f.Defers[:0]

			if len(vm.Stack) > f.StackBase {
				vm.Stack = vm.Stack[:f.StackBase]
			}

			vm.FrameIdx--
			if vm.FrameIdx < startFrame {
				return res
			}
			vm.push(res)
		case CALL:
			// fmt.Printf("VM: OpCall Triggered at IP %d\n", f.IP-1)
			n := int(vm.Bytecode[f.IP])
			f.IP++
			fnArgs := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				fnArgs[i] = vm.pop()
			}
			fn := vm.pop()
			if fn.K == value.Func {
				if s, ok := fn.V.(*value.Lambda); ok {
					if vm.FrameIdx+1 >= len(vm.Frames) {
						return value.Value{
							K: value.Invalid,
							V: fmt.Sprintf("Stack overflow: Call stack limit exceeded (at line %d)", vm.currentLine(f.IP-1)),
						}
					}
					vm.FrameIdx++
					nf := &vm.Frames[vm.FrameIdx]
					nf.IP = s.Address
					nf.Fn = s
					nf.StackBase = len(vm.Stack)
					nf.Vars = make(map[string]value.Value) // Fresh map
					for i, name := range s.Params {
						if i < len(fnArgs) {
							nf.Vars[name] = fnArgs[i]
						}
					}
				} else if m, ok := fn.V.(value.Method); ok {
					vm.push(m(value.Value{K: value.Nil}, fnArgs...))
				} else if g, ok := fn.V.(func(...value.Value) value.Value); ok {
					// fmt.Printf("[VM CALL] Executing Go func (%T) with %d args\n", g, len(fnArgs))
					vm.push(g(fnArgs...))
				} else if _, ok := fn.V.(reflect.Value); ok {
					// fmt.Printf("[VM CALL] Executing reflect.Value call with %d args\n", len(fnArgs))
					vm.push(fn.Call(fn.Text(), fnArgs...))
				} else {
					// fmt.Printf("[VM CALL] Unknown func type: %T (Kind: %s)\n", fn.V, fn.K.String())
					vm.push(value.Value{K: value.Nil})
				}
			} else if fn.K == value.Proxy {
				if handler, ok := fn.V.(value.ProxyHandler); ok {
					// Use empty method name to signify a direct call to the proxy
					vm.push(handler.OnInvoke("", fnArgs...))
				}
			} else {
				vm.call(fn.Text(), fnArgs...)
			}
		case INVOKE:
			n := int(vm.Bytecode[f.IP])
			f.IP++
			m := vm.pop().Text()
			ivArgs := make([]value.Value, n)
			for i := n - 1; i >= 0; i-- {
				ivArgs[i] = vm.pop()
			}
			target := vm.pop()
			handled := false
			// Special Handling for cache.get(key, callback, ttl)
			if cacheObj, ok := target.V.(value.TenantCache); ok && m == "get" && len(ivArgs) > 0 {
				key := ivArgs[0].Text()
				if val, found := cacheObj.GetCache(key); found {
					vm.push(val)
					handled = true
				} else if len(ivArgs) > 1 && ivArgs[1].K == value.Func {
					callback := ivArgs[1].V.(*value.Lambda)
					val := vm.ExecuteLambda(callback, nil)
					var ttl value.Value
					if len(ivArgs) > 2 {
						ttl = ivArgs[2]
					}
					cacheObj.SetCache(key, val, ttl)
					vm.push(val)
					handled = true
				}
			}

			// Special Handling for Functional Methods (Map, Filter, Find)
			if !handled && target.K == value.Array && len(ivArgs) > 0 && ivArgs[0].K == value.Func {
				callback := ivArgs[0].V.(*value.Lambda)
				arr := *target.V.(*[]value.Value)

				switch m {
				case "map":
					resArr := make([]value.Value, len(arr))
					for i, item := range arr {
						resArr[i] = vm.ExecuteLambda(callback, []value.Value{item, value.New(float64(i))})
					}
					vm.push(value.New(resArr))
					handled = true
				case "filter":
					resArr := []value.Value{}
					for i, item := range arr {
						if vm.ExecuteLambda(callback, []value.Value{item, value.New(float64(i))}).Truthy() {
							resArr = append(resArr, item)
						}
					}
					vm.push(value.New(resArr))
					handled = true
				case "find":
					for i, item := range arr {
						if vm.ExecuteLambda(callback, []value.Value{item, value.New(float64(i))}).Truthy() {
							vm.push(item)
							handled = true
							break
						}
					}
					if !handled {
						vm.push(value.Value{K: value.Nil})
						handled = true
					}
				}
			}

			if !handled && target.K == value.Array {
				if res, ok := vm.arrayCallbackMethod(target, m, ivArgs); ok {
					vm.push(res)
					handled = true
				}
			}

			if !handled && target.K == value.Map {
				// obj.method() where the member is a SCRIPT lambda (obj = { f: () => … })
				// must be executed by the VM — value.Invoke/Call cannot run a *value.Lambda.
				if member := target.Get(m); member.K == value.Func {
					if lambda, ok := member.V.(*value.Lambda); ok {
						vm.push(vm.ExecuteLambda(lambda, ivArgs))
						handled = true
					}
				}
			}

			if !handled {
				vm.push(target.Invoke(m, ivArgs...))
			}
		case GET:
			keyVal := vm.pop()
			target := vm.pop()
			if keyVal.K == value.Number {
				vm.push(target.At(int(keyVal.N)))
			} else {
				vm.push(target.Get(keyVal.Text()))
			}
		case DUP:
			vm.push(vm.peek())
		case MAKE:
			t := vm.Bytecode[f.IP]
			f.IP++
			if t == 0 {
				vm.push(value.New(make(map[string]value.Value)))
			} else {
				vm.push(value.New(&[]value.Value{}))
			}
		case SET:
			val, key, target := vm.pop(), vm.pop(), vm.pop()
			if target.IsMap() {
				target.V.(map[string]value.Value)[key.Text()] = val
			} else if target.IsArray() {
				ptr := target.V.(*[]value.Value)
				*ptr = append(*ptr, val)
			}
			vm.push(target)
		case MERGE:
			src, target := vm.pop(), vm.peek()
			if target.IsMap() && src.IsMap() {
				targetMap := target.V.(map[string]value.Value)
				srcMap := src.Map()
				for k, v := range srcMap {
					targetMap[k] = v
				}
			}
		case POP:
			vm.pop()
		case POPFIN:
			vm.finalizeStatement(vm.pop(), false)
		case POPFINSOFT:
			vm.finalizeStatement(vm.pop(), true)
		}

		// Kiểm tra lỗi phát sinh sau khi thực thi instruction
		if len(vm.Stack) > 0 && vm.peek().K == value.Invalid {
			errVal := vm.pop()
			line := vm.currentLine(f.IP - 1)
			errMsg := errVal.String()
			if !strings.Contains(errMsg, "(at line") {
				errMsg = fmt.Sprintf("%s (at line %d)", errMsg, line)
			}
			return value.Value{K: value.Invalid, V: errMsg}
		}
	}
	if len(vm.Stack) > base {
		return vm.Stack[len(vm.Stack)-1]
	}
	return value.Value{K: value.Nil}
}

func (vm *VM) currentLine(ip int) int32 {
	if ip >= 0 && ip < len(vm.SourceMap) {
		return vm.SourceMap[ip]
	}
	return 0
}
