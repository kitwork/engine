package work

import (
	"database/sql"
	"fmt"
	"sync"
)

const cronResourceName = "scheduler"

// cronRuntime is the concrete scheduler resource owned by app.Runtime. It is
// implemented in work because callbacks execute Kitwork bytecode and need the
// Tenant compatibility adapter.
type cronRuntime struct {
	owner *Tenant

	mu      sync.Mutex
	jobs    []*CronJob
	cancels []chan struct{}
	wg      sync.WaitGroup
	db      *sql.DB
	store   cronStore
	byName  map[string]*CronJob
	node    string
	closed  bool
}

func (s *cronRuntime) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return
	}
	s.closed = true
	for _, cancel := range s.cancels {
		close(cancel)
	}
	s.cancels = nil
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *cronRuntime) startRun() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

func (t *Tenant) scheduler() *cronRuntime {
	if t == nil || t.appRuntime == nil {
		return nil
	}
	current, _ := t.appRuntime.Resource(cronResourceName).(*cronRuntime)
	return current
}

func (t *Tenant) ensureScheduler() (*cronRuntime, error) {
	if t == nil || t.appRuntime == nil {
		return nil, fmt.Errorf("app runtime is unavailable")
	}
	if current := t.scheduler(); current != nil {
		return current, nil
	}
	candidate := &cronRuntime{owner: t}
	resource, installed, err := t.appRuntime.InstallResource(cronResourceName, candidate)
	if err != nil {
		return nil, err
	}
	if !installed {
		current, ok := resource.(*cronRuntime)
		if !ok {
			return nil, fmt.Errorf("app scheduler resource has unexpected type %T", resource)
		}
		return current, nil
	}
	return candidate, nil
}
