package core

import (
	"fmt"

	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func (e *Engine) SyncRegistry() {
	for _, w := range e.Registry {
		e.syncRoutes(w)
		e.syncSchedules(w)
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
				work.GlobalRouter.Routes[i].Redirect = rt.Redirect
				work.GlobalRouter.Routes[i].Template = rt.Template
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
				Redirect: rt.Redirect,
				Template: rt.Template,
			})
		}
		work.GlobalRouter.Mu.Unlock()
	}
}

func (e *Engine) syncSchedules(w *work.Work) {
	if w.Schedules == nil {
		return
	}
	for _, s := range w.Schedules {
		cronExpr := s.Cron
		handler := s.Handler
		workUnit := w

		_, err := e.scheduler.AddFunc(cronExpr, func() {
			if e.Config.Debug {
				fmt.Printf("⏰ [Scheduler] Executing task for %s (%s)\n", workUnit.Name, cronExpr)
			}
			e.ExecuteLambda(workUnit, handler, nil, nil)
		})

		if err != nil {
			fmt.Printf("❌ [Scheduler] Failed to register task for %s (%s): %v\n", workUnit.Name, cronExpr, err)
		} else if e.Config.Debug {
			fmt.Printf("✅ [Scheduler] Registered task for %s (%s)\n", workUnit.Name, cronExpr)
		}
	}
}
