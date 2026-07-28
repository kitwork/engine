package app

import (
	"context"
	"sync"
)

// TaskGroup tracks detached work for exactly one application. Site eviction
// does not cancel accepted work; application shutdown does.
type TaskGroup struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closing bool
}

func newTaskGroup() *TaskGroup {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskGroup{ctx: ctx, cancel: cancel}
}

func (g *TaskGroup) Start() (context.Context, func(), bool) {
	if g == nil {
		return nil, nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return nil, nil, false
	}
	g.wg.Add(1)
	return g.ctx, g.wg.Done, true
}

func (g *TaskGroup) Close() {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		g.wg.Wait()
		return
	}
	g.closing = true
	cancel := g.cancel
	g.mu.Unlock()
	cancel()
	g.wg.Wait()
}

func (g *TaskGroup) Closing() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	closing := g.closing
	g.mu.Unlock()
	return closing
}
