package work

import (
	"fmt"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// Work là Blueprint (Bản thiết kế) - IMMUTABLE
type Work struct {
	Name        string
	TenantID    string // Multi-tenancy Isolation
	Ver         string
	Description string
	Retries     int
	TimeoutDur  time.Duration
	Bytecode    *compiler.Bytecode
	SourcePath  string // Absolute path to the source file

	// Modular Cores
	CoreRouter   *RouterCore
	CoreRender   *RenderCore
	CoreSchedule *ScheduleCore

	DoneHandler *value.Script
	FailHandler *value.Script
	MainHandler *value.Script
}

func (w *Work) LoadFromConfig(data map[string]any) {
	fmt.Printf("DEBUG: Loading Work Config: %+v\n", data)
	if name, ok := data["name"].(string); ok {
		w.Name = name
	}
	if desc, ok := data["description"].(string); ok {
		w.Description = desc
	}
	if ver, ok := data["version"].(string); ok {
		w.Ver = ver
	}
	if routes, ok := data["routes"].([]any); ok {
		for _, r := range routes {
			if rm, ok := r.(map[string]any); ok {
				method, _ := rm["method"].(string)
				path, _ := rm["path"].(string)
				w.CoreRouter.Routes = append(w.CoreRouter.Routes, &StaticRoute{Method: method, Path: path}) // Simplified for now
			}
		}
	}
}

func NewWork(name string) *Work {
	return &Work{
		Name:         name,
		CoreRouter:   &RouterCore{},
		CoreRender:   &RenderCore{},
		CoreSchedule: &ScheduleCore{},
	}
}

func (w *Work) Handle(fn value.Value) *Work {
	if len(w.CoreRouter.Routes) > 0 {
		lastRoute := w.CoreRouter.Routes[len(w.CoreRouter.Routes)-1]
		if sFn, ok := fn.V.(*value.Script); ok {
			fmt.Printf("[Handle] %s: Setting handler for %s %s with Address: %d (was: %v)\n",
				w.Name, lastRoute.Method, lastRoute.Path, sFn.Address, lastRoute.Handler)
			lastRoute.Handler = sFn
		} else {
			fmt.Printf("[Handle] %s: WARNING: fn.V is not *ScriptFunction, type: %T\n", w.Name, fn.V)
		}
	} else {
		// No routes? This becomes the primary handler for the work unit (used by schedules)
		if sFn, ok := fn.V.(*value.Script); ok {
			w.MainHandler = sFn
			fmt.Printf("[Handle] %s: Setting primary handler with Address: %d\n", w.Name, sFn.Address)
		}
	}
	return w
}

func (w *Work) Retry(times int, _ string) *Work {
	w.Retries = times
	return w
}

func (w *Work) Desc(d string) *Work {
	w.Description = d
	return w
}

func (w *Work) Version(v string) *Work {
	w.Ver = v
	return w
}

func (w *Work) Cache(duration any) *Work {
	dur := parseDuration(duration)
	if w.CoreRouter.LastRoute != nil {
		w.CoreRouter.LastRoute.CacheDuration = dur
	} else {
		w.CoreRender.CacheDuration = dur
	}
	return w
}

func parseDuration(val any) time.Duration {
	switch v := val.(type) {
	case string:
		d, _ := time.ParseDuration(v)
		return d
	case float64:
		return time.Duration(v) * time.Second
	case int:
		return time.Duration(v) * time.Second
	case value.Value:
		if v.IsString() {
			d, _ := time.ParseDuration(v.Text())
			return d
		} else if v.IsNumeric() {
			return time.Duration(v.Float()) * time.Second
		}
	}
	return 0
}

func (w *Work) Done(fn value.Value) *Work {
	if sFn, ok := fn.V.(*value.Script); ok {
		w.DoneHandler = sFn
	}
	return w
}

func (w *Work) Fail(fn value.Value) *Work {
	if sFn, ok := fn.V.(*value.Script); ok {
		w.FailHandler = sFn
	}
	return w
}
