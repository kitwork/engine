package core

import (
	"errors"
	"fmt"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func (e *Engine) Build(source string, tenantID string, domain string, sourcePath string) (*work.Work, error) {
	l := compiler.NewLexer(source)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, errors.New(p.Errors()[0])
	}

	w := work.New("temp", tenantID, domain, sourcePath)

	env := compiler.NewEnclosedEnvironment(e.stdlib)
	env.Set("kitwork", value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			if args[0].K == value.String {
				name = args[0].Text()
			} else {
				// Object config mode (e.g. kitwork({ debug: true }))
				name = "kitwork_app"
			}
		}

		e.RegistryMu.Lock()
		defer e.RegistryMu.Unlock()

		if existing, ok := e.Registry[name]; ok {
			// Ensure it has access to register items backward
			existing.EngineRegistry = e
			return value.New(existing)
		}

		newWork := work.New(name, tenantID, domain, sourcePath)
		newWork.EngineRegistry = e
		e.Registry[name] = newWork

		return value.New(newWork)
	}))

	c := e.compilerPool.Get().(*compiler.Compiler)
	c.Reset()
	if err := c.Compile(prog); err == nil {
		w.SetBytecode(c.ByteCodeResult())
		if e.Config.Debug {
			fmt.Printf("[Build] Assigned bytecode to Work (bytecode length: %d)\n", len(w.GetBytecode().Instructions))
		}
	} else {
		fmt.Printf("[Build] Compile error for Work: %v\n", err)
	}

	compiler.Evaluator(prog, env) // Now can read addresses from AST

	e.compilerPool.Put(c)
	e.cache.Add(source, w)
	return w, nil
}
