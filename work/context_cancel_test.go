package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHTTPRequestContextCancellation verifies that when an HTTP request's context
// is cancelled mid-execution, the VM halts execution immediately.
func TestHTTPRequestContextCancellation(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	routeDir := filepath.Join(appDir, "spin")
	if err := os.MkdirAll(routeDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router } from "kitwork";

var loop = (n) => {
    return loop(n + 1);
};

router.get((ctx) => {
    loop(0);
    return ctx.text("done");
});
`
	if err := os.WriteFile(filepath.Join(routeDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://localhost/spin", nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		tenant.Serve(rec, req)
		close(done)
	}()

	select {
	case <-done:
		if strings.Contains(rec.Body.String(), "done") {
			t.Fatal("Request completed instead of being cancelled")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Tenant.Serve did not halt within 1s after context cancellation")
	}
}
