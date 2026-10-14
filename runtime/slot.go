package runtime

import (
	"strings"

	"github.com/kitwork/engine/value"
)

// Variable slots (14/10/2026). The compiler resolves every name before it emits code and gives each
// variable the name LOAD and STORE use for it, its slot, so a variable of a block or of a nested
// function never meets another of the same name in a function's map:
//
//	name       a declaration of a function's own scope, a parameter, the program's top level, and any
//	           name nothing declares (var x = …, a global)
//	name@N     a declaration of a function's own scope whose name a function around may hold
//	name#N     a declaration in a block (if, else, a loop, a switch)
//	#N         the object a loop makes for each iteration, holding the variables of that iteration
//	           that a closure reaches, under their slots
//
// Identifiers never contain @ or #, so a slot never names another variable. The VM itself does not
// read slots: it looks names up as it always has.
const (
	SlotFunctionMark = '@'
	SlotBlockMark    = '#'
)

// SlotName is the variable a slot holds as the source wrote it: error@3 and error#7 are error, a name
// without a mark is itself, and an iteration's object (#4) is "".
func SlotName(slot string) string {
	if index := strings.IndexAny(slot, "@#"); index >= 0 {
		return slot[:index]
	}
	return slot
}

// isIterationSlot reports whether a slot is the object a loop makes for each iteration.
func isIterationSlot(slot string) bool {
	return len(slot) > 1 && slot[0] == SlotBlockMark
}

// DeclaredNames turns a function's map into the names its body declared, as Kitwork UI reads a view
// and the files around it: name@N is name (it wins over a parameter's own entry of that name, which a
// copied parameter leaves behind), and a variable of a block (name#N) or an iteration's object (#N) is
// not the function's. A map without slots is returned as it is.
func DeclaredNames(locals map[string]value.Value) map[string]value.Value {
	slotted := false
	for name := range locals {
		if strings.ContainsAny(name, "@#") {
			slotted = true
			break
		}
	}
	if !slotted {
		return locals
	}
	names := make(map[string]value.Value, len(locals))
	for name, item := range locals {
		if !strings.ContainsAny(name, "@#") {
			names[name] = item
		}
	}
	for name, item := range locals {
		if strings.IndexByte(name, SlotBlockMark) < 0 && strings.IndexByte(name, SlotFunctionMark) >= 0 {
			names[SlotName(name)] = item
		}
	}
	return names
}
