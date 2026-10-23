package app

import (
	"fmt"
)

// Resource is app-wide infrastructure whose lifecycle ends with Runtime.
// Concrete scheduler code remains in work to avoid coupling app to the VM.
type Resource interface {
	Close()
}

// Drainer is a resource that runs work of its own (a worker): Runtime.Close drains every one before it
// closes any resource, while all of them are still installed, so work that ends during the drain can
// still use the others (the app's database) to record what it did. Close follows as for any resource.
type Drainer interface {
	Drain()
}

// InstallResource publishes one named app resource. The runtime takes
// ownership only when installed is true.
func (r *Runtime) InstallResource(name string, resource Resource) (current Resource, installed bool, err error) {
	if r == nil {
		return nil, false, fmt.Errorf("app runtime is nil")
	}
	if name == "" || resource == nil {
		return nil, false, fmt.Errorf("app resource is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed.Load() {
		return nil, false, fmt.Errorf("app runtime %q is closed", r.identity)
	}
	if current = r.resources[name]; current != nil {
		return current, false, nil
	}
	r.resources[name] = resource
	return resource, true, nil
}

func (r *Runtime) Resource(name string) Resource {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	resource := r.resources[name]
	r.mu.RUnlock()
	return resource
}
