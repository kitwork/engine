package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

type Result struct {
	Value    value.Value
	Response value.Value
	ResType  string
	Error    string
	Energy   uint64
}

type GlobalConfig struct {
	Port   int
	Source string
	Debug  bool
}

type ExecutionContext struct {
	machine    *runtime.Runtime
	task       *work.Task
	jsonFn     value.Value
	htmlFn     value.Value
	nowFn      value.Value
	dbFn       value.Value
	payloadFn  value.Value
	logFn      value.Value
	httpFn     value.Value
	parallelFn value.Value
	engineFn   value.Value
	workFn     value.Value
}

type Engine struct {
	stdlib       *compiler.Environment
	stdlibStore  map[string]value.Value
	cache        *lru.Cache[string, *work.Work]
	Registry     map[string]*work.Work // Exposed for Sync
	compilerPool sync.Pool
	ctxPool      sync.Pool
	Config       GlobalConfig
}

func New() *Engine {
	cache, _ := lru.New[string, *work.Work](2048)
	stdlib := compiler.NewEnvironment()

	e := &Engine{
		stdlib:      stdlib,
		stdlibStore: stdlib.Store(),
		cache:       cache,
		Registry:    make(map[string]*work.Work),
	}

	e.compilerPool.New = func() any { return compiler.NewCompiler() }

	e.ctxPool.New = func() any {
		ctx := &ExecutionContext{
			machine: runtime.New(nil, nil),
			task:    &work.Task{},
		}

		// Map Builtins to Context
		ctx.jsonFn = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				ctx.task.JSON(args[0])
			}
			return value.New(ctx.task)
		})
		ctx.htmlFn = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) >= 1 {
				ctx.task.HTML(args[0], args[1:]...)
			}
			return value.New(ctx.task)
		})

		// Parallel implementation
		ctx.parallelFn = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.NewNull()
			}
			arg := args[0]
			if arg.IsArray() {
				arr := arg.Array()
				results := make([]value.Value, len(arr))
				var wg sync.WaitGroup
				for i, v := range arr {
					if sFn, ok := v.V.(*value.ScriptFunction); ok {
						wg.Add(1)
						go func(idx int, fn *value.ScriptFunction) {
							defer wg.Done()
							r := e.ExecuteLambda(ctx.task.Work, fn, nil)
							results[idx] = r.Value
						}(i, sFn)
					} else {
						results[i] = v
					}
				}
				wg.Wait()
				return value.New(results)
			} else if arg.IsMap() {
				m := arg.Map()
				results := make(map[string]value.Value)
				var wg sync.WaitGroup
				var mu sync.Mutex
				for k, v := range m {
					if sFn, ok := v.V.(*value.ScriptFunction); ok {
						wg.Add(1)
						go func(key string, fn *value.ScriptFunction) {
							defer wg.Done()
							r := e.ExecuteLambda(ctx.task.Work, fn, nil)
							mu.Lock()
							results[key] = r.Value
							mu.Unlock()
						}(k, sFn)
					} else {
						results[k] = v
					}
				}
				wg.Wait()
				return value.New(results)
			}
			return value.NewNull()
		})

		// engine object for chaining
		var runtimeObj map[string]value.Value
		runtimeObj = map[string]value.Value{
			"source": value.NewFunc(func(args ...value.Value) value.Value {
				if len(args) > 0 {
					e.Config.Source = args[0].Text()
				}
				return value.New(runtimeObj)
			}),
			"debug": value.NewFunc(func(args ...value.Value) value.Value {
				if len(args) > 0 {
					e.Config.Debug = args[0].IsTrue()
				}
				return value.New(runtimeObj)
			}),
		}

		ctx.engineFn = value.New(map[string]value.Value{
			"run": value.NewFunc(func(args ...value.Value) value.Value {
				if len(args) > 0 {
					arg := args[0]
					if arg.IsMap() {
						cfg, _ := arg.Interface().(map[string]any)
						if p, ok := cfg["port"].(float64); ok {
							e.Config.Port = int(p)
						}
						if d, ok := cfg["debug"].(bool); ok {
							e.Config.Debug = d
						}
						if s, ok := cfg["source"].(string); ok {
							e.Config.Source = s
						}
					} else if arg.IsNumeric() {
						e.Config.Port = int(arg.N)
					}
				}
				return value.New(runtimeObj)
			}),
		})

		ctx.nowFn = value.NewFunc(func(args ...value.Value) value.Value { return ctx.task.Now() })
		ctx.dbFn = value.NewFunc(func(args ...value.Value) value.Value {
			db := ctx.task.DB()
			db.SetExecutor(ctx.machine)
			return value.New(db)
		})
		ctx.payloadFn = value.NewFunc(func(args ...value.Value) value.Value { return ctx.task.Payload() })
		ctx.logFn = value.NewFunc(func(args ...value.Value) value.Value {
			ctx.task.Log(args...)
			return value.NewNull()
		})
		ctx.httpFn = value.NewFunc(func(args ...value.Value) value.Value { return value.New(ctx.task.HTTP()) })

		ctx.workFn = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.NewNull()
			}
			name := args[0].Text()
			fmt.Printf("[Trigger work()] Called for: %s\n", name)
			if w, ok := e.Registry[name]; ok {
				return value.New(w)
			}
			w := work.NewWork(name)
			e.Registry[name] = w
			return value.New(w)
		})

		return ctx
	}

	e.registerBuiltins()
	return e
}

