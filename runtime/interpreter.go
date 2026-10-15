package runtime

import (
	"reflect"

	"github.com/kitwork/engine/value"
)

// Run executes the root frame of the currently loaded Program.
func (vm *VM) Run() (result value.Value) {
	if vm == nil || vm.program == nil {
		return vm.diagnosticValue(DiagnosticNoProgram, "VM has no verified program", -1)
	}
	if vm.FrameIdx < 0 {
		return vm.diagnosticValue(DiagnosticVMStopped, "VM is stopped", -1)
	}
	defer func() {
		if vm.FrameIdx > 0 {
			vm.FrameIdx = 0
		}
	}()
	return vm.execute(0)
}

// ExecuteLambda binds a lambda to a new frame and runs it through the same
// interpreter used by Run. The caller frame and stack are restored on every
// exit path, including HALT, cancellation, energy exhaustion, and errors.
func (vm *VM) ExecuteLambda(lambda *value.Lambda, args []value.Value) (result value.Value) {
	if lambda == nil {
		return value.Value{K: value.Nil}
	}
	if vm == nil || vm.program == nil {
		return vm.diagnosticValue(DiagnosticNoProgram, "VM has no verified program", -1)
	}

	callerFrame := vm.FrameIdx
	stackBase := len(vm.Stack)
	if fault := vm.pushLambdaFrame(lambda, args); fault != nil {
		return vm.diagnosticValue(fault.code, fault.message, vm.Frames[vm.FrameIdx].LastIP)
	}
	floor := vm.FrameIdx
	return vm.executePreparedLambda(callerFrame, stackBase, floor)
}

// ExecuteLambdaScope runs a lambda like ExecuteLambda and also returns the names its body declared
// (parameters included), as they stand when it returns. Kitwork UI reads a view this way: the
// const and var of router.view are what the interface sees. A const or let of a block inside the
// view lives in that block (14/10/2026) and is not one of them. The map is handed over, so the frame
// slot never reuses it.
func (vm *VM) ExecuteLambdaScope(lambda *value.Lambda, args []value.Value) (value.Value, map[string]value.Value) {
	if lambda == nil {
		return value.Value{K: value.Nil}, nil
	}
	if vm == nil || vm.program == nil {
		return vm.diagnosticValue(DiagnosticNoProgram, "VM has no verified program", -1), nil
	}

	callerFrame := vm.FrameIdx
	stackBase := len(vm.Stack)
	if fault := vm.pushLambdaFrame(lambda, args); fault != nil {
		return vm.diagnosticValue(fault.code, fault.message, vm.Frames[vm.FrameIdx].LastIP), nil
	}
	floor := vm.FrameIdx
	result := vm.executePreparedLambda(callerFrame, stackBase, floor)
	frame := &vm.Frames[floor]
	locals := frame.Vars
	frame.captured = true // handed out: the next call into this slot allocates a fresh map
	return result, DeclaredNames(locals)
}

func (vm *VM) executePreparedLambda(callerFrame, stackBase, floor int) (result value.Value) {
	defer func() {
		if len(vm.Stack) > stackBase {
			vm.Stack = vm.Stack[:stackBase]
		}
		vm.FrameIdx = callerFrame
	}()

	return vm.execute(floor)
}

func (vm *VM) executeLambdaFixed(
	lambda *value.Lambda,
	count int,
	first value.Value,
	second value.Value,
	third value.Value,
) value.Value {
	if lambda == nil {
		return value.Value{K: value.Nil}
	}
	if vm == nil || vm.program == nil {
		return vm.diagnosticValue(DiagnosticNoProgram, "VM has no verified program", -1)
	}

	callerFrame := vm.FrameIdx
	stackBase := len(vm.Stack)
	frame, fault := vm.prepareLambdaFrame(lambda)
	if fault != nil {
		return vm.diagnosticValue(fault.code, fault.message, vm.Frames[vm.FrameIdx].LastIP)
	}
	if count > 0 && len(lambda.Params) > 0 {
		frame.Vars[lambda.Params[0]] = first
	}
	if count > 1 && len(lambda.Params) > 1 {
		frame.Vars[lambda.Params[1]] = second
	}
	if count > 2 && len(lambda.Params) > 2 {
		frame.Vars[lambda.Params[2]] = third
	}
	return vm.executePreparedLambda(callerFrame, stackBase, vm.FrameIdx)
}

