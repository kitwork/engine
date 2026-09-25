package proxies

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// Manager owns every configured listener and their active connections.
type Manager struct {
	runtimes []*listenerRuntime
	errors   chan error
	close    sync.Once
}

type listenerRuntime struct {
	config    Config
	listener  *trackedListener
	http      *http.Server
	transport *http.Transport
}

// New validates and opens all listeners before starting any serving loop.
func New(configs []Config) (*Manager, error) {
	prepared, err := prepare(configs)
	if err != nil {
		return nil, err
	}
	manager := &Manager{errors: make(chan error, len(prepared))}
	for _, configured := range prepared {
		listener, err := net.Listen("tcp", configured.address())
		if err != nil {
			manager.closeListeners()
			return nil, fmt.Errorf("listen %s proxy on %s: %w", configured.Protocol, configured.address(), err)
		}
		manager.runtimes = append(manager.runtimes, &listenerRuntime{
			config:   configured,
			listener: newTrackedListener(listener),
		})
	}
	for _, runtime := range manager.runtimes {
		if runtime.config.Protocol == "socks5" {
			go manager.serveSOCKS5(runtime)
		} else {
			prepareHTTP(runtime)
			go manager.serveHTTP(runtime)
		}
	}
	return manager, nil
}

// Errors reports unexpected listener failures. Close-related errors are omitted.
func (m *Manager) Errors() <-chan error {
	if m == nil {
		return nil
	}
	return m.errors
}

// Close drains HTTP requests, closes tunnels, and releases every listener.
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	var result error
	m.close.Do(func() {
		for _, runtime := range m.runtimes {
			if runtime.http != nil {
				result = errors.Join(result, closeError(runtime.http.Shutdown(ctx)))
			}
			result = errors.Join(result, closeError(runtime.listener.Close()))
			runtime.listener.closeConnections()
			if runtime.transport != nil {
				runtime.transport.CloseIdleConnections()
			}
		}
	})
	return result
}

func closeError(err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (m *Manager) closeListeners() {
	for _, runtime := range m.runtimes {
		_ = runtime.listener.Close()
		runtime.listener.closeConnections()
	}
}

func (m *Manager) report(err error) {
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return
	}
	select {
	case m.errors <- err:
	default:
	}
}

type trackedListener struct {
	net.Listener
	mu     sync.Mutex
	conns  map[*trackedConn]struct{}
	closed bool
}

type trackedConn struct {
	net.Conn
	owner *trackedListener
	once  sync.Once
}

func newTrackedListener(listener net.Listener) *trackedListener {
	return &trackedListener{Listener: listener, conns: make(map[*trackedConn]struct{})}
}

func (l *trackedListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		tracked := &trackedConn{Conn: connection, owner: l}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = connection.Close()
			return nil, net.ErrClosed
		}
		if len(l.conns) >= 1024 {
			l.mu.Unlock()
			_ = connection.Close()
			continue
		}
		l.conns[tracked] = struct{}{}
		l.mu.Unlock()
		return tracked, nil
	}
}

func (l *trackedListener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Listener.Close()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.owner.mu.Unlock()
	})
	return err
}

func (l *trackedListener) closeConnections() {
	l.mu.Lock()
	connections := make([]*trackedConn, 0, len(l.conns))
	for connection := range l.conns {
		connections = append(connections, connection)
	}
	l.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}
