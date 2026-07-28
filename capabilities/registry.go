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
	// LifetimeSite is appended to preserve the numeric values of the original
	// public constants. New code should prefer named constants, never integers.
	LifetimeSite
)

// Closer represents a capability that holds resources needing cleanup on app unload.
type Closer interface {
	Close() error
}

// Factory constructs a capability JS object (value.Value) bound to a given Scope.
type Factory func(scope Scope) value.Value

type entry struct {
	factory     Factory
	lifetime    Lifetime
	permissions []string
}

type PermissionChecker interface {
	HasPermission(permission string) bool
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

// Register adds a site-scoped capability factory under a name. This preserves
// the historical behavior where one Tenant (one domain) owned the cache.
func (r *Registry) Register(name string, factory Factory) {
	r.RegisterWithLifetime(name, LifetimeSite, factory)
}

// RegisterWithLifetime adds a capability factory with explicit lifetime scope.
func (r *Registry) RegisterWithLifetime(name string, lifetime Lifetime, factory Factory) {
	r.RegisterWithPermissions(name, lifetime, nil, factory)
}

// RegisterWithPermissions declares capability grants required at resolution.
// Existing capabilities remain unrestricted when permissions is empty.
func (r *Registry) RegisterWithPermissions(
	name string,
	lifetime Lifetime,
	permissions []string,
	factory Factory,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = entry{
		factory:     factory,
		lifetime:    lifetime,
		permissions: append([]string(nil), permissions...),
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
	return LifetimeSite
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

// Close releases process-scoped capability instances. App- and site-scoped
// instances are closed by their runtime owners.
func (r *Registry) Close() {
	r.singletonMu.Lock()
	instances := r.singletons
	r.singletons = make(map[string]value.Value)
	r.singletonMu.Unlock()

	closeInstances(instances)
}

// InstanceCache caches capability instances for one runtime owner.
type InstanceCache struct {
	mu        sync.RWMutex
	instances map[string]value.Value
	closed    bool
}

func NewInstanceCache() *InstanceCache {
	return &InstanceCache{
		instances: make(map[string]value.Value),
	}
}

func (c *InstanceCache) GetOrCompute(name string, registry *Registry, scope Scope) (value.Value, bool) {
	switch registry.GetLifetime(name) {
	case LifetimeTransient, LifetimeRequest:
		// This compatibility method has only one owner cache. Request-scoped
		// capabilities therefore remain transient; production uses Resolve with
		// an explicit request cache.
		return registry.Get(name, scope)
	case LifetimeSingleton:
		return registry.getSingleton(name, scope)
	}
	return c.getOrCompute(name, registry, scope)
}

func (c *InstanceCache) getOrCompute(name string, registry *Registry, scope Scope) (value.Value, bool) {
	if c == nil {
		return registry.Get(name, scope)
	}
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return value.Value{K: value.Nil}, false
	}
	inst, ok := c.instances[name]
	c.mu.RUnlock()
	if ok {
		return inst, true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return value.Value{K: value.Nil}, false
	}
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

// Resolve selects the cache belonging to the capability's declared owner. The
// optional request cache keeps the original call shape compatible for
// off-request execution, where LifetimeRequest deliberately stays transient.
func (r *Registry) Resolve(
	name string,
	scope Scope,
	appCache *InstanceCache,
	siteCache *InstanceCache,
	requestCaches ...*InstanceCache,
) (value.Value, bool) {
	return r.ResolveAuthorized(
		name,
		scope,
		scope,
		appCache,
		siteCache,
		requestCaches...,
	)
}

// ResolveAuthorized separates the scope used to construct a cached capability
// from the request scope used to enforce access. This prevents an app-scoped
// factory from accidentally retaining request state.
func (r *Registry) ResolveAuthorized(
	name string,
	factoryScope Scope,
	accessScope Scope,
	appCache *InstanceCache,
	siteCache *InstanceCache,
	requestCaches ...*InstanceCache,
) (value.Value, bool) {
	if !r.allowed(name, accessScope) {
		return value.Value{K: value.Nil}, false
	}
	switch r.GetLifetime(name) {
	case LifetimeTransient:
		return r.Get(name, factoryScope)
	case LifetimeRequest:
		var requestCache *InstanceCache
		if len(requestCaches) > 0 {
			requestCache = requestCaches[0]
		}
		return requestCache.getOrCompute(name, r, factoryScope)
	case LifetimeSingleton:
		return r.getSingleton(name, factoryScope)
	case LifetimeApp:
		return appCache.getOrCompute(name, r, factoryScope)
	case LifetimeSite:
		return siteCache.getOrCompute(name, r, factoryScope)
	default:
		return r.Get(name, factoryScope)
	}
}

func (r *Registry) allowed(name string, scope Scope) bool {
	r.mu.RLock()
	ent, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok || len(ent.permissions) == 0 {
		return ok
	}
	checker, ok := scope.(PermissionChecker)
	if !ok {
		return false
	}
	for _, permission := range ent.permissions {
		if !checker.HasPermission(permission) {
			return false
		}
	}
	return true
}

func (c *InstanceCache) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	instances := c.instances
	c.instances = nil
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
