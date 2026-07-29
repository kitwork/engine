package app

import (
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/runtime"
)

// Pool manages reusable VM instances for tenant app execution.
type Pool struct {
	pool   sync.Pool
	active atomic.Int64
}

func NewPool() *Pool {
	return &Pool{
		pool: sync.Pool{
			New: func() interface{} {
				return runtime.New(nil)
			},
		},
	}
}

func (p *Pool) Acquire() *runtime.VM {
	vm := p.pool.Get().(*runtime.VM)
	p.active.Add(1)
	return vm
}

func (p *Pool) Release(vm *runtime.VM) {
	if vm != nil {
		vm.ResetForPool()
		p.pool.Put(vm)
		p.active.Add(-1)
	}
}

// Active reports how many VMs are currently checked out. It is intended for
// runtime health metrics and lifecycle regression tests.
func (p *Pool) Active() int64 {
	if p == nil {
		return 0
	}
	return p.active.Load()
}
