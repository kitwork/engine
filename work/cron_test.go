package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestCronAndGlobalFetch(t *testing.T) {
	// 1. Setup a mockup HTTP server for testing fetch
	serverCallCount := 0
	serverMu := sync.Mutex{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverMu.Lock()
		serverCallCount++
		serverMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success": true, "message": "hello from test server"}`))
	}))
	defer ts.Close()

	// 2. Setup temporary tenant workspace
	tmpDir, err := os.MkdirTemp("", "kitwork-cron-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	cronRunCount := 0
	cronMu := sync.Mutex{}

	// Register test notification hook
	TestNotifyHook = func(name string, args ...value.Value) {
		if name == "cron_tick" {
			cronMu.Lock()
			cronRunCount++
			cronMu.Unlock()
		}
	}
	defer func() { TestNotifyHook = nil }()

	script := fmt.Sprintf(`
import { cron, log } from "kitwork";

// Test 1: Global fetch
const res = fetch("%s", {
    method: "POST",
    headers: { "X-Test": "123" },
    timeout: 1000
});

log.Print("FETCH_STATUS: " + res.status);
log.Print("FETCH_OK: " + res.ok);
const data = res.json();
log.Print("FETCH_MSG: " + data.message);

if (res.status != 200) fail("fetch status not 200");
if (!res.ok) fail("fetch ok flag failed");
if (data.message != "hello from test server") fail("fetch response body parsing failed");

// Test 2: Cron schedule
cron.Every("10ms", () => {
    log.Print("Cron ticked!");
    testNotify("cron_tick");
});
`, ts.URL)

	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}

	// Create and run the tenant
	tenant := NewTenant(tmpDir, "localhost")
	AllowLocal = true

	if err := tenant.Run(); err != nil {
		t.Fatalf("tenant run failed: %v", err)
	}
	defer tenant.Close()

	// Wait up to 150ms for cron ticks to happen
	time.Sleep(150 * time.Millisecond)

	cronMu.Lock()
	ticks := cronRunCount
	cronMu.Unlock()

	if ticks == 0 {
		t.Error("expected cron job to tick, but run count was 0")
	} else {
		t.Logf("cron ticked successfully: %d times", ticks)
	}

	serverMu.Lock()
	serverHits := serverCallCount
	serverMu.Unlock()

	if serverHits != 1 {
		t.Errorf("expected 1 hit on the test server from fetch, got %d", serverHits)
	}
}