func (vm *VM) executeLambda1(lambda *value.Lambda, first value.Value) value.Value {
	return vm.executeLambdaFixed(
		lambda,
		1,
		first,
		value.Value{K: value.Nil},
		value.Value{K: value.Nil},
	)
}

func (vm *VM) executeLambda2(
	lambda *value.Lambda,
	first value.Value,
	second value.Value,
) value.Value {
	return vm.executeLambdaFixed(
		lambda,
		2,
		first,
		second,
		value.Value{K: value.Nil},
	)
}

func (vm *VM) executeLambda3(
	lambda *value.Lambda,
	first value.Value,
	second value.Value,
	third value.Value,
) value.Value {
	return vm.executeLambdaFixed(lambda, 3, first, second, third)
}

// execute is the VM's only bytecode dispatch loop. floor is the first frame
// owned by this invocation; returning below it hands control back to the caller.
func (vm *VM) execute(floor int) value.Value {
	for vm.FrameIdx >= floor {
		frame := &vm.Frames[vm.FrameIdx]

		if frame.IP >= len(vm.program.code) {
			result := value.Value{K: value.Nil}
			if len(vm.Stack) > frame.StackBase {
				result = vm.pop()
			}
			finished, result := vm.finishFrame(frame, floor, result)
			if finished {
				return result
			}
			if result.K == value.Invalid {
				return vm.exitFailure(floor, result)
			}
			continue
		}

		opIP := frame.IP
		op := Opcode(vm.program.code[frame.IP])
		frame.IP++
		frame.LastIP = opIP

		// Program construction verified every opcode, operand width, constant,
		// jump, and stack transition before publication. The immutable dispatch
		// path can therefore read canonical metadata directly.
		spec := &instructionTable[uint8(op)]

		if !vm.consumeEnergy(spec.Energy) {
			return vm.exitFailure(floor, vm.diagnosticValue(
				DiagnosticEnergyLimit,
				"Energy Limit Exceeded: Execution halted",
				opIP,
			))
		}

		vm.instructions++
		if vm.instructions&cancellationCheckMask == 0 {
			if failure, failed := vm.checkpoint(opIP); failed {
				return vm.exitFailure(floor, failure)
			}
		}

		switch op {
		case PUSH, PUSH_WIDE:
			index := vm.readOperand(frame, spec.operandSize)
			constant := vm.program.constants[index]
			if template, ok := constant.V.(*value.Lambda); ok {
				closure := &value.Lambda{
					Address:      template.Address,
					Name:         template.Name,
					SourceFile:   template.SourceFile,
					SourceLine:   template.SourceLine,
					SourceColumn: template.SourceColumn,
					Params:       append([]string(nil), template.Params...),
					Scope:        frame.Vars,
					Parent:       frame.Fn,
					Program:      vm.program,
				}
				frame.captured = true
				vm.push(value.New(closure))
			} else {
				vm.push(constant)
			}

		case POP:
			vm.pop()

		case LOAD, LOAD_WIDE:
			name := vm.program.constants[vm.readOperand(frame, spec.operandSize)].V.(string)
			vm.push(vm.load(frame, name))

		case STORE, STORE_WIDE:
			name := vm.program.constants[vm.readOperand(frame, spec.operandSize)].V.(string)
			vm.store(frame, name, vm.peek())

		case GET:
			key := vm.pop()
			target := vm.pop()
			if key.K == value.Number {
				vm.push(target.At(int(key.N)))
			} else {
				vm.push(vm.nativeProperty(target, key.Text()))
			}

		case DUP:
			vm.push(vm.peek())

		case BUILTIN:
			index := int(vm.program.code[frame.IP])
			frame.IP++
			if index < len(vm.Builtins) {
				vm.push(vm.Builtins[index])
			} else {
				vm.push(value.Value{K: value.Nil})
			}

		case ADD:
			right, left := vm.pop(), vm.pop()
			vm.push(left.Add(right))
		case SUB:
			right, left := vm.pop(), vm.pop()
			vm.push(left.Sub(right))
		case MUL:
			right, left := vm.pop(), vm.pop()
			vm.push(left.Mul(right))
		case DIV:
			right, left := vm.pop(), vm.pop()
			vm.push(left.Div(right))
		case MOD:
			right, left := vm.pop(), vm.pop()
			vm.push(left.Mod(right))

		case AND:
			right, left := vm.pop(), vm.pop()
			if left.Truthy() {
				vm.push(right)
			} else {
				vm.push(left)
			}
		case OR:
			right, left := vm.pop(), vm.pop()
			if left.Truthy() {
				vm.push(left)
			} else {
				vm.push(right)
			}
		case NOT:
			vm.push(value.ToBool(!vm.pop().Truthy()))

		case COMPARE:
			mode := vm.program.code[frame.IP]
			frame.IP++
			right, left := vm.pop(), vm.pop()
			vm.compare(left, right, mode)

		case JUMP, JUMP_WIDE:
			frame.IP = vm.readOperand(frame, spec.operandSize)
		case TRUE, TRUE_WIDE:
			address := vm.readOperand(frame, spec.operandSize)
			if vm.pop().Truthy() {
				frame.IP = address
			}
		case FALSE, FALSE_WIDE:
			address := vm.readOperand(frame, spec.operandSize)
			if !vm.pop().Truthy() {
				frame.IP = address
			}

		case ITER, ITER_WIDE:
			address := vm.readOperand(frame, spec.operandSize)
			index := vm.pop()
			collection := vm.peek()
			if int(index.N) < collection.Len() {
				vm.push(value.Value{K: value.Number, N: index.N + 1})
				vm.push(collection.At(int(index.N)))
			} else {
				vm.pop()
				frame.IP = address
			}

		case MAKE:
			kind := vm.program.code[frame.IP]
			frame.IP++
			if kind == 0 {
				vm.push(value.New(make(map[string]value.Value)))
			} else {
				vm.push(value.New(&[]value.Value{}))
			}

		case SET:
			item, key, target := vm.pop(), vm.pop(), vm.pop()
			if target.IsMap() {
				target.V.(map[string]value.Value)[key.Text()] = item
			} else if target.IsArray() {
				// a[i] = v sets item i as JavaScript does: the length appends, past it the gap is null, a key
				// that is no index is a failure (array_item.go, 14/10/2026)
				if failure := vm.setArrayItem(target, key, item, opIP); failure.K == value.Invalid {
					vm.push(failure)
					break
				}
			}
			vm.push(target)

		case MERGE:
			source, target := vm.pop(), vm.peek()
			if target.IsMap() && source.IsMap() {
				targetMap := target.V.(map[string]value.Value)
				for key, item := range source.Map() {
					targetMap[key] = item
				}
			}

		case CALL:
			vm.executeCall(frame)

		case INVOKE:
			vm.executeInvoke(frame)

		case RETURN:
			result := vm.pop()
			finished, result := vm.finishFrame(frame, floor, result)
			if finished {
				return result
			}

		case HALT:
			result := vm.pop()
			result = vm.unwindFrames(floor, result)
			vm.unwindToCaller(floor)
			return result

		case DEFER:
			deferred := vm.pop()
			if lambda, ok := deferred.V.(*value.Lambda); ok {
				frame.Defers = append(frame.Defers, lambda)
			}

		case SPAWN:
			spawned := vm.pop()
			if lambda, ok := spawned.V.(*value.Lambda); ok && vm.Spawner != nil {
				if failure := vm.nativeAction("SPAWN", func() {
					vm.Spawner(lambda)
				}); failure.K == value.Invalid {
					vm.push(failure)
				}
			}

		case COMMIT:
			committed := vm.commit(vm.pop())
			vm.push(committed)
		}

		activeFrame := &vm.Frames[vm.FrameIdx]
		if len(vm.Stack) > activeFrame.StackBase &&
			vm.Stack[len(vm.Stack)-1].K == value.Invalid {
			invalid := vm.Stack[len(vm.Stack)-1]
			vm.Stack = vm.Stack[:len(vm.Stack)-1]
			if _, structured := DiagnosticFrom(invalid); structured {
				return vm.exitFailure(floor, invalid)
			}
			message := invalid.String()
			failure, hasFailure := value.FailureFrom(invalid)
			if hasFailure {
				message = failure.Message
			}
			// A host call that failed because the request is over (a query's "context canceled", a VM
			// asked of a closed scope, SQLite's CANCELLED) is the cancellation, which nothing catches;
			// a host that wrapped a failure nothing catches keeps it so; any other failure of the host
			// is a runtime error, caught by safe(() => …).
			if (hasFailure && failure.Code == value.CancelledCode) || vm.cancelled() {
				return vm.exitFailure(floor, vm.diagnosticValue(DiagnosticCancelled, cancelledMessage(message), opIP))
			}
			if code, never := uncatchableCode(failure.Code); hasFailure && never {
				return vm.exitFailure(floor, vm.diagnosticValue(code, message, opIP))
			}
			diagnostic := vm.buildDiagnostic(DiagnosticRuntimeError, message, opIP)
			if hasFailure {
				diagnostic.CauseCode = failure.Code
				diagnostic.Progress = failure.Progress
			}
			return vm.exitFailure(
				floor,
				diagnosticResult(diagnostic),
			)
		}
	}

	return value.Value{K: value.Nil}
}

