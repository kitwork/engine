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

	w := work.New("temp")
	w.Entity = tenantID
	w.Domain = domain
	w.SourcePath = sourcePath

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

		if _, ok := e.Registry[name]; ok {
			// We return our DSL object
			return value.New(createAppObj(e, tenantID, domain, sourcePath))
		}

		w.Name = name
		w.Entity = tenantID
		w.Domain = domain
		w.SourcePath = sourcePath

		e.RegistryMu.Lock()
		e.Registry[name] = w
		e.RegistryMu.Unlock()

		return value.New(createAppObj(e, tenantID, domain, sourcePath))
	}))

	c := e.compilerPool.Get().(*compiler.Compiler)
	c.Reset()
	if err := c.Compile(prog); err == nil {
		w.SetBytecode(c.ByteCodeResult())
		if e.Config.Debug {
			fmt.Printf("[Build] Assigned bytecode to Work: %s (bytecode length: %d)\n", w.Name, len(w.GetBytecode().Instructions))
		}
	} else {
		fmt.Printf("[Build] Compile error for Work: %s - %v\n", w.Name, err)
	}

	compiler.Evaluator(prog, env) // Now can read addresses from AST

	e.compilerPool.Put(c)
	e.cache.Add(source, w)
	return w, nil
}
