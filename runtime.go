package engine

import (
	"context"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/vm"
)

var (
	envPool = sync.Pool{
		New: func() any {
			return compiler.NewEnvironment()
		},
	}
	vmPool = sync.Pool{
		New: func() any {
			// Pre-allocate stack to avoid future allocs
			return vm.NewVM(nil, nil)
		},
	}
)

type Runtime struct {
	ctx   context.Context
	env   *compiler.Environment
	work  *Work // Runtime luôn gắn liền với một Work Blueprint
	start time.Time
}

func NewRuntime(ctx context.Context, work *Work, global *compiler.Environment) *Runtime {
	// Sử dụng pool để lấy environment sạch
	env := envPool.Get().(*compiler.Environment)
	env.Reset()
	env.SetOuter(global)

	// DX: Tự động bind các phương thức của 'work' ra phạm vi toàn cục (Context Alias)
	if work != nil {
		wVal := value.New(work)

		// Alias: json(data) -> work.json(data)
		env.Set("json", value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				work.JSON(args[0])
			}
			return wVal
		}))

		// Alias: now() -> work.now()
		env.Set("now", value.NewFunc(func(args ...value.Value) value.Value {
			return work.Now()
		}))

		// Alias: db() -> work.db()
		env.Set("db", value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(work.DB())
		}))

		// Chia sẻ luôn đối tượng 'w' và 'context' vào script nếu cần
		env.Set("w", wVal)
		env.Set("context", wVal)
	}

	return &Runtime{
		ctx:   ctx,
		env:   env,
		work:  work,
		start: time.Now(),
	}
}

func (rt *Runtime) Execute(nodes ...compiler.Node) value.Value {
	// Cuối hàm, trả Environment về pool
	defer func() {
		rt.env.Reset()
		envPool.Put(rt.env)
	}()

	// Ưu tiên sử dụng Bytecode VM
	if len(nodes) == 0 && rt.work != nil && rt.work.Bytecode != nil {
		machine := vmPool.Get().(*vm.VM)
		machine.Reset(rt.work.Bytecode.Instructions, rt.work.Bytecode.Constants)

		for k, v := range rt.env.All() {
			machine.Vars[k] = v
		}

		res := machine.Run()
		vmPool.Put(machine) // Trả về pool để tái sử dụng stack
		return res
	}

	// Cơ chế Fallback hoặc dùng cho giai đoạn Build/Discovery: Chạy trực tiếp AST
	var targetNode compiler.Node

	if len(nodes) > 0 {
		targetNode = nodes[0]
	} else if rt.work != nil {
		targetNode = rt.work.EntryBlock.(compiler.Node)
	}

	if targetNode == nil {
		return value.Value{K: value.Invalid}
	}

	return compiler.Evaluator(targetNode, rt.env)
}
