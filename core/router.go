package core

import (
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func (e *Engine) SyncRegistry() {
	for _, w := range e.Registry {
		e.syncRoutes(w)
	}
}

func (e *Engine) syncRoutes(w *work.Work) {
	if w.Routes == nil {
		return
	}
	for _, rt := range w.Routes {
		work.GlobalRouter.Mu.Lock()
		exists := false
		for i, existing := range work.GlobalRouter.Routes {
			if existing.Method == rt.Method && existing.Path == rt.Path {
				work.GlobalRouter.Routes[i].Fn = rt.Handler
				if work.GlobalRouter.Routes[i].Fn == nil {
					work.GlobalRouter.Routes[i].Fn = &value.ScriptFunction{Address: 0}
				}
				work.GlobalRouter.Routes[i].Work = w
				exists = true
				break
			}
		}
		if !exists {
			h := rt.Handler
			if h == nil {
				h = &value.ScriptFunction{Address: 0}
			}
			work.GlobalRouter.Routes = append(work.GlobalRouter.Routes, work.Route{
				Method: rt.Method, Path: rt.Path, Fn: h, Work: w,
			})
		}
		work.GlobalRouter.Mu.Unlock()
	}
}