func (e *Engine) RegisterWork(w *work.Work) {
	e.Registry[w.Name] = w
}

func (e *Engine) registerBuiltins() {
	e.stdlib.Set("work", value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			name = args[0].Text()
		}
		if w, ok := e.Registry[name]; ok {
			return value.New(w)
		}
		w := work.NewWork(name)
		e.Registry[name] = w
		return value.New(w)
	}))

	// Inject global engine object
	e.stdlib.Set("engine", value.NewNull()) // Dummy for now, populated in EC
}

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
		fmt.Printf("[Build] Assigned bytecode to Work: %s (bytecode length: %d)\n", w.Name, len(w.Bytecode.Instructions))
	} else {
		fmt.Printf("[Build] Compile error for Work: %s - %v\n", w.Name, err)
	}
	e.compilerPool.Put(c)
	e.cache.Add(source, w)
	return w, nil
}

func (e *Engine) Trigger(ctx context.Context, w *work.Work, params ...map[string]value.Value) Result {
	if w == nil {
		fmt.Printf("[Trigger] Work is nil\n")
		return Result{}
	}
	if w.Bytecode == nil {
		fmt.Printf("[Trigger] %s: Bytecode is nil, skipping execution\n", w.Name)
		return Result{}
	}
	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	if len(params) > 0 && params[0] != nil {
		for k, v := range params[0] {
			ec.task.Params[k] = v
		}
	}

	ec.machine.FastReset(w.Bytecode.Instructions, w.Bytecode.Constants, e.stdlibStore)

	// Inject functions
	// Inject reused functions
	ec.machine.Vars["work"] = ec.workFn
	ec.machine.Vars["engine"] = ec.engineFn
	ec.machine.Vars["json"] = ec.jsonFn
	ec.machine.Vars["now"] = ec.nowFn
	ec.machine.Vars["db"] = ec.dbFn
	ec.machine.Vars["payload"] = ec.payloadFn
	ec.machine.Vars["log"] = ec.logFn
	ec.machine.Vars["http"] = ec.httpFn
	ec.machine.Vars["parallel"] = ec.parallelFn

	evalRes := ec.machine.Run()

	// Update routes to global router if needed
	e.syncRoutes(w)

	return Result{Value: evalRes, Response: ec.task.Response, ResType: ec.task.ResType, Energy: ec.machine.Energy}
}

func (e *Engine) SyncRegistry() {
	for _, w := range e.Registry {
		e.syncRoutes(w)
	}
}

func (e *Engine) syncRoutes(w *work.Work) {
	if w.Routes == nil {
		return
	}
	for _, rt := range w.Routes {
		work.GlobalRouter.Mu.Lock()
		exists := false
		for i, existing := range work.GlobalRouter.Routes {
			if existing.Method == rt.Method && existing.Path == rt.Path {
				work.GlobalRouter.Routes[i].Fn = rt.Handler
				if work.GlobalRouter.Routes[i].Fn == nil {
					work.GlobalRouter.Routes[i].Fn = &value.ScriptFunction{Address: 0}
				}
				work.GlobalRouter.Routes[i].Work = w
				exists = true
				break
			}
		}
		if !exists {
			h := rt.Handler
			if h == nil {
				h = &value.ScriptFunction{Address: 0}
			}
			work.GlobalRouter.Routes = append(work.GlobalRouter.Routes, work.Route{
				Method: rt.Method, Path: rt.Path, Fn: h, Work: w,
			})
		}
		work.GlobalRouter.Mu.Unlock()
	}
}

func (e *Engine) ExecuteLambda(w *work.Work, sFn *value.ScriptFunction, params map[string]value.Value) (res Result) {
	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	for k, v := range params {
		ec.task.Params[k] = v
	}
	ec.machine.FastReset(w.Bytecode.Instructions, w.Bytecode.Constants, e.stdlibStore)

	evalRes := ec.machine.ExecuteLambda(sFn, []value.Value{value.New(params)})
	return Result{Value: evalRes, Response: ec.task.Response, ResType: ec.task.ResType, Energy: ec.machine.Energy}
}
