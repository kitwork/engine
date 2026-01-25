package core

import (
	"errors"
	"fmt"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func (e *Engine) Build(source string) (*work.Work, error) {
	l := compiler.NewLexer(source)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, errors.New(p.Errors()[0])
	}

	w := work.NewWork("temp")
	env := compiler.NewEnclosedEnvironment(e.stdlib)
	env.Set("work", value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			name = args[0].Text()
		}
		if existing, ok := e.Registry[name]; ok {
			w = existing
			return value.New(existing)
		}
		w.Name = name
		e.Registry[name] = w
		return value.New(w)
	}))
	compiler.Evaluator(prog, env) // Creates routes with Address=0, Trigger() will update

	c := e.compilerPool.Get().(*compiler.Compiler)
	c.Reset()
	if err := c.Compile(prog); err == nil {
		w.Bytecode = c.ByteCodeResult()
		if e.Config.Debug {
			fmt.Printf("[Build] Assigned bytecode to Work: %s (bytecode length: %d)\n", w.Name, len(w.Bytecode.Instructions))
		}
	} else {
		fmt.Printf("[Build] Compile error for Work: %s - %v\n", w.Name, err)
	}
	e.compilerPool.Put(c)
	e.cache.Add(source, w)
	return w, nil
}
