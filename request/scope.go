// Package request owns state whose lifetime is exactly one HTTP request.
package request

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// Scope is the request-level owner beneath a site runtime. It delegates stable
// app/site services to its parent while owning cancellation, request-scoped
// capabilities, and at most one leased VM.
type Scope struct {
	parent  capabilities.Scope
	writer  http.ResponseWriter
	request *http.Request
	ctx     context.Context
	cancel  context.CancelFunc

	capabilities *capabilities.InstanceCache
	principal    Principal
	permissions  map[string]struct{}

	vmMu      sync.Mutex
	vm        *runtime.VM
	releaseVM func(*runtime.VM)

	executionMu sync.Mutex
	executionWG sync.WaitGroup

	cleanupMu sync.Mutex
	cleanups  []func()

	closeOnce sync.Once
	closed    atomic.Bool
}

var _ capabilities.Scope = (*Scope)(nil)
var _ capabilities.Runtime = (*Scope)(nil)

func New(parent capabilities.Scope, writer http.ResponseWriter, req *http.Request) *Scope {
	parentContext := context.Background()
	if req != nil && req.Context() != nil {
		parentContext = req.Context()
	}
	ctx, cancel := context.WithCancel(parentContext)
	authorization := authorizationFromContext(parentContext)
	permissions := make(map[string]struct{}, len(authorization.Permissions))
	for _, permission := range authorization.Permissions {
		if permission != "" {
			permissions[permission] = struct{}{}
		}
	}
	return &Scope{
		parent:       parent,
		writer:       writer,
		request:      req,
		ctx:          ctx,
		cancel:       cancel,
		capabilities: capabilities.NewInstanceCache(),
		principal:    authorization.Principal,
		permissions:  permissions,
	}
}

func (s *Scope) Context() context.Context {
	if s == nil || s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *Scope) Request() *http.Request {
	if s == nil {
		return nil
	}
	return s.request
}

func (s *Scope) Writer() http.ResponseWriter {
	if s == nil {
		return nil
	}
	return s.writer
}

func (s *Scope) CapabilitiesCache() *capabilities.InstanceCache {
	if s == nil {
		return nil
	}
	return s.capabilities
}

func (s *Scope) Principal() Principal {
	if s == nil {
		return Principal{}
	}
	authorization := cloneAuthorization(Authorization{Principal: s.principal})
	return authorization.Principal
}

// HasPermission implements capabilities.PermissionChecker. The wildcard grant
// is reserved for trusted host middleware and is never inferred from input.
func (s *Scope) HasPermission(permission string) bool {
	if s == nil || permission == "" {
		return false
	}
	if _, ok := s.permissions["*"]; ok {
		return true
	}
	_, ok := s.permissions[permission]
	return ok
}

func (s *Scope) AppID() string {
	if s == nil || s.parent == nil {
		return ""
	}
	return s.parent.AppID()
}

func (s *Scope) Domain() string {
	if s == nil || s.parent == nil {
		return ""
	}
	return s.parent.Domain()
}

func (s *Scope) ResolvePath(paths ...string) string {
	if s == nil || s.parent == nil {
		return ""
	}
	return s.parent.ResolvePath(paths...)
}

func (s *Scope) DB(name string) *sql.DB {
	if s == nil || s.parent == nil {
		return nil
	}
	return s.parent.DB(name)
}

// Execute preserves the existing optional compute seam for capabilities that
// assert capabilities.Runtime. The parent remains the owner of this detached
// execution; request-bound handlers use the leased VM instead.
func (s *Scope) Execute(
	bc *compiler.Bytecode,
	fn *value.Lambda,
	args []value.Value,
) (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("request scope is nil")
	}
	executor, ok := s.parent.(capabilities.Runtime)
	if !ok {
		return 0, fmt.Errorf("request scope parent does not provide execution")
	}
	return executor.Execute(bc, fn, args)
}

// LeaseVM acquires the request's one VM lease. Repeated calls return the same
// lease until ReleaseVM is called.
func (s *Scope) LeaseVM(
	acquire func() *runtime.VM,
	release func(*runtime.VM),
) (*runtime.VM, error) {
	if s == nil || acquire == nil || release == nil {
		return nil, fmt.Errorf("request VM lease is not configured")
	}

	s.vmMu.Lock()
	defer s.vmMu.Unlock()
	if s.closed.Load() {
		return nil, fmt.Errorf("request scope is closed")
	}
	if s.vm != nil {
		return s.vm, nil
	}

	vm := acquire()
	if vm == nil {
		return nil, fmt.Errorf("request VM pool returned nil")
	}
	vm.Context = s.ctx
	s.vm = vm
	s.releaseVM = release
	return vm, nil
}

func (s *Scope) ReleaseVM() {
	if s == nil {
		return
	}
	s.vmMu.Lock()
	vm := s.vm
	release := s.releaseVM
	s.vm = nil
	s.releaseVM = nil
	s.vmMu.Unlock()

	if vm != nil && release != nil {
		vm.Context = nil
		release(vm)
	}
}

// AcquireExecutionVM tracks an additional short-lived VM used by work nested
// inside a request, such as a query predicate or transaction callback.
func (s *Scope) AcquireExecutionVM(
	acquire func() *runtime.VM,
	release func(*runtime.VM),
) (*runtime.VM, func(), error) {
	if s == nil || acquire == nil || release == nil {
		return nil, nil, fmt.Errorf("request execution VM lease is not configured")
	}

	s.executionMu.Lock()
	if s.closed.Load() {
		s.executionMu.Unlock()
		return nil, nil, fmt.Errorf("request scope is closed")
	}
	s.executionWG.Add(1)
	s.executionMu.Unlock()

	vm := acquire()
	if vm == nil {
		s.executionWG.Done()
		return nil, nil, fmt.Errorf("request VM pool returned nil")
	}
	vm.Context = s.ctx

	var once sync.Once
	done := func() {
		once.Do(func() {
			vm.Context = nil
			release(vm)
			s.executionWG.Done()
		})
	}
	return vm, done, nil
}

// AddCleanup transfers a release callback to the request. Cleanups run in
// reverse registration order after VMs and request capabilities are closed.
func (s *Scope) AddCleanup(cleanup func()) bool {
	if s == nil || cleanup == nil {
		return false
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.closed.Load() {
		return false
	}
	s.cleanups = append(s.cleanups, cleanup)
	return true
}

func (s *Scope) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.cancel()
		s.ReleaseVM()

		// Synchronize with an AcquireExecutionVM that passed the initial closed
		// check before shutdown, then wait for every accepted child lease.
		s.executionMu.Lock()
		s.executionMu.Unlock()
		s.executionWG.Wait()

		s.capabilities.Close()

		s.cleanupMu.Lock()
		cleanups := s.cleanups
		s.cleanups = nil
		s.cleanupMu.Unlock()
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	})
}

func (s *Scope) Closed() bool {
	return s == nil || s.closed.Load()
}
