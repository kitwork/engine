// Package site owns domain-scoped runtime identity and lifecycle.
package site

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/utilities/ratelimit"
	"github.com/kitwork/engine/utilities/sse"
)

// App is the narrow parent contract a site needs. Keeping this interface here
// lets package app own concrete sites without creating an import cycle.
type App interface {
	ID() string
}

// Runtime represents one domain inside an application identity. It owns
// monotonic generation publication and retirement plus state that must survive
// generation replacement: persistent responses, rate limits, and SSE streams.
type Runtime struct {
	app    App
	root   string
	domain string

	generationMu sync.RWMutex
	current      *Generation
	nextVersion  uint64

	resourceMu   sync.Mutex
	resourceRoot string
	persistStore *persist.Store
	limiter      *ratelimit.Limiter
	sseBroker    *sse.SSEBroker

	closeOnce sync.Once
	closed    atomic.Bool
}

func NewRuntime(parent App, root, domain string) *Runtime {
	return &Runtime{
		app:       parent,
		root:      root,
		domain:    domain,
		limiter:   ratelimit.New(),
		sseBroker: sse.NewSSEBroker(),
	}
}

func (r *Runtime) App() App {
	if r == nil {
		return nil
	}
	return r.app
}

func (r *Runtime) AppID() string {
	if r == nil || r.app == nil {
		return ""
	}
	return r.app.ID()
}

func (r *Runtime) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

func (r *Runtime) Domain() string {
	if r == nil {
		return ""
	}
	return r.domain
}

// PrepareGeneration creates an inactive site revision. The caller must finish
// initialization before ActivateGeneration publishes it.
func (r *Runtime) PrepareGeneration() (*Generation, error) {
	if r == nil {
		return nil, fmt.Errorf("site runtime is nil")
	}
	r.generationMu.Lock()
	defer r.generationMu.Unlock()
	if r.closed.Load() {
		return nil, fmt.Errorf("site runtime %q is closed", r.domain)
	}
	r.nextVersion++
	return newGeneration(r, r.nextVersion), nil
}

// ActivateGeneration atomically publishes a prepared revision and returns the
// previously active revision. Retirement is left to its owning Tenant so it can
// first stop accepting requests and drain its other resources.
func (r *Runtime) ActivateGeneration(next *Generation) (*Generation, error) {
	if r == nil || next == nil {
		return nil, fmt.Errorf("site generation is nil")
	}
	if next.owner != r {
		return nil, fmt.Errorf("site generation belongs to another runtime")
	}

	r.generationMu.Lock()
	defer r.generationMu.Unlock()
	if r.closed.Load() {
		return nil, fmt.Errorf("site runtime %q is closed", r.domain)
	}
	if r.current == next {
		return nil, nil
	}
	if r.current != nil && next.version <= r.current.version {
		return nil, fmt.Errorf(
			"site generation %d is older than current generation %d",
			next.version,
			r.current.version,
		)
	}
	next.mu.Lock()
	defer next.mu.Unlock()
	if next.retired {
		return nil, fmt.Errorf("site generation %d is retired", next.version)
	}
	next.published = true
	next.presentation.Freeze()
	next.sources.Freeze()
	previous := r.current
	r.current = next
	return previous, nil
}

func (r *Runtime) CurrentGeneration() *Generation {
	if r == nil {
		return nil
	}
	r.generationMu.RLock()
	current := r.current
	r.generationMu.RUnlock()
	return current
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		r.StopStreams()
		r.generationMu.Lock()
		current := r.current
		r.current = nil
		r.generationMu.Unlock()
		if current != nil {
			current.Retire()
		}
	})
}

func (r *Runtime) Closed() bool {
	return r == nil || r.closed.Load()
}
