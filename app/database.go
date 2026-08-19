package app

import (
	"database/sql"
	"fmt"
	"sync"
)

// DatabaseManager owns every database connection opened by one application.
// Keys are chosen by the work adapter: configured aliases are shared across
// sibling sites, while site-local SQLite keys include their absolute path.
type DatabaseManager struct {
	mu          sync.Mutex
	connections map[string]*sql.DB
	cleanups    map[string]func() // per-key teardown (e.g. releasing a single-instance file lock)
	closed      bool
}

func newDatabaseManager() *DatabaseManager {
	return &DatabaseManager{
		connections: make(map[string]*sql.DB),
		cleanups:    make(map[string]func()),
	}
}

// SetCleanup registers a teardown to run for `key` when the manager closes — used to release the OS
// file lock a local database holds. Idempotent: re-registering the same key overwrites. Safe to call
// after Open returns (it does not run under Open's lock).
func (m *DatabaseManager) SetCleanup(key string, cleanup func()) {
	if m == nil || key == "" || cleanup == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		cleanup() // already torn down — release immediately rather than leak the lock
		return
	}
	if m.cleanups == nil {
		m.cleanups = make(map[string]func())
	}
	m.cleanups[key] = cleanup
}

// Open returns an existing connection or creates exactly one while holding the
// manager lock. This deliberately serializes first connection per app.
func (m *DatabaseManager) Open(key string, connect func() (*sql.DB, error)) (*sql.DB, error) {
	if m == nil {
		return nil, fmt.Errorf("app database manager is nil")
	}
	if key == "" {
		return nil, fmt.Errorf("app database key is empty")
	}
	if connect == nil {
		return nil, fmt.Errorf("app database connector is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("app database manager is closed")
	}
	if current := m.connections[key]; current != nil {
		return current, nil
	}

	candidate, err := connect()
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, fmt.Errorf("app database connector returned nil")
	}

	m.connections[key] = candidate
	return candidate, nil
}

func (m *DatabaseManager) Lookup(key string) *sql.DB {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	current := m.connections[key]
	m.mu.Unlock()
	return current
}

func (m *DatabaseManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	count := len(m.connections)
	m.mu.Unlock()
	return count
}

func (m *DatabaseManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	connections := m.connections
	cleanups := m.cleanups
	m.connections = nil
	m.cleanups = nil
	m.mu.Unlock()

	for _, connection := range connections {
		_ = connection.Close()
	}
	for _, cleanup := range cleanups {
		cleanup()
	}
}