// readOperand reads a constant index or a jump address: two bytes, or three in a wide form (size is
// the instruction's operand size, which the verifier proved).
func (vm *VM) readOperand(frame *Frame, size uint8) int {
	code, ip := vm.program.code, frame.IP
	if size == 3 {
		frame.IP = ip + 3
		return int(code[ip])<<16 | int(code[ip+1])<<8 | int(code[ip+2])
	}
	frame.IP = ip + 2
	return int(code[ip])<<8 | int(code[ip+1])
}

func (vm *VM) load(frame *Frame, name string) value.Value {
	if item, ok := frame.Vars[name]; ok {
		return item
	}
	if item, ok := lookupScopeChain(frame.Fn, name); ok {
		return item
	}
	if item, ok := vm.Vars[name]; ok {
		return item
	}
	if item, ok := vm.Globals[name]; ok {
		return item
	}
	return value.Value{K: value.Nil}
}

func (vm *VM) store(frame *Frame, name string, item value.Value) {
	if storeScopeChain(frame.Fn, name, item) {
		return
	}
	if vm.FrameIdx == 0 {
		vm.Vars[name] = item
		return
	}
	frame.Vars[name] = item
}

func (vm *VM) pushLambdaFrame(lambda *value.Lambda, args []value.Value) *runtimeFault {
	frame, fault := vm.prepareLambdaFrame(lambda)
	if fault != nil {
		return fault
	}
	for index, name := range lambda.Params {
		if index < len(args) {
			frame.Vars[name] = args[index]
		}
	}
	return nil
}

