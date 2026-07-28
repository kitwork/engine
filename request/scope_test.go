package request_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/capabilities"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

type parentScope struct{}

func (parentScope) AppID() string                      { return "app-a" }
func (parentScope) Domain() string                     { return "example.com" }
func (parentScope) ResolvePath(paths ...string) string { return "/site" }
func (parentScope) DB(string) *sql.DB                  { return nil }

type closer struct {
	count atomic.Int32
}

func (c *closer) Close() error {
	c.count.Add(1)
	return nil
}

func TestScopeOwnsCancellationCapabilitiesAndVMLease(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	scope := requestscope.New(parentScope{}, httptest.NewRecorder(), req)

	registry := capabilities.NewRegistry()
	resource := &closer{}
	registry.RegisterWithLifetime("request", capabilities.LifetimeRequest, func(capabilities.Scope) value.Value {
		return value.New(resource)
	})
	first, ok := registry.Resolve(
		"request",
		scope,
		nil,
		nil,
		scope.CapabilitiesCache(),
	)
	if !ok {
		t.Fatal("request capability was not created")
	}
	second, _ := registry.Resolve(
		"request",
		scope,
		nil,
		nil,
		scope.CapabilitiesCache(),
	)
	if first.V != second.V {
		t.Fatal("request capability was not reused inside one scope")
	}

	pool := app.NewPool()
	vm, err := scope.LeaseVM(pool.Acquire, pool.Release)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Context != scope.Context() || pool.Active() != 1 {
		t.Fatal("request scope did not own the active VM lease")
	}
	childVM, releaseChild, err := scope.AcquireExecutionVM(pool.Acquire, pool.Release)
	if err != nil {
		t.Fatal(err)
	}
	if childVM.Context != scope.Context() || pool.Active() != 2 {
		t.Fatal("request scope did not track its child VM lease")
	}
	var cleanupCalls atomic.Int32
	if !scope.AddCleanup(func() { cleanupCalls.Add(1) }) {
		t.Fatal("request scope rejected cleanup ownership")
	}

	closed := make(chan struct{})
	go func() {
		scope.Close()
		close(closed)
	}()
	select {
	case <-scope.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("request close did not cancel its context")
	}
	select {
	case <-closed:
		t.Fatal("request closed before its child VM lease returned")
	default:
	}
	deadline := time.Now().Add(time.Second)
	for pool.Active() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if pool.Active() != 1 {
		t.Fatalf("request close left %d VMs active while waiting", pool.Active())
	}
	releaseChild()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("request did not finish closing after child VM release")
	}
	scope.Close()
	if !scope.Closed() || pool.Active() != 0 {
		t.Fatal("request scope did not release its VM")
	}
	if resource.count.Load() != 1 {
		t.Fatalf("request capability closed %d times, want 1", resource.count.Load())
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("request cleanup ran %d times, want 1", cleanupCalls.Load())
	}
	if !contextCanceled(scope.Context()) {
		t.Fatal("request context was not cancelled on close")
	}
	if scope.AddCleanup(func() {}) {
		t.Fatal("closed request accepted another cleanup")
	}
}

func TestScopeOwnsImmutablePrincipalAndCapabilityPermissions(t *testing.T) {
	attributes := map[string]string{"role": "editor"}
	authorization := requestscope.Authorization{
		Principal: requestscope.Principal{
			Subject:       "user-123",
			Authenticated: true,
			Attributes:    attributes,
		},
		Permissions: []string{"content.read"},
	}
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req = req.WithContext(requestscope.WithAuthorization(req.Context(), authorization))
	scope := requestscope.New(parentScope{}, httptest.NewRecorder(), req)
	defer scope.Close()

	attributes["role"] = "admin"
	authorization.Permissions[0] = "content.write"
	principal := scope.Principal()
	if principal.Subject != "user-123" || !principal.Authenticated ||
		principal.Attributes["role"] != "editor" {
		t.Fatalf("principal was not frozen at request creation: %+v", principal)
	}
	principal.Attributes["role"] = "changed"
	if scope.Principal().Attributes["role"] != "editor" {
		t.Fatal("Principal returned mutable request state")
	}
	if !scope.HasPermission("content.read") || scope.HasPermission("content.write") {
		t.Fatal("request permissions do not match the trusted authorization snapshot")
	}

	registry := capabilities.NewRegistry()
	var factoryReceivedRequest atomic.Bool
	registry.RegisterWithPermissions(
		"reader",
		capabilities.LifetimeApp,
		[]string{"content.read"},
		func(factoryScope capabilities.Scope) value.Value {
			_, isRequest := factoryScope.(*requestscope.Scope)
			factoryReceivedRequest.Store(isRequest)
			return value.New("allowed")
		},
	)
	appCache := capabilities.NewInstanceCache()
	defer appCache.Close()
	got, ok := registry.ResolveAuthorized(
		"reader",
		parentScope{},
		scope,
		appCache,
		nil,
		scope.CapabilitiesCache(),
	)
	if !ok || got.Text() != "allowed" {
		t.Fatal("authorized capability was denied")
	}
	if factoryReceivedRequest.Load() {
		t.Fatal("app-scoped capability factory retained a request scope")
	}

	deniedRegistry := capabilities.NewRegistry()
	deniedRegistry.RegisterWithPermissions(
		"writer",
		capabilities.LifetimeTransient,
		[]string{"content.write"},
		func(capabilities.Scope) value.Value { return value.New("denied") },
	)
	if _, ok := deniedRegistry.ResolveAuthorized(
		"writer",
		parentScope{},
		scope,
		nil,
		nil,
	); ok {
		t.Fatal("capability resolved without its required permission")
	}
}

func contextCanceled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
