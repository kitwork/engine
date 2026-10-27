package site

import (
	"fmt"
	"sync"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/utilities/cache"
	"github.com/kitwork/engine/value"
)

// Generation is one execution revision of a site. It owns immutable route,
// render, environment, presentation, and source snapshots plus RAM response
// state and generation-scoped capabilities. Concrete implementations remain
// behind lifecycle interfaces while work.Tenant is reduced to an adapter.
type Generation struct {
	owner   *Runtime
	version uint64

	capabilities  *capabilities.InstanceCache
	presentation  *Presentation
	sources       *SourceManifest
	routeGraph    RouteGraph
	renderPlan    RenderPlan
	responseCache *cache.Store
	bytecodeCache *compiler.FileCache
	environment   value.Value

	mu         sync.Mutex
	active     int
	retired    bool
	published  bool
	drained    chan struct{}
	retireDone chan struct{}
	retireOnce sync.Once
}

func newGeneration(owner *Runtime, version uint64) *Generation {
	return &Generation{
		owner:         owner,
		version:       version,
		capabilities:  capabilities.NewInstanceCache(),
		presentation:  newPresentation(),
		sources:       newSourceManifest(),
		responseCache: cache.NewStore(1000),
		environment:   value.Value{K: value.Nil},
		drained:       make(chan struct{}),
		retireDone:    make(chan struct{}),
	}
}

func (g *Generation) Version() uint64 {
	if g == nil {
		return 0
	}
	return g.version
}

// CapabilitiesCache owns LifetimeSite instances for this generation.
func (g *Generation) CapabilitiesCache() *capabilities.InstanceCache {
	if g == nil {
		return nil
	}
	return g.capabilities
}

// Presentation returns the preparation-time presentation builder. Activation
// freezes it before the generation becomes visible to requests.
func (g *Generation) Presentation() *Presentation {
	if g == nil {
		return nil
	}
	return g.presentation
}

// Sources returns the executable-source manifest prepared for this generation.
func (g *Generation) Sources() *SourceManifest {
	if g == nil {
		return nil
	}
	return g.sources
}

// SetRouteGraph installs the fully prepared executable graph before
// publication.
func (g *Generation) SetRouteGraph(graph RouteGraph) error {
	if g == nil {
		return fmt.Errorf("site generation is nil")
	}
	if graph == nil {
		return fmt.Errorf("site route graph is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.published || g.retired {
		return fmt.Errorf("site generation %d is not mutable", g.version)
	}
	if g.routeGraph != nil {
		return fmt.Errorf("site generation %d already has a route graph", g.version)
	}
	g.routeGraph = graph
	return nil
}

// RouteGraph returns the immutable graph published with this generation.
func (g *Generation) RouteGraph() RouteGraph {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	graph := g.routeGraph
	g.mu.Unlock()
	return graph
}

// SetRenderPlan installs the fully prepared template plan before publication.
func (g *Generation) SetRenderPlan(plan RenderPlan) error {
	if g == nil {
		return fmt.Errorf("site generation is nil")
	}
	if plan == nil {
		return fmt.Errorf("site render plan is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.published || g.retired {
		return fmt.Errorf("site generation %d is not mutable", g.version)
	}
	if g.renderPlan != nil {
		return fmt.Errorf("site generation %d already has a render plan", g.version)
	}
	g.renderPlan = plan
	return nil
}

// RenderPlan returns the immutable template plan published with this
// generation.
func (g *Generation) RenderPlan() RenderPlan {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	plan := g.renderPlan
	g.mu.Unlock()
	return plan
}

// SetEnvironment installs the immutable environment snapshot before
// publication.
func (g *Generation) SetEnvironment(environment value.Value) error {
	if g == nil {
		return fmt.Errorf("site generation is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.published || g.retired {
		return fmt.Errorf("site generation %d is not mutable", g.version)
	}
	g.environment = environment
	return nil
}

func (g *Generation) Environment() value.Value {
	if g == nil {
		return value.Value{K: value.Nil}
	}
	g.mu.Lock()
	environment := g.environment
	g.mu.Unlock()
	return environment
}

// ResponseCache owns generation-scoped RAM response and fetch entries.
func (g *Generation) ResponseCache() *cache.Store {
	if g == nil {
		return nil
	}
	return g.responseCache
}

// SetBytecodeCache installs the optional artifact cache used while preparing
// this generation. The cache handle belongs to the generation and cannot be
// replaced after the generation is published.
func (g *Generation) SetBytecodeCache(bytecodeCache *compiler.FileCache) error {
	if g == nil {
		return fmt.Errorf("site generation is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.published || g.retired {
		return fmt.Errorf("site generation %d is not mutable", g.version)
	}
	if g.bytecodeCache != nil {
		return fmt.Errorf("site generation %d already has a bytecode cache", g.version)
	}
	g.bytecodeCache = bytecodeCache
	return nil
}

// BytecodeCache returns the generation-owned compiler cache. A nil result
// means source compilation is intentionally uncached.
func (g *Generation) BytecodeCache() *compiler.FileCache {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	bytecodeCache := g.bytecodeCache
	g.mu.Unlock()
	return bytecodeCache
}

// Acquire pins this generation for one request.
func (g *Generation) Acquire() (*Lease, bool) {
	if g == nil {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired {
		return nil, false
	}
	g.active++
	return &Lease{generation: g}, true
}

func (g *Generation) release() {
	g.mu.Lock()
	if g.active > 0 {
		g.active--
	}
	if g.retired && g.active == 0 {
		select {
		case <-g.drained:
		default:
			close(g.drained)
		}
	}
	g.mu.Unlock()
}

// Retire prevents new request leases and waits for accepted requests to drain.
func (g *Generation) Retire() {
	if g == nil {
		return
	}
	g.retireOnce.Do(func() {
		g.mu.Lock()
		g.retired = true
		if g.active == 0 {
			select {
			case <-g.drained:
			default:
				close(g.drained)
			}
		}
		drained := g.drained
		g.mu.Unlock()

		<-drained
		g.capabilities.Close()
		g.responseCache.Close()
		g.mu.Lock()
		graph := g.routeGraph
		g.routeGraph = nil
		plan := g.renderPlan
		g.renderPlan = nil
		g.mu.Unlock()
		if graph != nil {
			graph.Close()
		}
		if plan != nil {
			plan.Close()
		}
		close(g.retireDone)
	})
	<-g.retireDone
}

func (g *Generation) Active() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	active := g.active
	g.mu.Unlock()
	return active
}

func (g *Generation) Retired() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	retired := g.retired
	g.mu.Unlock()
	return retired
}

// Lease is a release-once request pin for a generation.
type Lease struct {
	generation *Generation
	once       sync.Once
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.generation.release()
	})
}
