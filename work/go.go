package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

func (k *KitWork) Go(fn value.Value, args ...value.Value) *KitWork {
	if fn.IsCallable() && k != nil && k.tenant != nil {
		vm := enginePool.Acquire()

		tenant := k.tenant
		bc := tenant.bytecode
		var builtins []value.Value
		var globals map[string]value.Value
		var vars map[string]value.Value
		if tenant.vm != nil {
			builtins = tenant.vm.Builtins
			globals = tenant.vm.Globals
			vars = tenant.vm.Vars
		}

		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[Background Task] Panic: %v\n", r)
				}
				enginePool.Release(vm)
			}()

			vm.Builtins = builtins
			if bc != nil {
				vm.FastReset(bc.Instructions, bc.Constants, globals, bc.SourceMap)
			}
			vm.MaxEnergy = tenant.MaxEnergy

			for key, val := range vars {
				vm.Vars[key] = val
			}

			if lambda, ok := fn.V.(*value.Lambda); ok {
				vm.ExecuteLambda(lambda, args)
			} else {
				fn.Call("go", args...)
			}
		}()
	}
	return k
}
