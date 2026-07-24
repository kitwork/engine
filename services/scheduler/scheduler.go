package scheduler

import (
	"sync"
	"time"

	"github.com/kitwork/engine/capabilities"
)

type Job struct {
	ID         string
	Name       string
	Schedule   string
	NextRun    time.Time
	LastRun    time.Time
	Status     string
	RunCount   int64
	ErrorCount int64
}

type Manager struct {
	mu      sync.Mutex
	scope   capabilities.Scope
	jobs    map[string]*Job
	cancels map[string]chan struct{}
}

func NewManager(scope capabilities.Scope) *Manager {
	return &Manager{
		scope:   scope,
		jobs:    make(map[string]*Job),
		cancels: make(map[string]chan struct{}),
	}
}

func (m *Manager) Schedule(name, expression string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	job := &Job{
		ID:       name,
		Name:     name,
		Schedule: expression,
		Status:   "scheduled",
	}
	m.jobs[name] = job
	return job
}

func (m *Manager) List() []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j)
	}
	return list
}

func (m *Manager) Cancel(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch, exists := m.cancels[name]
	if !exists {
		return false
	}
	close(ch)
	delete(m.cancels, name)
	if j, ok := m.jobs[name]; ok {
		j.Status = "cancelled"
	}
	return true
}
