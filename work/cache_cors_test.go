package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestCacheAndCors(t *testing.T) {
	// 1. Setup temporary tenant workspace
	tmpDir, err := os.MkdirTemp("", "kitwork-cache-cors-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	fallbackRunCount := 0
	mu := sync.Mutex{}

	TestNotifyHook = func(name string, args ...value.Value) {
		if name == "fallback_run" {
			mu.Lock()
			fallbackRunCount++
			mu.Unlock()
		}
	}
	defer func() { TestNotifyHook = nil }()

	script := `
import { router, cache, log } from "kitwork";

// Configure route with CORS
router.get("/api/data")
    .cors({
        origin: "https://kitwork.io",
        methods: ["GET", "POST"],
        maxAge: 3600
    })
    .handle((request, response) => {
        // Test Cache Get and Fallback
        const result = cache.get("db_settings", () => {
            testNotify("fallback_run");
            return "dynamic_config_value";
        }, "5s");

        return { value: result };
    });
`

	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("tenant run failed: %v", err)
	}
	defer tenant.Close()

	// 2. Test CORS Preflight request (OPTIONS)
	reqPreflight := httptest.NewRequest("OPTIONS", "/api/data", nil)
	reqPreflight.Header.Set("Origin", "https://kitwork.io")
	recPreflight := httptest.NewRecorder()

	tenant.Serve(recPreflight, reqPreflight)

	resPreflight := recPreflight.Result()
	if resPreflight.StatusCode != http.StatusNoContent {
		t.Errorf("expected preflight response status 204, got %d", resPreflight.StatusCode)
	}
	if origin := resPreflight.Header.Get("Access-Control-Allow-Origin"); origin != "https://kitwork.io" {
		t.Errorf("expected Access-Control-Allow-Origin to be 'https://kitwork.io', got '%s'", origin)
	}
	if maxAge := resPreflight.Header.Get("Access-Control-Max-Age"); maxAge != "3600" {
		t.Errorf("expected Access-Control-Max-Age to be '3600', got '%s'", maxAge)
	}

	// 3. Test CORS Headers on regular GET request & Cache fallback execution
	reqGet1 := httptest.NewRequest("GET", "/api/data", nil)
	reqGet1.Header.Set("Origin", "https://kitwork.io")
	recGet1 := httptest.NewRecorder()

	tenant.Serve(recGet1, reqGet1)

	resGet1 := recGet1.Result()
	if resGet1.StatusCode != http.StatusOK {
		t.Errorf("expected GET status 200, got %d", resGet1.StatusCode)
	}
	if origin := resGet1.Header.Get("Access-Control-Allow-Origin"); origin != "https://kitwork.io" {
		t.Errorf("expected Access-Control-Allow-Origin on GET to be 'https://kitwork.io', got '%s'", origin)
	}

	mu.Lock()
	count1 := fallbackRunCount
	mu.Unlock()
	if count1 != 1 {
		t.Errorf("expected fallback function to run 1 time, ran %d times", count1)
	}

	// 4. Test Cache Hit on second GET request (fallback should NOT run again)
	reqGet2 := httptest.NewRequest("GET", "/api/data", nil)
	reqGet2.Header.Set("Origin", "https://kitwork.io")
	recGet2 := httptest.NewRecorder()

	tenant.Serve(recGet2, reqGet2)

	resGet2 := recGet2.Result()
	if resGet2.StatusCode != http.StatusOK {
		t.Errorf("expected second GET status 200, got %d", resGet2.StatusCode)
	}

	mu.Lock()
	count2 := fallbackRunCount
	mu.Unlock()
	if count2 != 1 {
		t.Errorf("expected fallback function to still have run only 1 time (cache hit), ran %d times", count2)
	}
}
