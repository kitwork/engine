package site

import (
	"fmt"
	"path/filepath"

	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/utilities/ratelimit"
	"github.com/kitwork/engine/utilities/sse"
)

// ConfigureResources binds durable site resources to the resolved site root.
// Repeated calls from replacement generations are idempotent.
func (r *Runtime) ConfigureResources(siteRoot string) error {
	if r == nil {
		return fmt.Errorf("site runtime is nil")
	}
	absolute, err := filepath.Abs(siteRoot)
	if err != nil {
		return fmt.Errorf("resolve site resource root: %w", err)
	}
	absolute = filepath.Clean(absolute)

	r.resourceMu.Lock()
	defer r.resourceMu.Unlock()
	if r.closed.Load() {
		return fmt.Errorf("site runtime %q is closed", r.domain)
	}
	if r.resourceRoot != "" && r.resourceRoot != absolute {
		return fmt.Errorf(
			"site runtime %q already uses resource root %q",
			r.domain,
			r.resourceRoot,
		)
	}
	if r.resourceRoot == "" {
		r.resourceRoot = absolute
		r.persistStore = persist.New(filepath.Join(absolute, ".persist"))
	}
	return nil
}

func (r *Runtime) PersistStore() *persist.Store {
	if r == nil {
		return nil
	}
	r.resourceMu.Lock()
	store := r.persistStore
	r.resourceMu.Unlock()
	return store
}

func (r *Runtime) Limiter() *ratelimit.Limiter {
	if r == nil {
		return nil
	}
	r.resourceMu.Lock()
	limiter := r.limiter
	r.resourceMu.Unlock()
	return limiter
}

// SSEBroker owns live streams and replay history for the complete site
// lifetime, including all hot-reload generations.
func (r *Runtime) SSEBroker() *sse.SSEBroker {
	if r == nil {
		return nil
	}
	r.resourceMu.Lock()
	broker := r.sseBroker
	r.resourceMu.Unlock()
	return broker
}

// StopStreams disconnects live streams before generation drain. SSE requests
// otherwise keep their generation lease until the stream ends.
func (r *Runtime) StopStreams() {
	if r == nil {
		return
	}
	r.resourceMu.Lock()
	broker := r.sseBroker
	r.resourceMu.Unlock()
	if broker != nil {
		broker.Stop()
	}
}
