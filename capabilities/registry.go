package capabilities

import (
	"sync"

	"github.com/kitwork/engine/value"
)

type Lifetime int

const (
	LifetimeTransient Lifetime = iota
	LifetimeRequest
	LifetimeApp
	LifetimeSingleton
)

// Closer represents a capability that holds resources needing cleanup on app unload.
type Closer interface {
	Close() error
}

// Factory constructs a capability JS object (value.Value) bound to a given Scope.
type Factory func(scope Scope) value.Value

type entry struct {
	factory  Factory
	lifetime Lifetime
}

// Registry manages registered capabilities and constructs capability instances for a Scope.
type Registry struct {
	mu          sync.RWMutex
	factories   map[string]entry
	singletonMu sync.Mutex
	singletons  map[string]value.Value
}

// NewRegistry creates a new capability Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories:  make(map[string]entry),
		singletons: make(map[string]value.Value),
	}
}

// DefaultRegistry is the global default capability registry for the engine.
var DefaultRegistry = NewRegistry()

// Register adds a capability factory under a name (e.g. "collection", "jwt", "qrcode").
func (r *Registry) Register(name string, factory Factory) {
	r.RegisterWithLifetime(name, LifetimeApp, factory)
}

// RegisterWithLifetime adds a capability factory with explicit lifetime scope.
func (r *Registry) RegisterWithLifetime(name string, lifetime Lifetime, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = entry{
		factory:  factory,
		lifetime: lifetime,
	}
}

// Get instantiates a capability object bound to the given Scope.
func (r *Registry) Get(name string, scope Scope) (value.Value, bool) {
	r.mu.RLock()
	ent, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return value.Value{K: value.Nil}, false
	}
	return ent.factory(scope), true
}

// GetLifetime returns the declared lifetime for a capability.
func (r *Registry) GetLifetime(name string) Lifetime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ent, ok := r.factories[name]; ok {
		return ent.lifetime
	}
	return LifetimeApp
}

func (r *Registry) getSingleton(name string, scope Scope) (value.Value, bool) {
	r.singletonMu.Lock()
	defer r.singletonMu.Unlock()

	if inst, ok := r.singletons[name]; ok {
		return inst, true
	}
	inst, ok := r.Get(name, scope)
	if !ok {
		return value.Value{K: value.Nil}, false
	}
	r.singletons[name] = inst
	return inst, true
}

// Close releases process-scoped capability instances. App-scoped instances are
// owned by their InstanceCache and are closed when that app unloads.
func (r *Registry) Close() {
	r.singletonMu.Lock()
	instances := r.singletons
	r.singletons = make(map[string]value.Value)
	r.singletonMu.Unlock()

	closeInstances(instances)
}

// InstanceCache caches capability instances per Scope (e.g. per tenant) across requests.
type InstanceCache struct {
	mu        sync.RWMutex
	instances map[string]value.Value
}

func NewInstanceCache() *InstanceCache {
	return &InstanceCache{
		instances: make(map[string]value.Value),
	}
}

func (c *InstanceCache) GetOrCompute(name string, registry *Registry, scope Scope) (value.Value, bool) {
	switch registry.GetLifetime(name) {
	case LifetimeTransient, LifetimeRequest:
		// A request cache does not exist yet, so request-scoped capabilities
		// deliberately degrade to transient instead of leaking across requests.
		return registry.Get(name, scope)
	case LifetimeSingleton:
		return registry.getSingleton(name, scope)
	}

	c.mu.RLock()
	inst, ok := c.instances[name]
	c.mu.RUnlock()
	if ok {
		return inst, true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if inst, ok := c.instances[name]; ok {
		return inst, true
	}
	computed, exists := registry.Get(name, scope)
	if !exists {
		return value.Value{K: value.Nil}, false
	}
	c.instances[name] = computed
	return computed, true
}

func (c *InstanceCache) Close() {
	c.mu.Lock()
	instances := c.instances
	c.instances = make(map[string]value.Value)
	c.mu.Unlock()

	closeInstances(instances)
}

func closeInstances(instances map[string]value.Value) {
	for _, inst := range instances {
		if closer, ok := inst.V.(Closer); ok {
			_ = closer.Close()
		}
	}
}
