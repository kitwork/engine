package core

import (
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"github.com/robfig/cron/v3"
)

type Engine struct {
	stdlib       *compiler.Environment
	stdlibStore  map[string]value.Value
	cache        *lru.Cache[string, *work.Work]
	Registry     map[string]*work.Work // Exposed for Sync
	RegistryMu   sync.RWMutex          // Mutex for Registry
	Routers      []*work.Router        // Active HTTP routes
	Crons        []*work.Cron          // Active Cron jobs
	compilerPool sync.Pool
	ctxPool      sync.Pool
	Config       GlobalConfig
	scheduler    *cron.Cron
}

func New() *Engine {
	cache, _ := lru.New[string, *work.Work](2048)
	stdlib := compiler.NewEnvironment()

	e := &Engine{
		stdlib:      stdlib,
		stdlibStore: stdlib.Store(),
		cache:       cache,
		Registry:    make(map[string]*work.Work),
		Routers:     make([]*work.Router, 0),
		Crons:       make([]*work.Cron, 0),
		scheduler:   cron.New(cron.WithSeconds()), // Support second-level precision if needed
	}
	e.scheduler.Start()

	e.compilerPool.New = func() any { return compiler.NewCompiler() }

	e.ctxPool.New = func() any {
		return newExecutionContext(e)
	}

	e.registerBuiltins()
	return e
}

func (e *Engine) RegisterWork(w *work.Work) {
	e.Registry[w.Name] = w
}

func (e *Engine) AddRouter(r *work.Router) {
	e.RegistryMu.Lock()
	e.Routers = append(e.Routers, r)
	e.RegistryMu.Unlock()
}

func (e *Engine) AddCron(c *work.Cron) {
	e.RegistryMu.Lock()
	e.Crons = append(e.Crons, c)
	e.RegistryMu.Unlock()
}

func (e *Engine) registerBuiltins() {
	e.stdlib.Set("kitwork", value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			name = args[0].Text()
		}
		if w, ok := e.Registry[name]; ok {
			return value.New(w)
		}
		w := work.New(name, "", "", "")
		e.Registry[name] = w
		return value.New(w)
	}))

	// Inject global engine object
	e.stdlib.Set("engine", value.NewNull()) // Dummy for now, populated in EC
	e.stdlib.Set("json", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("html", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("query", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("params", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("go", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("defer", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("parallel", value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() }))
	e.stdlib.Set("routes", value.NewFunc(func(args ...value.Value) value.Value {
		// GlobalRouter was removed. For now, return empty or implement new registry search.
		return value.New([]value.Value{})
	}))
}
