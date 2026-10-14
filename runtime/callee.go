package runtime

import (
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

// Calling a value that is not a function is a runtime error (14/10/2026), JavaScript's "x is not a
// function". The message says what was called as the source wrote it (f, o.x, o["x"]) and what kind of
// value it is, never the value: "o.x không phải hàm (là số): không gọi được". Where the call starts is
// the diagnostic's place (its at, "router.kitwork.js:12:5"), which a printed diagnostic leads with.

const (
	// unknownCallee stands for a part of a call the bytecode cannot name: a value a branch chose
	// ((c ? f : g)()), or one an operator made ((a || b).x()).
	unknownCallee = "(…)"
	// calledValue is the callee of a CALL whose name is not known at all.
	calledValue = "giá trị được gọi"

	// calleeSearchLimit bounds how many instructions one name lookup may visit.
	calleeSearchLimit = 4096
	// calleeNestingLimit bounds how deep a name is read back (o.a.b(…).c is four).
	calleeNestingLimit = 8
	// calleeTextLimit bounds a written name, in characters.
	calleeTextLimit = 80
)

// calledNotAFunction is the error of the CALL at ip, whose callee is the value called.
func (vm *VM) calledNotAFunction(ip int, called value.Value) value.Value {
	name, root := calledValue, -1
	if sites := vm.program.callSites(); sites != nil {
		if text, at := sites.callee(ip); text != unknownCallee {
			name, root = text, at
		}
	}
	return vm.notAFunction(ip, root, value.NotAFunctionMessage(shortenCallee(name), called))
}

// invokedNotAFunction is the error of the INVOKE at ip: message is what value.Invoke said, which starts
// with the method's name ("x không phải hàm …"); the object it was called on goes in front.
func (vm *VM) invokedNotAFunction(ip int, message string) value.Value {
	receiver, root := unknownCallee, -1
	if sites := vm.program.callSites(); sites != nil {
		receiver, root = sites.receiver(ip)
	}
	return vm.notAFunction(ip, root, shortenCallee(receiver)+"."+message)
}

// callbackNotAFunction is the error of the INVOKE at ip of an array method (map, filter, forEach…)
// whose callback, its first argument, is not a function: "items.forEach(…): đối số 1 không phải hàm
// (là số): không gọi được". JavaScript's "3 is not a function"; the method itself is there.
func (vm *VM) callbackNotAFunction(ip int, method string, count int, argument value.Value) value.Value {
	receiver, root := unknownCallee, -1
	if sites := vm.program.callSites(); sites != nil {
		receiver, root = sites.receiver(ip)
	}
	call := shortenCallee(receiver + "." + method + arguments(count))
	return vm.notAFunction(ip, root, call+": "+value.NotAFunctionMessage("đối số 1", argument))
}

// notAFunction makes the failure a runtime error like any other of the VM: inside safe(() => …) it is
// the error (its message the message, its code value.NotAFunctionCode, its at where the call starts);
// anywhere else it ends the run. The call starts at root, the first instruction of what was called,
// when the bytecode tells; otherwise at the call itself.
func (vm *VM) notAFunction(ip, root int, message string) value.Value {
	at := ip
	if root >= 0 {
		at = root
	}
	diagnostic := vm.buildDiagnostic(DiagnosticRuntimeError, message, at)
	diagnostic.CauseCode = value.NotAFunctionCode
	return diagnosticResult(diagnostic)
}

// callSites answers, after the fact, what a call was written as: the variable or member that put the
// called value on the stack and the instruction where that expression starts. The VM keeps no names at
// run time, so this is read back from the verified bytecode, and only when a call fails: the hot path
// never pays for it. A Program builds it once, on its first such failure.
type callSites struct {
	once         sync.Once
	ok           bool
	instructions []decodedInstruction
	byIP         map[int]int
	depth        map[int]int   // stack depth before each reachable instruction, in its own frame
	preds        map[int][]int // by instruction index: the instructions control can come from
	constants    []value.Value
}

// callSites is the Program's call-site table, built on first use; nil when it cannot be built.
func (p *Program) callSites() *callSites {
	if p == nil || p.calls == nil {
		return nil
	}
	p.calls.build(p.code, p.constants)
	if !p.calls.ok {
		return nil
	}
	return p.calls
}

func (s *callSites) build(code []byte, constants []value.Value) {
	s.once.Do(func() {
		instructions, byIP, _, err := decodeProgram(code)
		if err != nil {
			return
		}
		entries := []int{0}
		for _, constant := range constants {
			if lambda, ok := constant.V.(*value.Lambda); ok && lambda != nil {
				entries = append(entries, lambda.Address)
			}
		}
		_, depth, err := analyzeStack(entries, instructions, byIP, len(code))
		if err != nil {
			return
		}
		preds := make(map[int][]int, len(instructions))
		link := func(from, toIP int) {
			if to, ok := byIP[toIP]; ok {
				preds[to] = append(preds[to], from)
			}
		}
		for index, ins := range instructions {
			if _, reachable := depth[ins.ip]; !reachable {
				continue
			}
			switch ins.op {
			case RETURN, HALT:
			case JUMP:
				link(index, int(ins.operands[0]))
			case TRUE, FALSE, ITER:
				link(index, ins.next)
				link(index, int(ins.operands[0]))
			default:
				link(index, ins.next)
			}
		}
		s.instructions = instructions
		s.byIP = byIP
		s.depth = depth
		s.preds = preds
		s.constants = constants
		s.ok = true
	})
}

// callee names what the CALL at ip calls, and the instruction where it starts (-1 when unknown).
func (s *callSites) callee(ip int) (string, int) {
	index, depth, count, ok := s.call(ip, CALL)
	if !ok {
		return unknownCallee, -1
	}
	budget := calleeSearchLimit
	return s.describe(index, depth-count-1, true, calleeNestingLimit, &budget)
}

// receiver names the object the INVOKE at ip calls a method on, and the instruction where it starts.
func (s *callSites) receiver(ip int) (string, int) {
	index, depth, count, ok := s.call(ip, INVOKE)
	if !ok {
		return unknownCallee, -1
	}
	budget := calleeSearchLimit
	return s.describe(index, depth-count-2, false, calleeNestingLimit, &budget)
}

func (s *callSites) call(ip int, op Opcode) (index, depth, count int, ok bool) {
	index, ok = s.byIP[ip]
	if !ok {
		return 0, 0, 0, false
	}
	ins := s.instructions[index]
	depth, ok = s.depth[ip]
	if !ok || ins.op != op {
		return 0, 0, 0, false
	}
	return index, depth, int(ins.operands[0]), true
}

// producer finds the instruction that left the value in stack slot `slot` (counted in the frame) as
// control reaches the instruction at index `at`. Every path to `at` must agree on it: a value a branch
// chose (c ? f : g) has no single producer.
func (s *callSites) producer(at, slot int, budget *int) (int, bool) {
	found := -1
	visited := make(map[int]bool)
	queue := append([]int(nil), s.preds[at]...)
	for len(queue) > 0 {
		index := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if visited[index] {
			continue
		}
		visited[index] = true
		*budget--
		if *budget < 0 {
			return -1, false
		}
		ins := s.instructions[index]
		if ins.op == ITER {
			return -1, false
		}
		depth := s.depth[ins.ip]
		stackIn, stackOut := ins.spec.StackEffect(ins.operands)
		base := depth - stackIn
		switch {
		case ins.op == DUP && slot == base:
			// The lower of DUP's two copies is the value it was given, in the same slot: look past it.
			// A ?. tests its object with a DUP on each of two paths that meet again (o?.x()); both lead
			// back to the one instruction that made o.
			queue = append(queue, s.preds[index]...)
		case slot >= base && slot < base+stackOut:
			if found >= 0 && found != index {
				return -1, false
			}
			found = index
		case slot >= base:
			// It took the slot and left nothing there: the value seen at `at` did not come this way.
			return -1, false
		default:
			queue = append(queue, s.preds[index]...)
		}
	}
	return found, found >= 0
}

// describe writes the expression that left stack slot `slot` at instruction index `at`, and the
// instruction where it starts (-1 when unknown). A member read is written o["x"] when it is the called
// value itself (a call after a dot is an INVOKE, so a read then a call came from brackets) and o.x
// inside a chain.
func (s *callSites) describe(at, slot int, called bool, nesting int, budget *int) (string, int) {
	if nesting <= 0 || slot < 0 {
		return unknownCallee, -1
	}
	index, ok := s.producer(at, slot, budget)
	if !ok {
		return unknownCallee, -1
	}
	ins := s.instructions[index]
	depth := s.depth[ins.ip]
	switch ins.op {
	case LOAD:
		// a slot is the variable the source wrote (14/10/2026): error@3 and error#7 are error
		if name, ok := s.constant(ins).V.(string); ok && SlotName(name) != "" {
			return SlotName(name), ins.ip
		}
	case BUILTIN:
		return "kitwork", ins.ip
	case PUSH:
		constant := s.constant(ins)
		if _, isLambda := constant.V.(*value.Lambda); !isLambda {
			return literalText(constant), ins.ip
		}
	case MAKE:
		if ins.operands[0] == 0 {
			return "{…}", ins.ip
		}
		return "[…]", ins.ip
	case DUP, STORE, COMMIT:
		// each passes on the value it was given
		return s.describe(index, depth-1, called, nesting-1, budget)
	case GET:
		if variable, root, ok := s.iterationVariable(index, depth, budget); ok {
			return variable, root
		}
		target, root := s.describe(index, depth-2, false, nesting-1, budget)
		key, kind := s.key(index, depth-1, budget)
		switch {
		case kind == value.String && !called && isPlainName(key):
			return target + "." + key, root
		case kind == value.String:
			return target + "[" + strconv.Quote(key) + "]", root
		default:
			return target + "[" + key + "]", root
		}
	case INVOKE:
		count := int(ins.operands[0])
		target, root := s.describe(index, depth-count-2, false, nesting-1, budget)
		if method, kind := s.key(index, depth-1, budget); kind == value.String {
			return target + "." + method + arguments(count), root
		}
	case CALL:
		count := int(ins.operands[0])
		callee, root := s.describe(index, depth-count-1, true, nesting-1, budget)
		if callee == unknownCallee {
			return unknownCallee, -1
		}
		return callee + arguments(count), root
	}
	return unknownCallee, -1
}

// iterationVariable reads back a variable of a loop's iteration that a closure reaches: it lives in the
// object the iteration made, so reading it is LOAD #N; PUSH "x#5"; GET, which the source wrote x.
func (s *callSites) iterationVariable(get, depth int, budget *int) (string, int, bool) {
	index, ok := s.producer(get, depth-2, budget)
	if !ok || s.instructions[index].op != LOAD {
		return "", -1, false
	}
	load := s.instructions[index]
	if slot, ok := s.constant(load).V.(string); !ok || !isIterationSlot(slot) {
		return "", -1, false
	}
	key, kind := s.key(get, depth-1, budget)
	if kind != value.String || SlotName(key) == "" {
		return "", -1, false
	}
	return SlotName(key), load.ip, true
}

// key reads the key of a member read: a written string or number, or the name of a variable.
func (s *callSites) key(at, slot int, budget *int) (string, value.Kind) {
	index, ok := s.producer(at, slot, budget)
	if !ok {
		return "…", value.Invalid
	}
	ins := s.instructions[index]
	switch ins.op {
	case PUSH:
		constant := s.constant(ins)
		switch constant.K {
		case value.String:
			text, _ := constant.V.(string)
			return text, value.String
		case value.Number:
			return constant.Text(), value.Number
		}
	case LOAD:
		if name, ok := s.constant(ins).V.(string); ok {
			return SlotName(name), value.Nil
		}
	}
	return "…", value.Invalid
}

func (s *callSites) constant(ins decodedInstruction) value.Value {
	index := int(ins.operands[0])
	if index < 0 || index >= len(s.constants) {
		return value.Value{K: value.Nil}
	}
	return s.constants[index]
}

func arguments(count int) string {
	if count == 0 {
		return "()"
	}
	return "(…)"
}

// literalText writes a constant as the source wrote it.
func literalText(constant value.Value) string {
	if constant.K == value.String {
		text, _ := constant.V.(string)
		return strconv.Quote(text)
	}
	return constant.Text()
}

func isPlainName(name string) bool {
	if name == "" {
		return false
	}
	for index, char := range name {
		switch {
		case char == '_' || char == '$':
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
}

// shortenCallee keeps a written name to one readable line.
func shortenCallee(name string) string {
	if utf8.RuneCountInString(name) <= calleeTextLimit {
		return name
	}
	runes := []rune(name)
	return string(runes[:calleeTextLimit-1]) + "…"
}
