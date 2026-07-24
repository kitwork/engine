package work

import (
	"fmt"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// *Tenant is the host implementation of the capability compute seam (alongside capabilities.Scope,
// implemented in tenant.go). A capability that must run user JS off the request path asserts
// scope.(capabilities.Runtime) to reach Run.
var _ capabilities.Runtime = (*Tenant)(nil)

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

	vm.Builtins = t.vm.Builtins
	vm.FastReset(bc.Instructions, bc.Constants, t.vm.Globals, bc.SourceMap)
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
