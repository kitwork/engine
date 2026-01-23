package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/vm"
	"github.com/kitwork/engine/work"
)

// Result chứa kết quả thực thi và metadata phản hồi
type Result struct {
	Value    value.Value
	Response value.Value
	ResType  string
	Error    string // Added for recovery
}

type ExecutionContext struct {
	machine   *vm.VM
	task      *work.Task
	jsonFn    value.Value
	htmlFn    value.Value
	nowFn     value.Value
	dbFn      value.Value
	payloadFn value.Value
	logFn     value.Value
	httpFn    value.Value
}

type Engine struct {
	stdlib       *compiler.Environment
	stdlibStore  map[string]value.Value
	cache        *lru.Cache[string, *work.Work]
	compilerPool sync.Pool
	ctxPool      sync.Pool
}

func New() *Engine {
	cache, _ := lru.New[string, *work.Work](2048)
	stdlib := compiler.NewEnvironment()

	e := &Engine{
		stdlib:      stdlib,
		stdlibStore: stdlib.Store(),
		cache:       cache,
	}

	e.compilerPool.New = func() any { return compiler.NewCompiler() }

	e.ctxPool.New = func() any {
		ctx := &ExecutionContext{
			machine: vm.NewVM(nil, nil),
			task:    &work.Task{},
		}
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

		return ctx
	}

	e.registerBuiltins()
	return e
}

func (e *Engine) registerBuiltins() {
	e.stdlib.Set("work", value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			if args[0].IsString() {
				name = args[0].Text()
			}
		}
		return value.New(work.NewWork(name))
	}))
	e.stdlib.Set("router", value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(work.GlobalRouter)
	}))
}

func (e *Engine) Build(source string) (*work.Work, error) {
	lexer := compiler.NewLexer(source)
	parser := compiler.NewParser(lexer)
	program := parser.ParseProgram()
	if len(parser.Errors()) > 0 {
		return nil, errors.New(parser.Errors()[0])
	}

	w := work.NewWork("temp")
	env := compiler.NewEnclosedEnvironment(e.stdlib)
	env.Set("work", value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 && args[0].IsString() {
			w.Name = args[0].Text()
		}
		return value.New(w)
	}))
	compiler.Evaluator(program, env)

	c := e.compilerPool.Get().(*compiler.Compiler)
	c.Reset()
	if err := c.Compile(program); err == nil {
		w.Bytecode = c.ByteCodeResult()
	}
	e.compilerPool.Put(c)
	e.cache.Add(source, w)
	return w, nil
}

func (e *Engine) Trigger(ctx context.Context, w *work.Work, params ...map[string]value.Value) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 RECOVERED from panic: %v\n%s\n", r, debug.Stack())
			res.Error = fmt.Sprintf("%v", r)
		}
	}()

	if w == nil || w.Bytecode == nil {
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

	vTask := value.New(ec.task)
	ec.machine.Vars["w"] = vTask
	ec.machine.Vars["context"] = vTask
	ec.machine.Vars["json"] = ec.jsonFn
	ec.machine.Vars["html"] = ec.htmlFn
	ec.machine.Vars["now"] = ec.nowFn
	ec.machine.Vars["db"] = ec.dbFn
	ec.machine.Vars["payload"] = ec.payloadFn
	ec.machine.Vars["log"] = ec.logFn
	ec.machine.Vars["http"] = ec.httpFn

	evalRes := ec.machine.Run()

	// 1. Shorthand API Registration: Nếu script gọi w.router(), ta đăng ký vào GlobalRouter
	if w.Routes != nil {
		for _, rt := range w.Routes {
			work.GlobalRouter.Mu.Lock()
			// Cần bọc script thành Lambda để Router có thể gọi lại
			sFn := &value.ScriptFunction{
				Address: 0, // Bắt đầu từ đầu script
				// Note: Trong thực tế ta cần bytecode snapshot,
				// nhưng ở đây ta chỉ cần trỏ về main entry.
			}
			work.GlobalRouter.Routes = append(work.GlobalRouter.Routes, work.Route{
				Method: rt.Method,
				Path:   rt.Path,
				Fn:     sFn,
				Work:   w,
			})

			work.GlobalRouter.Mu.Unlock()
		}
	}

	if ec.task.Response.IsNil() || ec.task.Response.IsInvalid() {
		if !evalRes.IsNil() && !evalRes.IsInvalid() {
			if q, ok := evalRes.V.(*work.DBQuery); ok {
				ec.task.JSON(q.Get())
			} else if evalRes.IsMap() && evalRes.Get("__is_html").IsTrue() {
				ec.task.Response = evalRes
				ec.task.ResType = "html"
			} else {
				ec.task.JSON(evalRes)
			}
		}
	}

	res.Value = evalRes
	res.Response = ec.task.Response
	res.ResType = ec.task.ResType
	return res
}

func (e *Engine) ExecuteLambda(w *work.Work, sFn *value.ScriptFunction, params map[string]value.Value) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			res.Error = fmt.Sprintf("%v", r)
		}
	}()

	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	for k, v := range params {
		ec.task.Params[k] = v
	}

	ec.machine.FastReset(w.Bytecode.Instructions, w.Bytecode.Constants, e.stdlibStore)

	// Setup standard vars for lambda
	ec.machine.Vars["db"] = ec.dbFn
	ec.machine.Vars["log"] = ec.logFn

	// Create 'req' object
	reqObj := value.Value{K: value.Map, V: params}
	evalRes := ec.machine.ExecuteLambda(sFn, []value.Value{reqObj})

	// Auto-Response Logic for Lambda (Shorthand API support)
	if ec.task.Response.IsNil() || ec.task.Response.IsInvalid() {
		if !evalRes.IsNil() && !evalRes.IsInvalid() {
			if q, ok := evalRes.V.(*work.DBQuery); ok {
				ec.task.JSON(q.Get())
			} else if evalRes.IsMap() && evalRes.Get("__is_html").IsTrue() {
				ec.task.Response = evalRes
				ec.task.ResType = "html"
			} else {
				ec.task.JSON(evalRes)
			}
		}
	}

	res.Value = evalRes
	res.Response = ec.task.Response
	res.ResType = ec.task.ResType
	return res

}
