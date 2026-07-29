// Package playground provides bytecode disassembly and formatting for the Kitwork VM playground sandbox.
package playground

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

// OpNames is derived from the VM instruction contract for compatibility with
// callers that use the playground package directly.
var OpNames = func() map[runtime.Opcode]string {
	names := make(map[runtime.Opcode]string)
	for raw := 0; raw < 256; raw++ {
		op := runtime.Opcode(raw)
		if spec, ok := runtime.LookupInstruction(op); ok {
			names[op] = spec.Name
		}
	}
	return names
}()

// FormatBytecode formats compiled bytecode instructions into human-readable disassembly lines.
func FormatBytecode(bc *compiler.Bytecode) []string {
	if bc == nil || bc.Program == nil {
		return nil
	}
	var bytecodeOps []string
	instructions := bc.Instructions()
	i := 0
	for i < len(instructions) {
		addr := i
		op := runtime.Opcode(instructions[i])
		spec, found := runtime.LookupInstruction(op)
		if !found {
			bytecodeOps = append(bytecodeOps,
				fmt.Sprintf("%04d: UNKNOWN(0x%02x)", addr, instructions[i]))
			i++
			continue
		}

		i++
		operands, size, err := runtime.DecodeOperands(spec, instructions[i:])
		if err != nil {
			bytecodeOps = append(bytecodeOps,
				fmt.Sprintf("%04d: %-10s (truncated)", addr, spec.Name))
			break
		}
		i += size

		if len(operands) == 0 {
			bytecodeOps = append(bytecodeOps, fmt.Sprintf("%04d: %s", addr, spec.Name))
			continue
		}
		values := make([]string, len(operands))
		for j, operand := range operands {
			values[j] = fmt.Sprint(operand)
		}
		bytecodeOps = append(bytecodeOps,
			fmt.Sprintf("%04d: %-10s %s", addr, spec.Name, strings.Join(values, " ")))
	}
	return bytecodeOps
}

// FormatConstants formats the constant pool table into human-readable strings.
func FormatConstants(bc *compiler.Bytecode) []string {
	if bc == nil || bc.Program == nil {
		return nil
	}
	var constsList []string
	for idx, val := range bc.Constants() {
		constsList = append(constsList, fmt.Sprintf("[%d] %s (%s)", idx, val.Text(), val.K.String()))
	}
	return constsList
}
