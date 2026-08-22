package app

import (
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/runtime"
)

// Pool manages reusable VM instances for tenant app execution.
type Pool struct {
	pool     sync.Pool
	active   atomic.Int64
	created  atomic.Uint64
	acquired atomic.Uint64
	released atomic.Uint64
}

func NewPool() *Pool {
	pool := &Pool{}
	pool.pool.New = func() interface{} {
		pool.created.Add(1)
		return runtime.New(nil)
	}
	return pool
}

func (p *Pool) Acquire() *runtime.VM {
	vm := p.pool.Get().(*runtime.VM)
	p.acquired.Add(1)
	p.active.Add(1)
	return vm
}

func (p *Pool) Release(vm *runtime.VM) {
	if vm != nil {
		vm.ResetForPool()
		p.pool.Put(vm)
		p.released.Add(1)
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

// PoolStats is a bounded process-local snapshot. Created is cumulative because
// sync.Pool may discard idle VMs during GC without notifying the owner; it is
// intentionally not presented as the current idle capacity.
type PoolStats struct {
	Active   int64  `json:"active"`
	Created  uint64 `json:"created"`
	Acquired uint64 `json:"acquired"`
	Released uint64 `json:"released"`
}

func (p *Pool) Stats() PoolStats {
	if p == nil {
		return PoolStats{}
	}
	return PoolStats{
		Active:   p.active.Load(),
		Created:  p.created.Load(),
		Acquired: p.acquired.Load(),
		Released: p.released.Load(),
	}
}