func (vm *VM) prepareLambdaFrame(lambda *value.Lambda) (*Frame, *runtimeFault) {
	if lambda.Program != nil {
		owner, ok := ProgramFromRef(lambda.Program)
		if !ok || owner != vm.program {
			return nil, &runtimeFault{
				code:    DiagnosticProgramMismatch,
				message: "lambda belongs to a different program",
			}
		}
	}
	// callFloor is the frames held by the VMs this one runs inside (nesting.go): one ceiling for all.
	if vm.callFloor+vm.FrameIdx+1 >= defaultMaxCallDepth || vm.FrameIdx+1 >= len(vm.Frames) {
		return nil, &runtimeFault{
			code:    DiagnosticStackOverflow,
			message: "Stack overflow: Call stack limit exceeded",
		}
	}

	vm.FrameIdx++
	if vm.FrameIdx > vm.frameHighWater {
		vm.frameHighWater = vm.FrameIdx
	}
	frame := &vm.Frames[vm.FrameIdx]
	frame.IP = lambda.Address
	frame.LastIP = -1
	frame.Fn = lambda
	frame.StackBase = len(vm.Stack)

	if frame.Vars == nil || frame.captured {
		frame.Vars = make(map[string]value.Value)
	} else {
		clear(frame.Vars)
	}
	frame.captured = false
	clear(frame.Defers)
	frame.Defers = frame.Defers[:0]

	return frame, nil
}

func (vm *VM) finishFrame(
	frame *Frame,
	floor int,
	result value.Value,
) (bool, value.Value) {
	result = vm.runFrameDefers(frame, result)

	if len(vm.Stack) > frame.StackBase {
		vm.Stack = vm.Stack[:frame.StackBase]
	}

	if vm.FrameIdx == floor {
		if floor > 0 {
			vm.FrameIdx--
		}
		return true, result
	}

	vm.FrameIdx--
	vm.push(result)
	return false, result
}

func (vm *VM) runFrameDefers(frame *Frame, result value.Value) value.Value {
	for index := len(frame.Defers) - 1; index >= 0; index-- {
		deferredResult := vm.ExecuteLambda(frame.Defers[index], nil)
		result = mergeFailures(result, deferredResult)
	}
	clear(frame.Defers)
	frame.Defers = frame.Defers[:0]
	return result
}

func (vm *VM) unwindFrames(floor int, result value.Value) value.Value {
	for vm.FrameIdx >= floor {
		frame := &vm.Frames[vm.FrameIdx]
		result = vm.runFrameDefers(frame, result)
		if len(vm.Stack) > frame.StackBase {
			vm.Stack = vm.Stack[:frame.StackBase]
		}
		if vm.FrameIdx == floor {
			break
		}
		vm.FrameIdx--
	}
	return result
}

