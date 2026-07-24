// Package playground provides bytecode disassembly and formatting for the Kitwork VM playground sandbox.
package playground

import (
	"fmt"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

// OpNames maps VM Opcodes to human readable string names.
var OpNames = map[runtime.Opcode]string{
	runtime.PUSH:    "PUSH",
	runtime.POP:     "POP",
	runtime.LOAD:    "LOAD",
	runtime.STORE:   "STORE",
	runtime.GET:     "GET",
	runtime.DUP:     "DUP",
	runtime.BUILTIN: "BUILTIN",
	runtime.ADD:     "ADD",
	runtime.SUB:     "SUB",
	runtime.MUL:     "MUL",
	runtime.DIV:     "DIV",
	runtime.AND:     "AND",
	runtime.OR:      "OR",
	runtime.NOT:     "NOT",
	runtime.COMPARE: "COMPARE",
	runtime.JUMP:    "JUMP",
	runtime.TRUE:    "TRUE",
	runtime.FALSE:   "FALSE",
	runtime.ITER:    "ITER",
	runtime.HALT:    "HALT",
	runtime.YIELD:   "YIELD",
	runtime.MAKE:    "MAKE",
	runtime.SET:     "SET",
	runtime.MERGE:   "MERGE",
	runtime.CALL:    "CALL",
	runtime.INVOKE:  "INVOKE",
	runtime.LAMBDA:  "LAMBDA",
	runtime.RETURN:  "RETURN",
	runtime.DEFER:   "DEFER",
	runtime.SPAWN:   "SPAWN",
	runtime.MOD:     "MOD",
}

// FormatBytecode formats compiled bytecode instructions into human-readable disassembly lines.
func FormatBytecode(bc *compiler.Bytecode) []string {
	if bc == nil {
		return nil
	}
	var bytecodeOps []string
	instructions := bc.Instructions
	i := 0
	for i < len(instructions) {
		op := runtime.Opcode(instructions[i])
		opName, found := OpNames[op]
		if !found {
			opName = fmt.Sprintf("UNKNOWN(0x%02x)", instructions[i])
		}

		addr := i
		i++

		switch op {
		case runtime.PUSH, runtime.LOAD, runtime.STORE, runtime.JUMP, runtime.TRUE, runtime.FALSE, runtime.ITER:
			if i+1 < len(instructions) {
				idx := uint16(instructions[i])<<8 | uint16(instructions[i+1])
				bytecodeOps = append(bytecodeOps, fmt.Sprintf("%04d: %-10s %d", addr, opName, idx))
				i += 2
			} else {
				bytecodeOps = append(bytecodeOps, fmt.Sprintf("%04d: %-10s (truncated)", addr, opName))
			}
		case runtime.MAKE, runtime.COMPARE, runtime.INVOKE, runtime.BUILTIN:
			if i < len(instructions) {
				val := instructions[i]
				bytecodeOps = append(bytecodeOps, fmt.Sprintf("%04d: %-10s %d", addr, opName, val))
				i++
			} else {
				bytecodeOps = append(bytecodeOps, fmt.Sprintf("%04d: %-10s (truncated)", addr, opName))
			}
		default:
			bytecodeOps = append(bytecodeOps, fmt.Sprintf("%04d: %s", addr, opName))
		}
	}
	return bytecodeOps
}

// FormatConstants formats the constant pool table into human-readable strings.
func FormatConstants(bc *compiler.Bytecode) []string {
	if bc == nil {
		return nil
	}
	var constsList []string
	for idx, val := range bc.Constants {
		constsList = append(constsList, fmt.Sprintf("[%d] %s (%s)", idx, val.Text(), val.K.String()))
	}
	return constsList
}
