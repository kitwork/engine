package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is a BOUNDED counting loop, deliberately: it completes normally on its own, so a halt
// can only come from cancellation. An earlier version of this test used infinite recursion, which
// died of "Stack overflow" in ~16ms — before the 20ms cancel() even fired — so it passed whether or
// not cancellation worked at all. STABILITY.md §5: a test must prove the mechanism it names.
const cancelFixtureRouter = `
import { router } from "kitwork";

router.get((ctx) => {
    let total = 0;
    for (let i = 0; i < 500; i++) {
        total = total + i;
    }
    return ctx.text("done:" + total);
});
`

func newCancelFixtureTenant(t *testing.T) *Tenant {
	t.Helper()
	tmp := t.TempDir()
	routeDir := filepath.Join(tmp, "acme", "localhost", "spin")
	if err := os.MkdirAll(routeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(routeDir, "router.kitwork.js"), []byte(cancelFixtureRouter), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	return tenant
}

func serveCancelFixture(t *testing.T, tenant *Tenant, ctx context.Context) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/spin", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	return rec.Body.String()
}

// CONTROL: without cancellation the fixture must run to completion. This is what makes the
// cancellation assertion below meaningful — it proves the halt is not caused by the energy
// ceiling, a stack limit, or a broken fixture.
func TestRequestRunsToCompletionWithoutCancellation(t *testing.T) {
	tenant := newCancelFixtureTenant(t)
	body := serveCancelFixture(t, tenant, context.Background())

	if !strings.Contains(body, "done") {
		t.Fatalf("fixture must complete normally when not cancelled; got: %q", body)
	}
}

// Cancelling the request context halts the HANDLER. Route handlers are lambdas executed by
// runtime.VM.ExecuteLambda (see serveTree), which is a different dispatch loop from VM.Run — the
// cancellation probe has to exist in BOTH or this path keeps running after the client is gone.
// The context is cancelled up front so the test is deterministic, with no sleep-and-hope race.
func TestHTTPRequestContextCancellation(t *testing.T) {
	tenant := newCancelFixtureTenant(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	body := serveCancelFixture(t, tenant, ctx)

	if strings.Contains(body, "done") {
		t.Fatalf("cancelled request ran to completion: %q", body)
	}
	// Assert the REASON, not merely that something stopped.
	if !strings.Contains(body, "Cancelled") {
		t.Fatalf("expected the handler to halt with a cancellation error, got: %q", body)
	}
}