func (vm *VM) exitFailure(floor int, failure value.Value) value.Value {
	diagnostic, ok := DiagnosticFrom(failure)
	if !ok || diagnostic.Code != DiagnosticEnergyLimit || vm.MaxEnergy == 0 {
		return vm.unwindFrames(floor, failure)
	}

	originalLimit := vm.MaxEnergy
	cleanupLimit := vm.Energy + cleanupEnergyReserve
	if cleanupLimit < vm.Energy {
		cleanupLimit = ^uint64(0)
	}
	vm.MaxEnergy = cleanupLimit
	result := vm.unwindFrames(floor, failure)
	vm.MaxEnergy = originalLimit
	return result
}

func (vm *VM) unwindToCaller(floor int) {
	if floor > 0 {
		vm.FrameIdx = floor - 1
		return
	}
	vm.FrameIdx = 0
}

func (vm *VM) executeCall(frame *Frame) {
	count := int(vm.program.code[frame.IP])
	frame.IP++

	// Script lambdas consume arguments immediately into their frame. Bind from
	// the verified VM stack directly so an internal call does not allocate an
	// argument slice that no caller can retain.
	callableIndex := len(vm.Stack) - count - 1
	if callableIndex >= frame.StackBase {
		callable := vm.Stack[callableIndex]
		if callable.K == value.Func {
			if function, ok := callable.V.(*value.Lambda); ok {
				args := vm.Stack[callableIndex+1:]
				vm.Stack = vm.Stack[:callableIndex]
				if fault := vm.pushLambdaFrame(function, args); fault != nil {
					vm.push(vm.diagnosticValue(fault.code, fault.message, frame.LastIP))
				}
				return
			}
		}
	}

	args := make([]value.Value, count)
	for index := count - 1; index >= 0; index-- {
		args[index] = vm.pop()
	}
	callable := vm.pop()
	// The global safe runs its function on this VM (safe.go), so it is the VM's to call.
	if value.IsSafeFunction(callable) {
		vm.push(vm.callSafe(frame.LastIP, args))
		return
	}
	// The host may run a lambda among args in a VM nested in this one (nesting.go: LambdaCaller).
	if mark := vm.passLambdas(args); len(vm.passing) > mark {
		defer vm.withdrawLambdas(mark)
	}

	if result, callable := vm.callHost(callable, args); callable {
		vm.push(result)
		return
	}
	if callable.K == value.Proxy {
		// a proxy with no handler answers nothing
		return
	}

	// Anything else is not a function: calling it is a runtime error (14/10/2026), and it never prints.
	// Until then such a call gave null, and printed its arguments to standard output when the value was
	// the string "log" or "PRINT", so data a request carried could write a secret into the server's log.
	vm.push(vm.calledNotAFunction(frame.LastIP, callable))
}

// callHost calls a function of the host (not a script lambda) with args, as CALL calls it; callable is
// false when the value is not one. A function value of a kind the VM does not know gives null.
func (vm *VM) callHost(callable value.Value, args []value.Value) (result value.Value, ok bool) {
	if callable.K == value.Func {
		switch function := callable.V.(type) {
		case value.Method:
			return vm.nativeValue("function call", func() value.Value {
				return function(value.Value{K: value.Nil}, args...)
			}), true
		case func(...value.Value) value.Value:
			return vm.nativeValue("function call", func() value.Value {
				return function(args...)
			}), true
		case *value.FuncObject:
			return vm.nativeValue("function object call", func() value.Value {
				return function.Fn(args...)
			}), true
		case reflect.Value:
			return vm.nativeValue("reflected function call", func() value.Value {
				return callable.Call(callable.Text(), args...)
			}), true
		case value.SafeFunction:
			return vm.callSafe(vm.currentInstruction(), args), true
		default:
			return value.Value{K: value.Nil}, true
		}
	}
	if callable.K == value.Proxy {
		if handler, ok := callable.V.(value.ProxyHandler); ok {
			return vm.nativeValue("proxy call", func() value.Value {
				return handler.OnInvoke("", args...)
			}), true
		}
	}
	return value.Value{}, false
}

