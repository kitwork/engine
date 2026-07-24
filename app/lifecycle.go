package app

import (
	"sync"
)

type Lifecycle struct {
	mu     sync.RWMutex
	status string
}

func NewLifecycle() *Lifecycle {
	return &Lifecycle{status: "initialized"}
}

func (l *Lifecycle) Status() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status
}

func (l *Lifecycle) Boot() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = "running"
}

func (l *Lifecycle) Shutdown() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = "stopped"
}
