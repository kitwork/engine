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
	engine := New(tmpDir, 0, true)

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
	engine := New(tmpDir, 0, false)

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
