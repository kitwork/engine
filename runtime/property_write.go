package runtime

import (
	"strconv"

	"github.com/kitwork/engine/value"
)

// A write through a value that cannot hold properties is a runtime error (15/10/2026, the owner's L1), as
// JavaScript's TypeError: null.x = 1, o.missing.x = 1 (o.missing is null), and a member or an item of a
// string, a number, a boolean or a function (o.name.x = 1 where o.name is a string, s[0] = "x"). Until
// then SET dropped such a write without a word. JavaScript keeps f.x = 1 on a function, which is an object
// there; Kitwork keeps no member a program writes on any function, an arrow function or one of the
// engine's (Number, Math.max), whose members are the engine's own, so the write is the error rather
// than lost.
//
// The message names the write as the source wrote it and the kind of what it writes through, never a
// value (a key or a value can be a secret a request carried): "không ghi được o.name.x: o.name là chuỗi,
// không phải object hay mảng". Its place is the write's dot or bracket, its code TYPE, so safe(() => …)
// catches it as it catches "a" * 2.
//
// An object and an array take the write as before. A value of the host keeps the rule it had: a Struct
// (ctx, the kitwork object), a Proxy (a query builder's row), an engine time, duration or bytes (typeof
// "object") and any other Go value take no write from a script, and the write is dropped without an
// error, as it always was (the owner's list is null, the primitives and functions; 15/10/2026, the review
// took time, duration and bytes back out of it).

// cannotHoldProperties reports the kinds a write through is the error: JavaScript's primitives, null,
// and Kitwork's functions.
func cannotHoldProperties(kind value.Kind) bool {
	switch kind {
	case value.Nil, value.Number, value.Bool, value.String, value.Func:
		return true
	}
	return false
}

// writeThroughNonHolder is the error of the SET at ip, whose target cannot hold properties.
func (vm *VM) writeThroughNonHolder(ip int, target value.Value) value.Value {
	write, through := unknownCallee, unknownCallee
	if sites := vm.program.callSites(); sites != nil {
		write, through = sites.written(ip)
	}
	message := "không ghi được " + shortenCallee(write) + ": " + shortenCallee(through) + " là " +
		value.KindName(target) + ", không phải object hay mảng"
	diagnostic := vm.buildDiagnostic(DiagnosticRuntimeError, message, ip)
	diagnostic.CauseCode = value.TypeCode
	return diagnosticResult(diagnostic)
}

// written names what the SET at ip writes (o.name.x, s[0], grid[1][k]) and the value it writes through
// (o.name, s, grid[1]), as the source wrote them, read back from the bytecode as a failed call's callee
// is; "(…)" stands for a part it cannot name.
func (s *callSites) written(ip int) (string, string) {
	index, ok := s.byIP[ip]
	if !ok || s.instructions[index].op != SET {
		return unknownCallee, unknownCallee
	}
	depth, ok := s.depth[ip]
	if !ok {
		return unknownCallee, unknownCallee
	}
	budget := calleeSearchLimit
	target, _ := s.describe(index, depth-3, false, calleeNestingLimit, &budget)
	key, kind := s.key(index, depth-2, &budget)
	switch {
	case kind == value.String && isPlainName(key):
		return target + "." + key, target
	case kind == value.String:
		return target + "[" + strconv.Quote(key) + "]", target
	default:
		return target + "[" + key + "]", target
	}
}

// toNumber is value.ToNumber, the number ++ and -- step (STEP): a date made by new Date() steps from its
// time value in milliseconds, as isNaN, Number() and Math read it (value.DateTimeValue). No valueOf is
// called, neither the program's (the object is NaN: ++ runs no code of the program) nor one copied from a
// date into another object ({ valueOf: d.valueOf }, { ...d }), which a native valueOf once made a date
// (15/10/2026).
func (vm *VM) toNumber(v value.Value) value.Value {
	return value.ToNumber(v)
}