func (vm *VM) executeInvoke(frame *Frame) {
	count := int(vm.program.code[frame.IP])
	frame.IP++
	method := vm.pop().Text()

	// typeof x: answered before any key or method of x (TypeofMethod, array_item.go)
	if method == TypeofMethod && count == 0 {
		vm.push(value.NewString(vm.pop().TypeOf()))
		return
	}

	targetIndex := len(vm.Stack) - count - 1
	if targetIndex >= frame.StackBase &&
		vm.Stack[targetIndex].K == value.Array &&
		isArrayCallbackMethod(method) {
		target := vm.Stack[targetIndex]
		argument := value.Value{K: value.Nil}
		if count > 0 {
			argument = vm.Stack[targetIndex+1]
		}
		// The callback is called for each element when it is a function: a script lambda, or a function
		// of the engine (console.log, Boolean), as JavaScript calls it. Anything else is not a function,
		// so the call is the runtime error (14/10/2026), naming the argument; sort() and unique() without
		// one keep their own order and key.
		if callback, ok := newArrayCallback(argument); ok {
			initial := value.Value{K: value.Nil}
			hasInitial := count > 1
			if hasInitial {
				initial = vm.Stack[targetIndex+2]
			}
			vm.Stack = vm.Stack[:targetIndex]
			result, _ := vm.arrayCallbackMethod(
				target,
				method,
				callback,
				initial,
				hasInitial,
			)
			vm.push(result)
			return
		}
		if argument.K != value.Nil || (method != "sort" && method != "unique") {
			clear(vm.Stack[targetIndex:])
			vm.Stack = vm.Stack[:targetIndex]
			vm.push(vm.callbackNotAFunction(frame.LastIP, method, count, argument))
			return
		}
	}

	if targetIndex >= frame.StackBase {
		target := vm.Stack[targetIndex]
		// A call reaches an object's own key only when the key holds a function (value.CallsOwn):
		// { delete: f }.delete("k") calls f. An own key holding data never answers a call, so
		// { pick: {…} }.pick("name") is the builtin, as on an object without that key.
		if standardMethodUsesStackArgs(target.K) && !target.CallsOwn(method) {
			if standard, ok := target.K.StandardMethod(method); ok {
				top := len(vm.Stack)
				args := vm.Stack[targetIndex+1 : top : top]
				result := vm.nativeStandardMethod(target, method, standard, args)
				clear(vm.Stack[targetIndex:top])
				vm.Stack = vm.Stack[:targetIndex]
				vm.push(result)
				return
			}
		}
	}

	args := make([]value.Value, count)
	for index := count - 1; index >= 0; index-- {
		args[index] = vm.pop()
	}
	target := vm.pop()

	if cache, ok := target.V.(value.TenantCache); ok &&
		method == "get" &&
		len(args) > 0 {
		key := args[0].Text()
		var cached value.Value
		var found bool
		if failure := vm.nativeAction("cache get", func() {
			cached, found = cache.GetCache(key)
		}); failure.K == value.Invalid {
			vm.push(failure)
			return
		}
		if found {
			vm.push(cached)
			return
		}
		if len(args) > 1 {
			if callback, ok := args[1].V.(*value.Lambda); ok {
				result := vm.ExecuteLambda(callback, nil)
				if result.K == value.Invalid {
					vm.push(result)
					return
				}
				ttl := value.Value{K: value.Nil}
				if len(args) > 2 {
					ttl = args[2]
				}
				if failure := vm.nativeAction("cache set", func() {
					cache.SetCache(key, result, ttl)
				}); failure.K == value.Invalid {
					vm.push(failure)
					return
				}
				vm.push(result)
				return
			}
		}
	}

	if target.K == value.Map {
		if member := target.Get(method); member.K == value.Func {
			if lambda, ok := member.V.(*value.Lambda); ok {
				vm.push(vm.ExecuteLambda(lambda, args))
				return
			}
		}
	}

	// o.safe(f) where o.safe is the global safe (kitwork().safe, { safe }): the VM runs it, as CALL does.
	if method == "safe" && (target.K == value.Map || target.K == value.Struct) {
		if member := vm.nativeProperty(target, method); value.IsSafeFunction(member) {
			vm.push(vm.callSafe(frame.LastIP, args))
			return
		}
	}

	result := vm.nativeMethod(target, method, args)
	if message, ok := value.NotAFunctionFrom(result); ok {
		result = vm.invokedNotAFunction(frame.LastIP, message)
	}
	vm.push(result)
}

func standardMethodUsesStackArgs(kind value.Kind) bool {
	switch kind {
	case value.Number,
		value.Bool,
		value.Time,
		value.Duration,
		value.String,
		value.Bytes,
		value.Map,
		value.Array:
		return true
	default:
		return false
	}
}
