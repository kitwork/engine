package app

import (
	"context"
	"errors"
	"sync"
)

// ErrTasksClosing is a task refused because its application is shutting down; ErrTaskLimit one refused
// because the application already runs as many as the limit it was started within allows.
var (
	ErrTasksClosing = errors.New("app is closing")
	ErrTaskLimit    = errors.New("app task limit reached")
)

// TaskGroup tracks detached work for exactly one application: it counts the work running and drains it.
// Application shutdown cancels it and waits for it. A site generation that started work of its own and
// is replaced stops that work itself (work/routine.go); the group still counts and drains it.
type TaskGroup struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running int
	closing bool
}

func newTaskGroup() *TaskGroup {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskGroup{ctx: ctx, cancel: cancel}
}

func (g *TaskGroup) Start() (context.Context, func(), bool) {
	ctx, done, err := g.StartWithin(1, 0)
	return ctx, done, err == nil
}

// StartWithin accepts count tasks at once, or none: none while the application is closing
// (ErrTasksClosing), and none when count more would run past limit (ErrTaskLimit; a limit of zero or
// less is none). Nothing waits for room: a task refused is refused now. The function it gives ends one
// task, and is called once for each of the count tasks; the context is cancelled when the application
// closes.
func (g *TaskGroup) StartWithin(count, limit int) (context.Context, func(), error) {
	if g == nil {
		return nil, nil, ErrTasksClosing
	}
	if count < 1 {
		count = 1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return nil, nil, ErrTasksClosing
	}
	if limit > 0 && g.running+count > limit {
		return nil, nil, ErrTaskLimit
	}
	g.running += count
	g.wg.Add(count)
	return g.ctx, g.done, nil
}

func (g *TaskGroup) done() {
	g.mu.Lock()
	g.running--
	g.mu.Unlock()
	g.wg.Done()
}

// Running is how many accepted tasks have not ended.
func (g *TaskGroup) Running() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
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
