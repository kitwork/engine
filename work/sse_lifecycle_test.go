package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSSEReleasesVMAndTenantCloseDrainsStream(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	routerScript := `
import { router } from "kitwork";

router.get((ctx) => {
    ctx.sse.connect({ channel: "updates" });
});
`
	if err := os.WriteFile(filepath.Join(appDir, RouterFileName), []byte(routerScript), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(tenant.Serve))
	defer server.Close()

	baseline := enginePool.Active()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if got := enginePool.Active(); got != baseline {
		t.Fatalf("open SSE stream retained a request VM: active=%d baseline=%d", got, baseline)
	}

	closed := make(chan struct{})
	go func() {
		tenant.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Tenant.Close did not stop and drain the active SSE stream")
	}
}
