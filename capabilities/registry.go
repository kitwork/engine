package capabilities

import (
	"sync"

	"github.com/kitwork/engine/value"
)

// Factory constructs a capability JS object (value.Value) bound to a given Scope.
type Factory func(scope Scope) value.Value

// Registry manages registered capabilities and constructs capability instances for a Scope.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry creates a new capability Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
	}
}

// DefaultRegistry is the global default capability registry for the engine.
var DefaultRegistry = NewRegistry()

// Register adds a capability factory under a name (e.g. "collection", "jwt", "qrcode").
func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Get instantiates a capability object bound to the given Scope.
func (r *Registry) Get(name string, scope Scope) (value.Value, bool) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return value.Value{K: value.Nil}, false
	}
	return factory(scope), true
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
