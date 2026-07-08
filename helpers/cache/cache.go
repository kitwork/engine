// Package cache is an in-memory, TTL'd, size-capped RESPONSE cache. It is pure: it knows nothing
// about HTTP, tenants or the router — it stores opaque response bytes by key. The router wires it
// in (extracts the key from a request, replays the entry). Swap it for a Redis-backed store later
// without touching the router.
package cache

import (
	"sync"
	"time"
)

// Entry is a cached response: the raw body, its content type, and the status to replay.
type Entry struct {
	Body        []byte
	ContentType string
	Status      int
	expireAt    time.Time
}

// Store is a concurrent, size-capped map of key → Entry with lazy expiry.
type Store struct {
	mu  sync.RWMutex
	m   map[string]Entry
	max int
}

// NewStore returns a store holding at most max entries (default 1000).
func NewStore(max int) *Store {
	if max <= 0 {
		max = 1000
	}
	return &Store{m: make(map[string]Entry), max: max}
}

// Get returns the entry for key if present and unexpired.
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return Entry{}, false
	}
	if !e.expireAt.IsZero() && time.Now().After(e.expireAt) {
		s.mu.Lock()
		delete(s.m, key)
		s.mu.Unlock()
		return Entry{}, false
	}
	return e, true
}

// Set stores e under key with the given TTL (0 = no expiry). Evicts one entry when at capacity.
func (s *Store) Set(key string, e Entry, ttl time.Duration) {
	if ttl > 0 {
		e.expireAt = time.Now().Add(ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.m[key]; !exists && len(s.m) >= s.max {
		for k := range s.m { // simple FIFO-ish eviction — cheap, bounds memory
			delete(s.m, k)
			break
		}
	}
	s.m[key] = e
}
