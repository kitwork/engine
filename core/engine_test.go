package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEngineHotReloadAndFallback(t *testing.T) {
	// Setup temporary directory
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Write v1 of app.kitwork.js
	v1Code := `
kitwork().Router().Get("/test").Handle(() => {
    return "v1";
});
`
	appFile := filepath.Join(tenantDir, "app.kitwork.js")
	err = os.WriteFile(appFile, []byte(v1Code), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize Engine with HotReload = true
	engine := New(tmpDir, 0, true, "")

	// Send request for v1
	req1 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr1.Code, rr1.Body.String())
	}
	if !strings.Contains(rr1.Body.String(), "v1") {
		t.Errorf("expected body to contain v1, got %s", rr1.Body.String())
	}

	// 2. Write v2 of app.kitwork.js and set ModTime to be 5 seconds in the future to trigger reload check
	v2Code := `
kitwork().Router().Get("/test").Handle(() => {
    return "v2";
});
`
	err = os.WriteFile(appFile, []byte(v2Code), 0644)
	if err != nil {
		t.Fatal(err)
	}
	futureTime := time.Now().Add(5 * time.Second)
	err = os.Chtimes(appFile, futureTime, futureTime)
	if err != nil {
		t.Fatal(err)
	}

	// Wait slightly for the throttle limit (1 second) to pass
	time.Sleep(1100 * time.Millisecond)

	// Send request for v2 (triggers reload check)
	req2 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "v2") {
		t.Errorf("expected body to contain v2, got %s", rr2.Body.String())
	}

	// 3. Write invalid code (syntax error) and verify compile fallback
	invalidCode := `
kitwork().Router().Get("/test", () => {
    return "v3"
` // Missing closing brace/paren
	err = os.WriteFile(appFile, []byte(invalidCode), 0644)
	if err != nil {
		t.Fatal(err)
	}
	futureTime2 := futureTime.Add(5 * time.Second)
	err = os.Chtimes(appFile, futureTime2, futureTime2)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)

	// Send request after writing invalid code
	req3 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr3 := httptest.NewRecorder()
	engine.ServeHTTP(rr3, req3)

	// System should NOT crash and should fallback to v2!
	if rr3.Code != http.StatusOK {
		t.Fatalf("expected status 200 (fallback to v2), got %d. Body: %s", rr3.Code, rr3.Body.String())
	}
	if !strings.Contains(rr3.Body.String(), "v2") {
		t.Errorf("expected body to fall back and contain v2, got %s", rr3.Body.String())
	}

	// 4. Test directory deletion
	err = os.Remove(appFile)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)

	// Send request after deleting file
	req4 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr4 := httptest.NewRecorder()
	engine.ServeHTTP(rr4, req4)

	// Should return 404 since tenant was evicted from cache
	if rr4.Code != http.StatusNotFound {
		t.Errorf("expected status 404 after deletion, got %d", rr4.Code)
	}
}

func TestEngineHotReloadDisabled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	v1Code := `
kitwork().Router().Get("/test").Handle(() => {
    return "v1";
});
`
	appFile := filepath.Join(tenantDir, "app.kitwork.js")
	err = os.WriteFile(appFile, []byte(v1Code), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Initialize Engine with HotReload = false
	engine := New(tmpDir, 0, false, "")

	req1 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, req1)

	if !strings.Contains(rr1.Body.String(), "v1") {
		t.Fatalf("expected v1, got %s", rr1.Body.String())
	}

	// Write v2 of app.kitwork.js
	v2Code := `
kitwork().Router().Get("/test").Handle(() => {
    return "v2";
});
`
	err = os.WriteFile(appFile, []byte(v2Code), 0644)
	if err != nil {
		t.Fatal(err)
	}
	futureTime := time.Now().Add(5 * time.Second)
	err = os.Chtimes(appFile, futureTime, futureTime)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)

	req2 := httptest.NewRequest("GET", "http://localhost/test", nil)
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, req2)

	// Since HotReload is false, it should STILL return v1
	if !strings.Contains(rr2.Body.String(), "v1") {
		t.Errorf("expected v1 (cached), got %s", rr2.Body.String())
	}
}

func TestEngineRateLimit(t *testing.T) {
	// Setup temporary directory
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appCode := `
kitwork().Router().Get("/test").Handle(() => {
    return "ok";
});
`
	appFile := filepath.Join(tenantDir, "app.kitwork.js")
	err = os.WriteFile(appFile, []byte(appCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Create Engine with a Global Limit of 5 and Per-IP Limit of 2
	engine := New(tmpDir, 0, false, "")

	// 2. IP 1 makes 2 requests (allowed)
	r1 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r1.RemoteAddr = "1.1.1.1:1234"

	rr1_1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1_1, r1)
	if rr1_1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr1_1.Code)
	}

	rr1_2 := httptest.NewRecorder()
	engine.ServeHTTP(rr1_2, r1)
	if rr1_2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr1_2.Code)
	}

	// IP 1 third request is blocked by IP limit of 2 (should be 429 and rollback global)
	rr1_3 := httptest.NewRecorder()
	engine.ServeHTTP(rr1_3, r1)
	if rr1_3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr1_3.Code)
	}

	// 3. IP 2 makes 2 requests (allowed because IP 2 has separate budget)
	r2 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r2.RemoteAddr = "2.2.2.2:1234"

	rr2_1 := httptest.NewRecorder()
	engine.ServeHTTP(rr2_1, r2)
	if rr2_1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr2_1.Code)
	}

	rr2_2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2_2, r2)
	if rr2_2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr2_2.Code)
	}

	// 4. IP 3 makes 1 request (allowed, total active system requests = 2 + 2 + 1 = 5)
	r3 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r3.RemoteAddr = "3.3.3.3:1234"

	rr3_1 := httptest.NewRecorder()
	engine.ServeHTTP(rr3_1, r3)
	if rr3_1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr3_1.Code)
	}

	// 5. IP 4 makes 1 request (blocked, exceeds global system budget of 5)
	r4 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r4.RemoteAddr = "4.4.4.4:1234"

	rr4_1 := httptest.NewRecorder()
	engine.ServeHTTP(rr4_1, r4)
	if rr4_1.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 (global block), got %d", rr4_1.Code)
	}
}

func TestEngineBrowserRateLimit(t *testing.T) {
	// Setup temporary directory
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-b-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	err = os.MkdirAll(tenantDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	appCode := `
kitwork().Router().Get("/test").Handle(() => {
    return "ok";
});
`
	appFile := filepath.Join(tenantDir, "app.kitwork.js")
	err = os.WriteFile(appFile, []byte(appCode), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Create Engine with BrowserLimit=2
	engine := New(tmpDir, 0, false, "")

	// 2. Test Browser limit (IP rotations)
	rBrowser := httptest.NewRequest("GET", "http://localhost/test", nil)
	rBrowser.Header.Set("User-Agent", "MaliciousBrowser")
	rBrowser.Header.Set("Accept-Language", "en")

	// Request 1: Proxy IP A (Allowed)
	rBrowser.RemoteAddr = "1.1.1.1:1234"
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, rBrowser)
	if rr1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr1.Code)
	}

	// Request 2: Proxy IP B (Allowed)
	rBrowser.RemoteAddr = "2.2.2.2:1234"
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, rBrowser)
	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr2.Code)
	}

	// Request 3: Proxy IP C (Blocked by browser limit, despite rotating IP!)
	rBrowser.RemoteAddr = "3.3.3.3:1234"
	rr3 := httptest.NewRecorder()
	engine.ServeHTTP(rr3, rBrowser)
	if rr3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr3.Code)
	}
}
