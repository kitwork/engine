package core

import (
	"fmt"

	"github.com/kitwork/engine/work"
)

func (e *Engine) SyncRegistry() {
	e.RegistryMu.RLock()
	defer e.RegistryMu.RUnlock()

	for _, c := range e.Crons {
		for _, cronExpr := range c.Schedules {
			handler := c.GetHandle()
			if handler == nil {
				continue
			}

			// Capture variables
			cronLocal := cronExpr
			cLocal := c
			hLocal := handler

			_, err := e.scheduler.AddFunc(cronLocal, func() {
				if e.Config.Debug {
					fmt.Printf("⏰ [Scheduler] Executing task for %s (%s)\n", cLocal.Name, cronLocal)
				}
				e.ExecuteLambda(&cLocal.Work, hLocal, nil, nil)
			})
			if err != nil {
				fmt.Printf("❌ [Scheduler] Failed to register task for %s (%s): %v\n", cLocal.Name, cronLocal, err)
			} else if e.Config.Debug {
				fmt.Printf("✅ [Scheduler] Registered task for %s (%s)\n", cLocal.Name, cronLocal)
			}
		}
	}
}

func (e *Engine) syncRoutes(w *work.Work) {
}

func (e *Engine) syncSchedules(w *work.Work) {
}
