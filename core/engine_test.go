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

// writeTreeTenant lays a minimal FILESYSTEM-ROUTED tenant on disk (root/test/localhost) whose root
// router answers GET / with the given body. Returns the router file path (the tenant marker hot
// reload watches). The flat app.kitwork.js model is gone — every engine test drives the tree.
func writeTreeTenant(t *testing.T, tmpDir, body string) string {
	t.Helper()
	dir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	routerFile := filepath.Join(dir, "router.kitwork.js")
	writeRouterBody(t, routerFile, body)
	return routerFile
}

func writeRouterBody(t *testing.T, routerFile, body string) {
	t.Helper()
	code := "import { router } from \"kitwork\";\n" +
		"router.get().handle((ctx) => ctx.text(\"" + body + "\"));\n"
	if err := os.WriteFile(routerFile, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEngineHotReloadAndFallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	routerFile := writeTreeTenant(t, tmpDir, "v1")

	// Initialize Engine with HotReload = true
	engine := New(tmpDir, 0, true, "")

	req1 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr1.Code, rr1.Body.String())
	}
	if !strings.Contains(rr1.Body.String(), "v1") {
		t.Errorf("expected body to contain v1, got %s", rr1.Body.String())
	}

	// 2. Rewrite the root router (the watched marker) as v2, ModTime in the future so the 1s
	// hot-reload throttle sees a change.
	writeRouterBody(t, routerFile, "v2")
	futureTime := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(routerFile, futureTime, futureTime); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req2 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "v2") {
		t.Errorf("expected body to contain v2, got %s", rr2.Body.String())
	}

	// 3. Broken syntax: under the tree model folder routers compile LAZILY per request, so there is
	// no reload-time "fallback to the old version" (that was a flat-era semantic) — a folder whose
	// router fails to compile is an EMPTY folder: fail-visible 404, never a crash.
	if err := os.WriteFile(routerFile, []byte("import { router } from \"kitwork\";\nrouter.get().handle((ctx => {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	futureTime2 := futureTime.Add(5 * time.Second)
	if err := os.Chtimes(routerFile, futureTime2, futureTime2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req3 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr3 := httptest.NewRecorder()
	engine.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNotFound {
		t.Errorf("broken router should serve fail-visible 404 (empty folder), got %d. Body: %s", rr3.Code, rr3.Body.String())
	}

	// 4. Deleting the root router (the tenant marker) evicts the tenant from the cache → 404.
	if err := os.Remove(routerFile); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req4 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr4 := httptest.NewRecorder()
	engine.ServeHTTP(rr4, req4)
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

	routerFile := writeTreeTenant(t, tmpDir, "v1")

	// Initialize Engine with HotReload = false
	engine := New(tmpDir, 0, false, "")

	req1 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr1 := httptest.NewRecorder()
	engine.ServeHTTP(rr1, req1)
	if !strings.Contains(rr1.Body.String(), "v1") {
		t.Fatalf("expected v1, got %s", rr1.Body.String())
	}

	// Rewrite as v2 — with hot reload off, the cached tenant (and its compiled folder) must keep
	// serving v1.
	writeRouterBody(t, routerFile, "v2")
	futureTime := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(routerFile, futureTime, futureTime); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	req2 := httptest.NewRequest("GET", "http://localhost/", nil)
	rr2 := httptest.NewRecorder()
	engine.ServeHTTP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), "v1") {
		t.Errorf("expected v1 (cached), got %s", rr2.Body.String())
	}
}

func TestEngineRateLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeTreeTenant(t, tmpDir, "ok")

	// Global budget of 5 per window, per-IP budget of 2. Window = 1 minute so nothing refills
	// mid-test. Public test IPs — loopback/private would bypass the limiter.
	engine := New(tmpDir, 0, false, "")
	engine.SetRateLimit(&RateLimiter{Rate: 5, IPRate: 2, Period: time.Minute})

	send := func(ip string) int {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = ip + ":1234"
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, r)
		return rr.Code
	}

	// IP 1: two allowed, third blocked by the per-IP budget — and the global token it took must
	// be ROLLED BACK (a blocked request never burns global budget).
	if c := send("1.1.1.1"); c != http.StatusOK {
		t.Errorf("ip1 #1: expected 200, got %d", c)
	}
	if c := send("1.1.1.1"); c != http.StatusOK {
		t.Errorf("ip1 #2: expected 200, got %d", c)
	}
	if c := send("1.1.1.1"); c != http.StatusTooManyRequests {
		t.Errorf("ip1 #3: expected 429 (per-IP), got %d", c)
	}

	// IP 2 has its own budget: two more allowed (global now 4/5).
	if c := send("2.2.2.2"); c != http.StatusOK {
		t.Errorf("ip2 #1: expected 200, got %d", c)
	}
	if c := send("2.2.2.2"); c != http.StatusOK {
		t.Errorf("ip2 #2: expected 200, got %d", c)
	}

	// IP 3 takes the 5th and last global token — proof the rollback above worked.
	if c := send("3.3.3.3"); c != http.StatusOK {
		t.Errorf("ip3 #1: expected 200, got %d", c)
	}

	// IP 4 is refused by the exhausted GLOBAL bucket despite a fresh per-IP budget.
	if c := send("4.4.4.4"); c != http.StatusTooManyRequests {
		t.Errorf("ip4 #1: expected 429 (global), got %d", c)
	}
}

func TestEngineBrowserRateLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-b-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeTreeTenant(t, tmpDir, "ok")

	// Browser fingerprint budget of 2 — catches one client rotating proxy IPs.
	engine := New(tmpDir, 0, false, "")
	engine.SetRateLimit(&RateLimiter{BrowserRate: 2, Period: time.Minute})

	send := func(ip string) int {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = ip + ":1234"
		r.Header.Set("User-Agent", "MaliciousBrowser")
		r.Header.Set("Accept-Language", "en")
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, r)
		return rr.Code
	}

	if c := send("1.1.1.1"); c != http.StatusOK {
		t.Errorf("proxy A: expected 200, got %d", c)
	}
	if c := send("2.2.2.2"); c != http.StatusOK {
		t.Errorf("proxy B: expected 200, got %d", c)
	}
	// Third request: new IP, SAME browser fingerprint → blocked.
	if c := send("3.3.3.3"); c != http.StatusTooManyRequests {
		t.Errorf("proxy C: expected 429 (browser fingerprint), got %d", c)
	}
}

func TestEngineRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-engine-rl-xff-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	writeTreeTenant(t, tmpDir, "ok")

	engine := New(tmpDir, 0, false, "")
	engine.SetRateLimit(&RateLimiter{IPRate: 2, Period: time.Minute})

	// Same real connection, rotating FAKE X-Forwarded-For each time. Kitwork is the edge server:
	// the header is client-supplied and must be IGNORED (work.TrustProxyHeaders default false) —
	// otherwise the per-IP budget resets on every spoofed value.
	send := func(fakeIP string) int {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = "9.9.9.9:1234"
		r.Header.Set("X-Forwarded-For", fakeIP)
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, r)
		return rr.Code
	}

	if c := send("1.2.3.4"); c != http.StatusOK {
		t.Errorf("#1: expected 200, got %d", c)
	}
	if c := send("5.6.7.8"); c != http.StatusOK {
		t.Errorf("#2: expected 200, got %d", c)
	}
	if c := send("10.11.12.13"); c != http.StatusTooManyRequests {
		t.Errorf("#3: spoofed X-Forwarded-For must NOT bypass the per-IP limit, got %d", c)
	}
}
