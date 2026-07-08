// Package ratelimit is a pure, per-key fixed-window rate limiter. It knows nothing about HTTP —
// the router extracts the key (per IP / user / …) and calls Allow. Swap the window strategy or a
// distributed backend later without touching the router.
package ratelimit

import (
	"sync"
	"time"
)

type window struct {
	count int
	reset time.Time
}

// Limiter tracks a fixed window per key. Safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

func New() *Limiter { return &Limiter{windows: make(map[string]*window)} }

// Allow reports whether key may proceed: at most rate hits per `per` window. The first call in a
// window opens it; subsequent calls consume it until it resets.
func (l *Limiter) Allow(key string, rate int, per time.Duration) bool {
	if rate <= 0 || per <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w, ok := l.windows[key]
	if !ok || now.After(w.reset) {
		l.windows[key] = &window{count: 1, reset: now.Add(per)}
		return true
	}
	if w.count >= rate {
		return false
	}
	w.count++
	return true
}
