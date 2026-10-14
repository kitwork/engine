package compiler

import (
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The code of block scope (14/10/2026): how the compiler reads and writes a variable once scope.go has
// given it its slot.

// variable is what a name resolved to; nil for a name nothing declares (and before resolution).
func (c *Compiler) variable(name *Identifier) *binding {
	if c.scopes == nil || name == nil {
		return nil
	}
	return c.scopes.slots[name]
}

// slotOf is the name LOAD and STORE use for a variable.
func (c *Compiler) slotOf(name *Identifier) string {
	if found := c.variable(name); found != nil && found.slot != "" {
		return found.slot
	}
	return name.Value
}

func (c *Compiler) emitName(op runtime.Opcode, name string) {
	index := c.addConstant(value.NewString(name))
	c.emitIndex(op, index)
}

// loadName reads a variable: from its slot, or from its iteration's object.
//
//	LOAD slot                       or   LOAD #N; PUSH "x#5"; GET
func (c *Compiler) loadName(name *Identifier) {
	if found := c.variable(name); found != nil && found.environment {
		c.emitName(runtime.LOAD, found.loop.environment)
		c.emitName(runtime.PUSH, found.slot)
		c.emit(runtime.GET)
		return
	}
	c.emitName(runtime.LOAD, c.slotOf(name))
}

// storeName writes the value on top of the stack to a variable and leaves it there, as STORE does. A
// variable of an iteration's object is stored in a slot of the function running the code first, then
// copied into the object:
//
//	STORE slot                      or   STORE temp; LOAD #N; PUSH "x#5"; LOAD temp; SET; POP
func (c *Compiler) storeName(name *Identifier) {
	found := c.variable(name)
	if found == nil || !found.environment {
		c.emitName(runtime.STORE, c.slotOf(name))
		return
	}
	temp := found.slot
	if foreign, ok := c.scopes.temps[name]; ok {
		temp = foreign
	}
	c.emitName(runtime.STORE, temp)
	c.emitName(runtime.LOAD, found.loop.environment)
	c.emitName(runtime.PUSH, found.slot)
	c.emitName(runtime.LOAD, temp)
	c.emit(runtime.SET)
	c.emit(runtime.POP)
}

// assignName compiles name = value, leaving the value as the expression's result.
//
//	value; STORE slot               or   LOAD #N; PUSH "x#5"; value; SET; PUSH "x#5"; GET
func (c *Compiler) assignName(name *Identifier, assigned Expression) error {
	found := c.variable(name)
	if found == nil || !found.environment {
		if err := c.Compile(assigned); err != nil {
			return err
		}
		c.emitName(runtime.STORE, c.slotOf(name))
		return nil
	}
	c.emitName(runtime.LOAD, found.loop.environment)
	c.emitName(runtime.PUSH, found.slot)
	if err := c.Compile(assigned); err != nil {
		return err
	}
	c.emit(runtime.SET)
	c.emitName(runtime.PUSH, found.slot)
	c.emit(runtime.GET)
	return nil
}

// loopEnvironment is the slot of the object a for statement makes each iteration; "" when none.
func (c *Compiler) loopEnvironment(node Statement) string {
	if c.scopes == nil {
		return ""
	}
	if loop := c.scopes.loops[node]; loop != nil {
		return loop.environment
	}
	return ""
}

// newIteration makes the object of an iteration: MAKE 0; STORE #N; POP.
func (c *Compiler) newIteration(environment string) {
	c.emit(runtime.MAKE, 0)
	c.emitName(runtime.STORE, environment)
	c.emit(runtime.POP)
}

// nextIteration makes the next iteration's object with this iteration's counter in it, before the
// update changes it, as JavaScript copies a for's let into each iteration: a closure of this iteration
// keeps the value it saw.
//
//	MAKE 0; DUP; PUSH "i#3"; LOAD #N; PUSH "i#3"; GET; SET; POP; STORE #N; POP
func (c *Compiler) nextIteration(environment, counter string) {
	c.emit(runtime.MAKE, 0)
	c.emit(runtime.DUP)
	c.emitName(runtime.PUSH, counter)
	c.emitName(runtime.LOAD, environment)
	c.emitName(runtime.PUSH, counter)
	c.emit(runtime.GET)
	c.emit(runtime.SET)
	c.emit(runtime.POP)
	c.emitName(runtime.STORE, environment)
	c.emit(runtime.POP)
}

// compileHandedClosure makes a closure that reaches variables of a loop's iteration inside a function
// whose parameters are the objects of those iterations, called at once: the closure keeps that call's
// map, so it keeps the iteration it was made in.
//
//	JUMP over; <the closure as usual>; RETURN; over: PUSH maker; LOAD #N…; CALL n
func (c *Compiler) compileHandedClosure(literal *FunctionLiteral, environments []string) error {
	jumpOver := c.emitJump(runtime.JUMP)
	start := len(c.instructions)
	if err := c.compileFunctionLiteral(literal); err != nil {
		return err
	}
	c.emit(runtime.RETURN)
	c.patchJump(jumpOver, len(c.instructions))

	name := literal.DebugName
	if name == "" {
		name = "<anonymous>"
	}
	location := c.getSourceLocation(literal.Token.Source, literal.Token.Position)
	maker := &value.Lambda{
		Address:      start,
		Name:         name,
		SourceFile:   location.File,
		SourceLine:   location.Line,
		SourceColumn: location.Column,
		Params:       append([]string(nil), environments...),
	}
	c.at(literal.Token.Source, literal.Token.Position)
	index := c.addConstant(value.New(maker))
	c.emitIndex(runtime.PUSH, index)
	for _, environment := range environments {
		c.emitName(runtime.LOAD, environment)
	}
	c.emit(runtime.CALL, byte(len(environments)))
	return nil
}
