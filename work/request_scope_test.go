package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/kitwork/engine/capabilities"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

type requestCapabilityResource struct {
	id     int32
	closed atomic.Int32
}

func TestServePinsAndReleasesSiteGeneration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "identity-a", "example.com")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router } from "kitwork";
router.get().handle((ctx) => ctx.text("ok"));`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(root, "example.com")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	if err := tenant.ActivateGeneration(); err != nil {
		t.Fatal(err)
	}
	generation := tenant.SiteGeneration()

	recorder := httptest.NewRecorder()
	tenant.Serve(
		recorder,
		httptest.NewRequest(http.MethodGet, "http://example.com/", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("request status = %d, want 200", recorder.Code)
	}
	if generation.Active() != 0 || generation.Retired() {
		t.Fatal("completed request did not release its active generation lease")
	}

	tenant.Close()
	if !generation.Retired() {
		t.Fatal("tenant close did not retire its site generation")
	}
}

func (r *requestCapabilityResource) Close() error {
	r.closed.Add(1)
	return nil
}

func TestKitWorkResolvesCapabilitiesByRuntimeOwner(t *testing.T) {
	tenant := NewTenant(t.TempDir(), "example.com")
	t.Cleanup(tenant.Close)

	const requestName = "__work_test_request_owner"
	const appName = "__work_test_app_owner"
	const siteName = "__work_test_site_owner"
	var requestFactories atomic.Int32
	var appFactoryScope capabilities.Scope
	var siteFactoryScope capabilities.Scope

	capabilities.DefaultRegistry.RegisterWithLifetime(
		requestName,
		capabilities.LifetimeRequest,
		func(scope capabilities.Scope) value.Value {
			return value.New(&requestCapabilityResource{id: requestFactories.Add(1)})
		},
	)
	capabilities.DefaultRegistry.RegisterWithLifetime(
		appName,
		capabilities.LifetimeApp,
		func(scope capabilities.Scope) value.Value {
			appFactoryScope = scope
			return value.New(&struct{ ID string }{ID: "app"})
		},
	)
	capabilities.DefaultRegistry.Register(
		siteName,
		func(scope capabilities.Scope) value.Value {
			siteFactoryScope = scope
			return value.New(&struct{ ID string }{ID: "site"})
		},
	)

	firstScope := requestscope.New(
		tenant,
		httptest.NewRecorder(),
		httptest.NewRequest("GET", "http://example.com/", nil),
	)
	firstKitWork := &KitWork{tenant: tenant, requestScope: firstScope}
	first := firstKitWork.Capability(requestName)
	firstAgain := firstKitWork.Capability(requestName)
	if first.V != firstAgain.V {
		t.Fatal("one request did not reuse its request-scoped capability")
	}

	secondScope := requestscope.New(
		tenant,
		httptest.NewRecorder(),
		httptest.NewRequest("GET", "http://example.com/", nil),
	)
	secondKitWork := &KitWork{tenant: tenant, requestScope: secondScope}
	second := secondKitWork.Capability(requestName)
	if first.V == second.V || requestFactories.Load() != 2 {
		t.Fatal("separate requests shared a request-scoped capability")
	}

	firstKitWork.Capability(appName)
	firstKitWork.Capability(siteName)
	if appFactoryScope != tenant || siteFactoryScope != tenant {
		t.Fatal("app/site factory captured the shorter request scope")
	}

	firstResource := first.V.(*requestCapabilityResource)
	firstScope.Close()
	if firstResource.closed.Load() != 1 {
		t.Fatal("request capability did not close with its request")
	}
	if got := firstKitWork.Capability(requestName); got.K != value.Nil {
		t.Fatal("closed request recreated a request-scoped capability")
	}
	secondScope.Close()
}
