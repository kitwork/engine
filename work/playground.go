package work

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func (w *KitWork) Playground() *Playground { return &Playground{tenant: w.tenant} }

type Playground struct {
	tenant *Tenant
}

func (p *Playground) Compile(codeVal value.Value) value.Value {
	code := codeVal.Text()

	// 1. Lex and Parse
	l := compiler.NewLexer(code)
	prg := compiler.NewParser(l)
	prog := prg.ParseProgram()
	if len(prg.Errors()) > 0 {
		return value.New(map[string]value.Value{
			"error":     value.NewString(fmt.Sprintf("Parse error: %s", prg.Errors()[0])),
			"bytecode":  value.NewString(""),
			"constants": value.NewString(""),
			"gas":       value.New(float64(0)),
		})
	}

	// 2. Compile
	c := compiler.NewCompiler(code)
	if err := c.Compile(prog); err != nil {
		return value.New(map[string]value.Value{
			"error":     value.NewString(fmt.Sprintf("Compile error: %s", err)),
			"bytecode":  value.NewString(""),
			"constants": value.NewString(""),
			"gas":       value.New(float64(0)),
		})
	}
	bc := c.ByteCodeResult()

	// Opcode to string mapping
	opNames := map[runtime.Opcode]string{
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

	// Format bytecode instructions
	var bytecodeOps []string
	instructions := bc.Instructions
	i := 0
	for i < len(instructions) {
		op := runtime.Opcode(instructions[i])
		opName, found := opNames[op]
		if !found {
			opName = fmt.Sprintf("UNKNOWN(0x%02x)", instructions[i])
		}

		addr := i
		i++

		// Decode operands
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

	// Format constants
	var constsList []string
	for idx, val := range bc.Constants {
		constsList = append(constsList, fmt.Sprintf("[%d] %s (%s)", idx, val.Text(), val.K.String()))
	}

	// 3. Test Run for Gas estimation
	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.MaxEnergy = 50000 // reasonable limit for small snippet testing

	// Inject standard globals
	vm.Globals = make(map[string]value.Value)
	// Add mock kitwork built-in
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.NewNull()
	})
	vm.Builtins = []value.Value{kitworkFunc}
	vm.Globals["kitwork"] = kitworkFunc

	// Run VM
	var retVal value.Value
	func() {
		defer func() {
			if r := recover(); r != nil {
				retVal = value.Value{K: value.Invalid, V: fmt.Sprintf("runtime panic: %v", r)}
			}
		}()
		retVal = vm.Run()
	}()

	gasUsed := vm.Energy
	errStr := ""
	if retVal.K == value.Invalid {
		errStr = retVal.Text()
	}

	res := map[string]value.Value{
		"bytecode":  value.NewString(strings.Join(bytecodeOps, "\n")),
		"constants": value.NewString(strings.Join(constsList, "\n")),
		"gas":       value.New(float64(gasUsed)),
		"error":     value.NewString(errStr),
	}
	return value.New(res)
}
