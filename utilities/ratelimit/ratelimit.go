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

// sweepEvery is how often Allow drops the windows that have ended. A window past its reset answers as no
// window does, so dropping it changes no answer; without it a key seen once (one client address of a by-IP
// limit) would be held for the life of the site.
const sweepEvery = time.Minute

// Limiter tracks a fixed window per key. Safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*window
	// sweepAt is when Allow next drops the windows that have ended.
	sweepAt time.Time
	// now is the limiter's clock: a test's way to move it.
	now func() time.Time
}

func New() *Limiter { return &Limiter{windows: make(map[string]*window), now: time.Now} }

// Allow reports whether key may proceed: at most rate hits per `per` window. The first call in a
// window opens it; subsequent calls consume it until it resets.
func (l *Limiter) Allow(key string, rate int, per time.Duration) bool {
	if rate <= 0 || per <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !now.Before(l.sweepAt) {
		l.sweep(now)
	}
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

// sweep drops every window that has ended by now. Runs under l.mu.
func (l *Limiter) sweep(now time.Time) {
	for key, w := range l.windows {
		if now.After(w.reset) {
			delete(l.windows, key)
		}
	}
	l.sweepAt = now.Add(sweepEvery)
}
