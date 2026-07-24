package work

import (
	"context"
	"fmt"

	"github.com/kitwork/engine/value"
)

func (k *KitWork) Go(fn value.Value, args ...value.Value) *KitWork {
	if fn.IsCallable() && k != nil && k.tenant != nil {
		tenant := k.tenant
		ctx, done, ok := tenant.startBackgroundTask()
		if !ok {
			return k
		}

		var code []byte
		var constants []value.Value
		var sourceMap []int32
		if lambda, ok := fn.V.(*value.Lambda); ok && lambda.Code != nil {
			code = lambda.Code
			constants = lambda.Constants
			sourceMap = lambda.SourceMap
		} else if tenant.bytecode != nil {
			code = tenant.bytecode.Instructions
			constants = tenant.bytecode.Constants
			sourceMap = tenant.bytecode.SourceMap
		}
		var builtins []value.Value
		if tenant.vm != nil && len(tenant.vm.Builtins) > 0 {
			builtins = make([]value.Value, len(tenant.vm.Builtins))
			copy(builtins, tenant.vm.Builtins)
		}

		globals := make(map[string]value.Value)
		vars := make(map[string]value.Value)
		if tenant.vm != nil {
			for key, val := range tenant.vm.Globals {
				globals[key] = val
			}
			for key, val := range tenant.vm.Vars {
				vars[key] = val
			}
		}

		backgroundFn := fn
		if lambda, ok := fn.V.(*value.Lambda); ok {
			backgroundFn = value.New(snapshotLambda(lambda))
		}
		backgroundArgs := append([]value.Value(nil), args...)
		vm := enginePool.Acquire()

		go func(ctx context.Context) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[Background Task] Panic: %v\n", r)
				}
				enginePool.Release(vm)
				done()
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			vm.Builtins = builtins
			vm.FastReset(code, constants, globals, sourceMap)
			vm.MaxEnergy = tenant.MaxEnergy

			for key, val := range vars {
				vm.Vars[key] = val
			}

			if lambda, ok := backgroundFn.V.(*value.Lambda); ok {
				vm.ExecuteLambda(lambda, backgroundArgs)
			} else {
				backgroundFn.Call("go", backgroundArgs...)
			}
		}(ctx)
	}
	return k
}

func (t *Tenant) startBackgroundTask() (context.Context, func(), bool) {
	t.backgroundMu.Lock()
	defer t.backgroundMu.Unlock()

	if t.backgroundClosing {
		return nil, nil, false
	}
	if t.backgroundCtx == nil {
		t.backgroundCtx, t.backgroundCancel = context.WithCancel(context.Background())
	}
	t.backgroundWG.Add(1)
	return t.backgroundCtx, t.backgroundWG.Done, true
}

func (t *Tenant) stopBackgroundTasks() {
	t.backgroundMu.Lock()
	t.backgroundClosing = true
	cancel := t.backgroundCancel
	t.backgroundMu.Unlock()

	if cancel != nil {
		cancel()
	}
	t.backgroundWG.Wait()
}

func snapshotLambda(src *value.Lambda) *value.Lambda {
	return snapshotLambdaWithMemo(src, make(map[*value.Lambda]*value.Lambda))
}

func snapshotLambdaWithMemo(src *value.Lambda, memo map[*value.Lambda]*value.Lambda) *value.Lambda {
	if src == nil {
		return nil
	}
	if cloned, ok := memo[src]; ok {
		return cloned
	}

	cloned := &value.Lambda{
		Address:   src.Address,
		Params:    append([]string(nil), src.Params...),
		Code:      src.Code,
		Constants: src.Constants,
		SourceMap: src.SourceMap,
	}
	memo[src] = cloned
	cloned.Scope = snapshotScope(src.Scope, memo)
	cloned.Parent = snapshotLambdaWithMemo(src.Parent, memo)
	return cloned
}

func snapshotScope(src map[string]value.Value, memo map[*value.Lambda]*value.Lambda) map[string]value.Value {
	if src == nil {
		return nil
	}
	cloned := make(map[string]value.Value, len(src))
	for key, val := range src {
		if lambda, ok := val.V.(*value.Lambda); ok {
			val.V = snapshotLambdaWithMemo(lambda, memo)
		}
		cloned[key] = val
	}
	return cloned
}
