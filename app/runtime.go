package app

import (
	"sync"

	"github.com/kitwork/engine/runtime"
)

// Pool manages reusable VM instances for tenant app execution.
type Pool struct {
	pool sync.Pool
}

func NewPool() *Pool {
	return &Pool{
		pool: sync.Pool{
			New: func() interface{} {
				return runtime.New(nil, nil)
			},
		},
	}
}

func (p *Pool) Acquire() *runtime.VM {
	return p.pool.Get().(*runtime.VM)
}

func (p *Pool) Release(vm *runtime.VM) {
	if vm != nil {
		p.pool.Put(vm)
	}
}
