package gateway

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
)

// ErrManagerClosed is returned when a replacement is attempted after Close.
var ErrManagerClosed = errors.New("gateway manager is closed")

// Manager owns the currently published gateway snapshot. Replacements are
// built before publication, swapped atomically, and then drain the superseded
// snapshot before releasing its health workers and idle connections.
type Manager struct {
	current   atomic.Pointer[managedSnapshot]
	replaceMu sync.Mutex
	closed    bool
	options   handlerOptions
}

type managedSnapshot struct {
	handler *Handler

	mu       sync.Mutex
	active   int
	draining bool
	drained  chan struct{}
}

func newManagedSnapshot(handler *Handler) *managedSnapshot {
	return &managedSnapshot{
		handler: handler,
		drained: make(chan struct{}),
	}
}

func (s *managedSnapshot) acquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	s.active++
	return true
}

func (s *managedSnapshot) release() {
	s.mu.Lock()
	s.active--
	if s.draining && s.active == 0 {
		close(s.drained)
	}
	s.mu.Unlock()
}

func (s *managedSnapshot) drain() {
	s.mu.Lock()
	s.draining = true
	if s.active == 0 {
		close(s.drained)
	}
	drained := s.drained
	s.mu.Unlock()

	<-drained
	s.handler.Close()
}

// NewManager validates and publishes the first gateway snapshot.
func NewManager(targets []Target) (*Manager, error) {
	return newManager(targets, defaultHandlerOptions())
}

func newManager(targets []Target, options handlerOptions) (*Manager, error) {
	handler, err := newHandler(targets, options)
	if err != nil {
		return nil, err
	}
	manager := &Manager{options: options}
	manager.current.Store(newManagedSnapshot(handler))
	return manager, nil
}

// Replace validates and builds a complete replacement before publishing it.
// Invalid replacements leave the current snapshot untouched. Replace waits for
// requests already using the old snapshot, while new requests use the new one.
func (m *Manager) Replace(targets []Target) error {
	if m == nil {
		return ErrManagerClosed
	}

	m.replaceMu.Lock()
	defer m.replaceMu.Unlock()
	if m.closed {
		return ErrManagerClosed
	}

	handler, err := newHandler(targets, m.options)
	if err != nil {
		return err
	}
	replacement := newManagedSnapshot(handler)

	previous := m.current.Swap(replacement)
	if previous != nil {
		previous.drain()
	}
	return nil
}

func (m *Manager) acquire() *managedSnapshot {
	if m == nil {
		return nil
	}
	for {
		current := m.current.Load()
		if current == nil {
			return nil
		}
		if current.acquire() {
			return current
		}
	}
}

// ServeExact proxies a request when domain has an explicitly declared pool.
func (m *Manager) ServeExact(writer http.ResponseWriter, request *http.Request, domain string) bool {
	current := m.acquire()
	if current == nil {
		return false
	}
	defer current.release()
	return current.handler.ServeExact(writer, request, domain)
}

// ServeDefault proxies through the domainless pool when one was declared.
func (m *Manager) ServeDefault(writer http.ResponseWriter, request *http.Request) bool {
	current := m.acquire()
	if current == nil {
		return false
	}
	defer current.release()
	return current.handler.ServeDefault(writer, request)
}

// HasDefault reports whether the published snapshot has a default pool.
func (m *Manager) HasDefault() bool {
	current := m.acquire()
	if current == nil {
		return false
	}
	defer current.release()
	return current.handler.HasDefault()
}

// Snapshot returns diagnostics from the currently published snapshot.
func (m *Manager) Snapshot() []TargetStats {
	current := m.acquire()
	if current == nil {
		return nil
	}
	defer current.release()
	return current.handler.Snapshot()
}

// Close stops publication, drains active requests, and releases the current
// snapshot. It is safe to call more than once.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.replaceMu.Lock()
	defer m.replaceMu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	current := m.current.Swap(nil)
	if current != nil {
		current.drain()
	}
}
