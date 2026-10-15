package work

import (
	"fmt"
	"time"

	"github.com/kitwork/engine/capabilities"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// *Tenant is the host implementation of the capability compute seam (alongside capabilities.Scope,
// implemented in tenant.go). A capability that must run user JS off the request path asserts
// scope.(capabilities.Runtime) to reach Run.
var _ capabilities.Runtime = (*Tenant)(nil)

type tenantLambdaExecutor struct {
	tenant       *Tenant
	requestScope *requestscope.Scope
}

func (e tenantLambdaExecutor) ExecuteLambda(fn *value.Lambda, args []value.Value) (result value.Value) {
	if e.tenant == nil || fn == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "run: nil tenant or lambda")
	}

	program, _ := runtime.ProgramFromRef(fn.Program)
	if program == nil && e.tenant.bytecode != nil {
		program = e.tenant.bytecode.Program
	}
	if program == nil {
		return value.InvalidFailure(value.InvalidArgumentCode, "run: lambda has no bytecode")
	}

	vm := (*runtime.VM)(nil)
	releaseVM := func() {}
	// The VM this one runs inside (runtime/nesting.go), whose energy, call depth and context it draws
	// on: the VM that handed fn to the host call running now; failing that, the request's VM (or the
	// one nested deepest in it). A capability imported at module level has no request scope at all,
	// so the caller is found through the lambda, not the capability.
	outer := runtime.LambdaCaller(fn)
	if outer == nil && e.requestScope != nil {
		outer = requestVM(e.requestScope)
	}
	if e.requestScope != nil {
		var err error
		vm, releaseVM, err = e.requestScope.AcquireExecutionVM(enginePool.Acquire, enginePool.Release)
		if err != nil {
			// the request's scope is closed: the request is over, which nothing catches
			return value.InvalidFailure(value.CancelledCode, err.Error())
		}
	} else {
		vm = enginePool.Acquire()
		releaseVM = func() { enginePool.Release(vm) }
	}
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			// A panic is NATIVE_PANIC, as in the request's own VM: it ends the run and safe() does not
			// catch it. Nested even when it struck before the run began, so it reaches the outer VM.
			result = runtime.NativePanic("nested execution", recovered)
			vm.NestIn(outer)
		}
		vm.Unnest(result)
		e.tenant.recordVMExecution(program, vm, result, time.Since(started))
		releaseVM()
	}()

	e.tenant.prepareExecutionVM(vm, e.tenant.vm.Globals, e.tenant.vm.Builtins, e.requestScope)
	vm.FastResetPrepared(program)
	vm.MaxEnergy = e.tenant.MaxEnergy
	vm.NestIn(outer)
	result = vm.ExecuteLambda(fn, args)
	return result
}

// requestVM is the VM the request runs on, or nil when it has none. LeaseVM returns the request's one
// lease when there is one; its acquire returns nil, so asking never takes a new lease.
func requestVM(scope *requestscope.Scope) *runtime.VM {
	vm, err := scope.LeaseVM(func() *runtime.VM { return nil }, func(*runtime.VM) {})
	if err != nil {
		return nil
	}
	return vm
}

// Execute implements capabilities.Runtime: run a lambda against its bytecode in a pooled VM, bound to
// this tenant's Globals/Builtins/Vars and capped by MaxEnergy. This is the SAME runner the cron
// scheduler uses (runInJobVM now delegates here) — exposed generically so any capability can run
// handlers off the request path through its Scope. (Tenant.Run() is the tenant boot method; this is
// named Execute to avoid that.)
func (t *Tenant) Execute(program *runtime.Program, fn *value.Lambda, args []value.Value) (gas uint64, runErr error) {
	if fn == nil {
		return 0, fmt.Errorf("run: nil lambda")
	}
	if program == nil {
		program, _ = runtime.ProgramFromRef(fn.Program)
	}
	if program == nil {
		return 0, fmt.Errorf("run: lambda has no program")
	}
	// A capability that runs fn while a VM's host call is in progress (an atomic callback) nests it
	// there, as ExecuteLambda does; a run of its own (a cron job) has no caller and its own budget.
	outer := runtime.LambdaCaller(fn)
	vm := enginePool.Acquire()
	res := value.Value{K: value.Nil}
	defer func() {
		if r := recover(); r != nil {
			// NATIVE_PANIC, as a panic in any VM's host call is; the error is its diagnostic.
			res = runtime.NativePanic("execution", r)
			runErr = fmt.Errorf("%s", res.Text())
			if diagnostic, ok := runtime.DiagnosticFrom(res); ok {
				runErr = diagnostic
			}
			vm.NestIn(outer)
		}
		vm.Unnest(res)
		enginePool.Release(vm)
	}()

	t.prepareExecutionVM(vm, t.vm.Globals, t.vm.Builtins)
	vm.FastResetPrepared(program)
	vm.MaxEnergy = t.MaxEnergy
	for k, v := range t.vm.Vars {
		vm.Vars[k] = v
	}
	vm.NestIn(outer)

	started := time.Now()
	res = vm.ExecuteLambda(fn, args)
	t.recordVMExecution(program, vm, res, time.Since(started))
	gas = vm.Energy
	if res.K == value.Invalid {
		runErr = fmt.Errorf("%v", res.V)
		if diagnostic, ok := runtime.DiagnosticFrom(res); ok {
			// the run's own diagnostic, so a caller that makes a failure of it keeps its code
			runErr = diagnostic
		}
	}
	return gas, runErr
}
