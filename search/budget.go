package search

import (
	"context"
	"fmt"
	"sync"
)

// byteBudget is a context-aware weighted semaphore. A closed-and-replaced
// notification channel avoids one goroutine per waiter.
type byteBudget struct {
	limit   int64
	mu      sync.Mutex
	used    int64
	changed chan struct{}
}

func newByteBudget(limit int64) *byteBudget {
	return &byteBudget{limit: limit, changed: make(chan struct{})}
}

func (budget *byteBudget) acquire(ctx context.Context, amount int64) error {
	if ctx == nil {
		return fmt.Errorf("search: byte budget context is nil")
	}
	if amount <= 0 || amount > budget.limit {
		return ErrMutationTooLarge
	}
	for {
		budget.mu.Lock()
		if budget.used <= budget.limit-amount {
			budget.used += amount
			budget.mu.Unlock()
			return nil
		}
		changed := budget.changed
		budget.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (budget *byteBudget) release(amount int64) {
	if amount <= 0 {
		return
	}
	budget.mu.Lock()
	budget.used -= amount
	if budget.used < 0 {
		budget.used = 0
	}
	close(budget.changed)
	budget.changed = make(chan struct{})
	budget.mu.Unlock()
}

func (budget *byteBudget) value() int64 {
	if budget == nil {
		return 0
	}
	budget.mu.Lock()
	used := budget.used
	budget.mu.Unlock()
	return used
}
