package core

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

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
	goFn       value.Value
	deferFn    value.Value
	engineFn   value.Value
	workFn     value.Value
	queryFn    value.Value
	bodyFn     value.Value
	paramsFn   value.Value
	cookieFn   value.Value
	cacheFn    value.Value
	doneFn     value.Value
	failFn     value.Value
}

func (e *Engine) Trigger(ctx context.Context, w *work.Work, req *http.Request, writer http.ResponseWriter, params ...map[string]value.Value) Result {
	if w == nil {
		if e.Config.Debug {
			fmt.Printf("[Trigger] Work is nil\n")
		}
		return Result{}
	}
	if w.Bytecode == nil {
		if e.Config.Debug {
			fmt.Printf("[Trigger] %s: Bytecode is nil, skipping execution\n", w.Name)
		}
		return Result{}
	}
	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	ec.task.SetRequest(req, writer)

	if len(params) > 0 && params[0] != nil {
		ec.task.SetQuery(value.New(params[0]))
	}
	if len(params) > 1 && params[1] != nil {
		ec.task.SetBody(value.New(params[1]))
	}
	if len(params) > 2 && params[2] != nil {
		for k, v := range params[2] {
			ec.task.Params[k] = v
		}
	}

	ec.machine.FastReset(w.Bytecode.Instructions, w.Bytecode.Constants, e.stdlibStore)

	evalRes := ec.machine.Run()

	// 2. Lifecycle hooks
	if ec.task.Error != "" {
		if w.FailHandler != nil {
			ec.machine.ExecuteLambda(w.FailHandler, []value.Value{value.NewString(ec.task.Error)})
		}
	} else {
		if w.DoneHandler != nil {
			ec.machine.ExecuteLambda(w.DoneHandler, []value.Value{ec.task.Response})
		}
	}

	// Update routes to global router if needed
	e.syncRoutes(w)

	return Result{Value: evalRes, Response: ec.task.Response, ResType: ec.task.ResType, Error: ec.task.Error, Energy: ec.machine.Energy}
}

func (e *Engine) ExecuteLambda(w *work.Work, sFn *value.ScriptFunction, req *http.Request, writer http.ResponseWriter, params ...map[string]value.Value) (res Result) {
	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	ec.task.SetRequest(req, writer)

	if len(params) > 0 && params[0] != nil {
		ec.task.SetQuery(value.New(params[0]))
	}
	if len(params) > 1 && params[1] != nil {
		ec.task.SetBody(value.New(params[1]))
	}
	if len(params) > 2 && params[2] != nil {
		for k, v := range params[2] {
			ec.task.Params[k] = v
		}
	}

	ec.machine.FastReset(w.Bytecode.Instructions, w.Bytecode.Constants, e.stdlibStore)

	// Combine all for the single 'params' argument usually passed to lambdas (req object)
	combined := ec.task.Payload()
	evalRes := ec.machine.ExecuteLambda(sFn, []value.Value{combined})

	// Lifecycle hooks
	if ec.task.Error != "" {
		if w.FailHandler != nil {
			ec.machine.ExecuteLambda(w.FailHandler, []value.Value{value.NewString(ec.task.Error)})
		}
	} else {
		if w.DoneHandler != nil {
			ec.machine.ExecuteLambda(w.DoneHandler, []value.Value{ec.task.Response})
		}
	}

	return Result{Value: evalRes, Response: ec.task.Response, ResType: ec.task.ResType, Error: ec.task.Error, Energy: ec.machine.Energy}
}
