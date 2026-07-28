package work

import (
	"fmt"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
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
		return value.Value{K: value.Invalid, V: "run: nil tenant or lambda"}
	}

	code := fn.Code
	constants := fn.Constants
	sourceMap := fn.SourceMap
	if code == nil && e.tenant.bytecode != nil {
		code = e.tenant.bytecode.Instructions
		constants = e.tenant.bytecode.Constants
		sourceMap = e.tenant.bytecode.SourceMap
	}
	if code == nil {
		return value.Value{K: value.Invalid, V: "run: lambda has no bytecode"}
	}

	vm := (*runtime.VM)(nil)
	releaseVM := func() {}
	if e.requestScope != nil {
		var err error
		vm, releaseVM, err = e.requestScope.AcquireExecutionVM(enginePool.Acquire, enginePool.Release)
		if err != nil {
			return value.Value{K: value.Invalid, V: err.Error()}
		}
	} else {
		vm = enginePool.Acquire()
		releaseVM = func() { enginePool.Release(vm) }
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = value.Value{K: value.Invalid, V: fmt.Sprintf("panic: %v", recovered)}
		}
		releaseVM()
	}()

	e.tenant.prepareExecutionVM(vm, e.tenant.vm.Globals, e.tenant.vm.Builtins, e.requestScope)
	vm.FastReset(code, constants, vm.Globals, sourceMap)
	vm.MaxEnergy = e.tenant.MaxEnergy
	return vm.ExecuteLambda(fn, args)
}

// Execute implements capabilities.Runtime: run a lambda against its bytecode in a pooled VM, bound to
// this tenant's Globals/Builtins/Vars and capped by MaxEnergy. This is the SAME runner the cron
// scheduler uses (runInJobVM now delegates here) — exposed generically so any capability can run
// handlers off the request path through its Scope. (Tenant.Run() is the tenant boot method; this is
// named Execute to avoid that.)
func (t *Tenant) Execute(bc *compiler.Bytecode, fn *value.Lambda, args []value.Value) (gas uint64, runErr error) {
	if bc == nil || fn == nil {
		return 0, fmt.Errorf("run: nil bytecode or lambda")
	}
	vm := enginePool.Acquire()
	defer func() {
		if r := recover(); r != nil {
			runErr = fmt.Errorf("panic: %v", r)
		}
		enginePool.Release(vm)
	}()

	t.prepareExecutionVM(vm, t.vm.Globals, t.vm.Builtins)
	vm.FastReset(bc.Instructions, bc.Constants, vm.Globals, bc.SourceMap)
	vm.MaxEnergy = t.MaxEnergy
	for k, v := range t.vm.Vars {
		vm.Vars[k] = v
	}

	res := vm.ExecuteLambda(fn, args)
	gas = vm.Energy
	if res.K == value.Invalid {
		runErr = fmt.Errorf("%v", res.V)
	}
	return gas, runErr
}
