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
	reqCtx     *work.Request
	jsonFn     value.Value
	htmlFn     value.Value
	nowFn      value.Value
	dbFn       value.Value
	payloadFn  value.Value
	logFn      value.Value
	fetchFn    value.Value
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
	argsBuffer []value.Value // Reusable buffer for lambda args (Zero-Alloc optimization)
}

func (e *Engine) Trigger(ctx context.Context, w *work.Work, req *http.Request, writer http.ResponseWriter, params ...map[string]value.Value) Result {
	if w == nil {
		if e.Config.Debug {
			fmt.Printf("[Trigger] Work is nil\n")
		}
		return Result{}
	}
	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	ec.reqCtx.Reset(req, writer)

	if w.GetBytecode() == nil && w.GetDoneFunc() == nil {
		if e.Config.Debug {
			fmt.Printf("[Trigger] %s: No execution logic found (bytecode and native func are nil)\n", w.Name)
		}
		return Result{Response: ec.task.Response}
	}

	if len(params) > 0 && params[0] != nil {
		ec.reqCtx.Query = value.New(params[0])
	}
	if len(params) > 1 && params[1] != nil {
		ec.reqCtx.Body = value.New(params[1])
	}
	if len(params) > 2 && params[2] != nil {
		for k, v := range params[2] {
			ec.reqCtx.Params[k] = v
		}
	}

	var evalRes value.Value
	if w.GetBytecode() != nil {
		ec.machine.FastReset(w.GetBytecode().Instructions, w.GetBytecode().Constants, e.stdlibStore)
		evalRes = ec.machine.Run()
	}

	if w.GetDoneFunc() != nil {
		// Native Execution
		args := []value.Value{ec.reqCtx.Payload()}
		res := w.GetDoneFunc()(args...)
		ec.task.Response.Data = res
		evalRes = res
	}

	// 2. Lifecycle hooks
	if ec.task.Error != "" {
		if w.GetFail() != nil {
			ec.machine.ExecuteLambda(w.GetFail(), []value.Value{value.NewString(ec.task.Error)})
		}
	} else {
		if w.GetDone() != nil {
			ec.machine.ExecuteLambda(w.GetDone(), []value.Value{ec.task.Response.Data})
		}
	}

	// Update routes to global router if needed
	e.syncRoutes(w)

	return Result{Value: evalRes, Response: ec.task.Response, Error: ec.task.Error, Energy: ec.machine.Energy}
}

func (e *Engine) ExecuteLambda(w *work.Work, sFn *value.Script, req *http.Request, writer http.ResponseWriter, params ...map[string]value.Value) (res Result) {
	ec := e.ctxPool.Get().(*ExecutionContext)
	defer e.ctxPool.Put(ec)

	ec.task.Reset(w)
	ec.reqCtx.Reset(req, writer)

	if len(params) > 0 && params[0] != nil {
		ec.reqCtx.Query = value.New(params[0])
	}
	if len(params) > 1 && params[1] != nil {
		ec.reqCtx.Body = value.New(params[1])
	}
	if len(params) > 2 && params[2] != nil {
		for k, v := range params[2] {
			ec.reqCtx.Params[k] = v
		}
	}

	ec.machine.FastReset(w.GetBytecode().Instructions, w.GetBytecode().Constants, e.stdlibStore)

	// Combine all for the single 'params' argument usually passed to lambdas (req object)
	combined := ec.reqCtx.Payload()

	// OPTIMIZATION: Reuse args buffer (Zero-Alloc Slice)
	ec.argsBuffer = ec.argsBuffer[:0]
	ec.argsBuffer = append(ec.argsBuffer, combined)

	evalRes := ec.machine.ExecuteLambda(sFn, ec.argsBuffer)

	// Lifecycle hooks
	if ec.task.Error != "" {
		if w.GetFail() != nil {
			ec.machine.ExecuteLambda(w.GetFail(), []value.Value{value.NewString(ec.task.Error)})
		}
	} else {
		if w.GetDone() != nil && w.GetDone() != sFn {
			ec.machine.ExecuteLambda(w.GetDone(), []value.Value{ec.task.Response.Data})
		}
	}

	return Result{Value: evalRes, Response: ec.task.Response, Error: ec.task.Error, Energy: ec.machine.Energy}
}
