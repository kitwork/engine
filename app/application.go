package app

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/site"
)

// Runtime is the identity-scoped owner of an application. One Runtime may own
// several domain-scoped site runtimes.
type Runtime struct {
	identity string

	mu        sync.RWMutex
	sites     map[string]*site.Runtime
	resources map[string]Resource

	capabilities *capabilities.InstanceCache
	databases    *DatabaseManager
	tasks        *TaskGroup

	closeOnce sync.Once
	closed    atomic.Bool
}

func NewRuntime(identity string) *Runtime {
	return &Runtime{
		identity:     identity,
		sites:        make(map[string]*site.Runtime),
		resources:    make(map[string]Resource),
		capabilities: capabilities.NewInstanceCache(),
		databases:    newDatabaseManager(),
		tasks:        newTaskGroup(),
	}
}

func (r *Runtime) ID() string {
	if r == nil {
		return ""
	}
	return r.identity
}

// Site returns the one site runtime owned by this app for domain.
func (r *Runtime) Site(root, domain string) (*site.Runtime, error) {
	if r == nil {
		return nil, fmt.Errorf("app runtime is nil")
	}

	r.mu.RLock()
	current := r.sites[domain]
	isClosed := r.closed.Load()
	r.mu.RUnlock()
	if isClosed {
		return nil, fmt.Errorf("app runtime %q is closed", r.identity)
	}
	if current != nil {
		return current, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed.Load() {
		return nil, fmt.Errorf("app runtime %q is closed", r.identity)
	}
	if current = r.sites[domain]; current != nil {
		return current, nil
	}
	current = site.NewRuntime(r, root, domain)
	r.sites[domain] = current
	return current, nil
}

func (r *Runtime) LookupSite(domain string) (*site.Runtime, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	current, ok := r.sites[domain]
	r.mu.RUnlock()
	return current, ok
}

// RemoveSite detaches and closes one domain without affecting sibling sites or
// the app-wide runtime.
func (r *Runtime) RemoveSite(domain string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	current := r.sites[domain]
	delete(r.sites, domain)
	r.mu.Unlock()
	if current != nil {
		current.Close()
	}
}

func (r *Runtime) SiteCount() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	count := len(r.sites)
	r.mu.RUnlock()
	return count
}

// CapabilitiesCache owns LifetimeApp instances for this identity.
func (r *Runtime) CapabilitiesCache() *capabilities.InstanceCache {
	if r == nil {
		return nil
	}
	return r.capabilities
}

func (r *Runtime) Databases() *DatabaseManager {
	if r == nil {
		return nil
	}
	return r.databases
}

func (r *Runtime) Tasks() *TaskGroup {
	if r == nil {
		return nil
	}
	return r.tasks
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.closed.Store(true)

		r.mu.Lock()
		sites := make([]*site.Runtime, 0, len(r.sites))
		for _, current := range r.sites {
			sites = append(sites, current)
		}
		r.sites = make(map[string]*site.Runtime)
		resources := make([]Resource, 0, len(r.resources))
		for _, resource := range r.resources {
			resources = append(resources, resource)
		}
		r.resources = make(map[string]Resource)
		r.mu.Unlock()

		for _, current := range sites {
			current.Close()
		}
		if r.tasks != nil {
			r.tasks.Close()
		}
		for _, resource := range resources {
			resource.Close()
		}
		if r.capabilities != nil {
			r.capabilities.Close()
		}
		if r.databases != nil {
			r.databases.Close()
		}
	})
}

func (r *Runtime) Closed() bool {
	return r == nil || r.closed.Load()
}
