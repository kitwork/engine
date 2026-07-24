package work

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/compiler"
	pg "github.com/kitwork/engine/utilities/playground"
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

	bytecodeOps := pg.FormatBytecode(bc)
	constsList := pg.FormatConstants(bc)

	// 3. Test Run for Gas estimation
	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.MaxEnergy = 50000 // reasonable limit for small snippet testing

	// Inject standard globals
	vm.Globals = make(map[string]value.Value)
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.NewNull()
	})
	vm.Builtins = []value.Value{kitworkFunc}
	vm.Globals["kitwork"] = kitworkFunc

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

	return value.New(map[string]value.Value{
		"error":     value.NewString(errStr),
		"bytecode":  value.NewString(strings.Join(bytecodeOps, "\n")),
		"constants": value.NewString(strings.Join(constsList, "\n")),
		"gas":       value.New(float64(gasUsed)),
	})
}
