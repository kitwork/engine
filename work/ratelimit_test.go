package work

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestParseLimitStr(t *testing.T) {
	tests := []struct {
		input     string
		wantRate  int
		wantDur   time.Duration
		expectErr bool
	}{
		{"10/s", 10, time.Second, false},
		{"100/1m", 100, time.Minute, false},
		{"500/5s", 500, 5 * time.Second, false},
		{"5/minute", 5, time.Minute, false},
		{"1/hour", 1, time.Hour, false},
		{"2/hr", 2, time.Hour, false},
		{"invalid", 0, 0, true},
		{"10/", 0, 0, true},
		{"/s", 0, 0, true},
	}

	for _, tt := range tests {
		rate, dur, err := parseLimitStr(tt.input)
		if tt.expectErr {
			if err == nil {
				t.Errorf("expected error for %q, but got none", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error for %q: %v", tt.input, err)
			}
			if rate != tt.wantRate {
				t.Errorf("expected rate %d for %q, got %d", tt.wantRate, tt.input, rate)
			}
			if dur != tt.wantDur {
				t.Errorf("expected duration %v for %q, got %v", tt.wantDur, tt.input, dur)
			}
		}
	}
}

func TestRateLimiterAllow(t *testing.T) {
	lim := NewRateLimiter(2, time.Second)

	// Consume 2 tokens
	if !lim.Allow(2, time.Second) {
		t.Errorf("expected allow for first token")
	}
	if !lim.Allow(2, time.Second) {
		t.Errorf("expected allow for second token")
	}

	// Third token should be denied
	if lim.Allow(2, time.Second) {
		t.Errorf("expected deny for third token")
	}

	// Wait 500ms -> should refill ~1 token
	time.Sleep(550 * time.Millisecond)
	if !lim.Allow(2, time.Second) {
		t.Errorf("expected allow after partial refill")
	}
	if lim.Allow(2, time.Second) {
		t.Errorf("expected deny after exhausting refilled token")
	}
}

func TestTenantCheckRateLimit(t *testing.T) {
	tenant := &Tenant{}

	// Route limit: 3 requests
	matchedRoute := &Router{
		limitRules: []rateRule{{Type: "ip", Rate: 3, Period: time.Second}},
		Method:     "GET",
		Path:       "/test",
	}

	r1 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r1.RemoteAddr = "1.1.1.1:1234"

	if !tenant.checkRateLimit(matchedRoute, r1, httptest.NewRecorder()) {
		t.Errorf("expected request 1 to be allowed")
	}
	if !tenant.checkRateLimit(matchedRoute, r1, httptest.NewRecorder()) {
		t.Errorf("expected request 2 to be allowed")
	}
	if !tenant.checkRateLimit(matchedRoute, r1, httptest.NewRecorder()) {
		t.Errorf("expected request 3 to be allowed")
	}

	wBlocked := httptest.NewRecorder()
	if tenant.checkRateLimit(matchedRoute, r1, wBlocked) {
		t.Errorf("expected request 4 to be rate limited")
	}

	if wBlocked.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", wBlocked.Code)
	}

	// Test Route-level API Limit configuration in JavaScript. Each .Limit() REPLACES the rules;
	// the legacy/single-map forms each produce exactly one "ip" rule.
	router := &Router{tenant: tenant}
	one := func(label string, wantRate int, wantPeriod time.Duration) {
		if len(router.limitRules) != 1 || router.limitRules[0].Type != "ip" ||
			router.limitRules[0].Rate != wantRate || router.limitRules[0].Period != wantPeriod {
			t.Errorf("%s: got %+v, want one ip rule %d/%v", label, router.limitRules, wantRate, wantPeriod)
		}
	}

	router.Limit(value.New("5/s"))
	one("string", 5, time.Second)

	router.Limit(value.New(map[string]any{"rate": 15, "period": "2s"}))
	one("map period", 15, 2*time.Second)

	router.Limit(value.New(map[string]any{"rate": 10, "second": 1}))
	one("map second", 10, time.Second)

	router.Limit(value.New(map[string]any{"rate": 100, "minute": 1}))
	one("map minute", 100, time.Minute)

	router.Limit(value.New(25), value.New("5s"))
	one("multi string", 25, 5*time.Second)

	router.Limit(value.New(30), value.New(2))
	one("multi number", 30, 2*time.Second)

	router.Limit(value.New(40), value.New(time.Minute))
	one("multi duration", 40, time.Minute)
}

func TestTenantLevelRateLimit(t *testing.T) {
	// Initialize tenant with a tenant-wide global limit of 3 and per-IP limit of 2
	tenant := &Tenant{
		rateLimitRules: []rateRule{
			{Type: "global", Rate: 3, Period: time.Second},
			{Type: "ip", Rate: 2, Period: time.Second},
		},
	}

	// No route-specific limits configured
	matchedRoute := &Router{}

	// Client IP 1
	r1 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r1.RemoteAddr = "1.1.1.1:1234"

	// IP 1: Request 1 (Allowed)
	if !tenant.checkRateLimit(matchedRoute, r1, httptest.NewRecorder()) {
		t.Errorf("expected IP 1 request 1 to be allowed")
	}

	// IP 1: Request 2 (Allowed)
	if !tenant.checkRateLimit(matchedRoute, r1, httptest.NewRecorder()) {
		t.Errorf("expected IP 1 request 2 to be allowed")
	}

	// IP 1: Request 3 (Blocked by IP limit of 2)
	wIPBlocked := httptest.NewRecorder()
	if tenant.checkRateLimit(matchedRoute, r1, wIPBlocked) {
		t.Errorf("expected IP 1 request 3 to be blocked by IP limit")
	}
	if wIPBlocked.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", wIPBlocked.Code)
	}

	// Client IP 2
	r2 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r2.RemoteAddr = "2.2.2.2:1234"

	// IP 2: Request 1 (Allowed, total global = 1+1+1 = 3)
	if !tenant.checkRateLimit(matchedRoute, r2, httptest.NewRecorder()) {
		t.Errorf("expected IP 2 request 1 to be allowed")
	}

	// IP 2: Request 2 (Blocked by global limit of 3)
	wGlobalBlocked := httptest.NewRecorder()
	if tenant.checkRateLimit(matchedRoute, r2, wGlobalBlocked) {
		t.Errorf("expected IP 2 request 2 to be blocked by global tenant limit")
	}
	if wGlobalBlocked.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", wGlobalBlocked.Code)
	}
}

func TestTenantRateLimitMapRotation(t *testing.T) {
	tenant := &Tenant{}

	matchedRoute := &Router{
		limitRules: []rateRule{{Type: "ip", Rate: 1, Period: time.Second}},
		Method:     "GET",
		Path:       "/test",
	}

	r := httptest.NewRequest("GET", "http://localhost/test", nil)
	r.RemoteAddr = "1.2.3.4:1234"

	// Request 1: Allowed
	if !tenant.checkRateLimit(matchedRoute, r, httptest.NewRecorder()) {
		t.Errorf("expected request 1 to be allowed")
	}

	// Request 2: Blocked (rate is 1/s)
	if tenant.checkRateLimit(matchedRoute, r, httptest.NewRecorder()) {
		t.Errorf("expected request 2 to be blocked")
	}

	// Force map rotation by setting lastRotation to 2 seconds ago
	tenant.limiters[ScopeTenant].lastRotation = time.Now().Add(-2 * time.Second)

	// Request 3: Still blocked, but state carries over from previous map
	if tenant.checkRateLimit(matchedRoute, r, httptest.NewRecorder()) {
		t.Errorf("expected request 3 to be blocked (carried over)")
	}

	// Wait for limitPeriod to expire (1 second)
	time.Sleep(1100 * time.Millisecond)

	// Request 4: Allowed (tokens refilled)
	if !tenant.checkRateLimit(matchedRoute, r, httptest.NewRecorder()) {
		t.Errorf("expected request 4 to be allowed after token refill")
	}
}

func TestTenantUserRateLimit(t *testing.T) {
	tenant := &Tenant{
		rateLimitRules: []rateRule{
			{Type: "user", Rate: 2, Period: time.Second},
		},
	}

	matchedRoute := &Router{}

	// Request from User A
	r1 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r1.RemoteAddr = "1.1.1.1:1234"
	r1.Header.Set("Authorization", "Bearer UserTokenXYZ")

	if !tenant.checkRateLimit(matchedRoute, r1, httptest.NewRecorder()) {
		t.Errorf("expected request 1 to be allowed")
	}

	// Request 2 from same User A (different IP and browser)
	r2 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r2.RemoteAddr = "2.2.2.2:1234"
	r2.Header.Set("User-Agent", "MobileAgent")
	r2.Header.Set("Authorization", "Bearer UserTokenXYZ")

	if !tenant.checkRateLimit(matchedRoute, r2, httptest.NewRecorder()) {
		t.Errorf("expected request 2 to be allowed")
	}

	// Request 3 from same User A
	r3 := httptest.NewRequest("GET", "http://localhost/test", nil)
	r3.RemoteAddr = "3.3.3.3:1234"
	r3.Header.Set("Authorization", "Bearer UserTokenXYZ")

	wBlocked := httptest.NewRecorder()
	if tenant.checkRateLimit(matchedRoute, r3, wBlocked) {
		t.Errorf("expected request 3 to be blocked by User Account Limit")
	}
	if wBlocked.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", wBlocked.Code)
	}
}

func TestTenantUserRateLimitDynamic(t *testing.T) {
	tenant := &Tenant{
		rateLimitRules: []rateRule{
			{Type: "user", Rate: 2, Period: time.Second},
		},
	}

	matchedRoute := &Router{}

	// 1. Create a mock JWT with a custom limit of 4
	claimJSON := `{"user_rate": 4}`
	payloadEnc := base64.RawURLEncoding.EncodeToString([]byte(claimJSON))
	mockToken := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + payloadEnc + ".SignaturePart"

	r := httptest.NewRequest("GET", "http://localhost/test", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	r.Header.Set("Authorization", "Bearer "+mockToken)

	// User should get 4 requests instead of the default 2!
	for i := 1; i <= 4; i++ {
		if !tenant.checkRateLimit(matchedRoute, r, httptest.NewRecorder()) {
			t.Errorf("expected request %d to be allowed under custom JWT limit", i)
		}
	}

	// Request 5 should be blocked
	wBlocked := httptest.NewRecorder()
	if tenant.checkRateLimit(matchedRoute, r, wBlocked) {
		t.Errorf("expected request 5 to be blocked under custom JWT limit")
	}
	if wBlocked.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", wBlocked.Code)
	}
}

// New typed/array API: .limit([{ type, rate, period }, ...]).
func TestRouteLimitArrayParse(t *testing.T) {
	router := &Router{}
	router.Limit(value.New([]any{
		map[string]any{"type": "ip", "rate": 100, "second": 1},
		map[string]any{"type": "user", "rate": 10, "minute": 1},
		map[string]any{"type": "browser", "rate": 30, "period": "1s"},
	}))

	if len(router.limitRules) != 3 {
		t.Fatalf("expected 3 rules, got %d (%+v)", len(router.limitRules), router.limitRules)
	}
	want := []rateRule{
		{Type: "ip", Rate: 100, Period: time.Second, Scope: "tenant"},
		{Type: "user", Rate: 10, Period: time.Minute, Scope: "tenant"},
		{Type: "browser", Rate: 30, Period: time.Second, Scope: "tenant"},
	}
	for i, w := range want {
		if router.limitRules[i] != w {
			t.Errorf("rule %d: got %+v, want %+v", i, router.limitRules[i], w)
		}
	}

	// A single typed map → one rule of that type.
	router.Limit(value.New(map[string]any{"type": "user", "rate": 5, "second": 1}))
	if len(router.limitRules) != 1 || router.limitRules[0].Type != "user" || router.limitRules[0].Rate != 5 {
		t.Errorf("single typed map: got %+v", router.limitRules)
	}
}

// Multi-window: two IP rules with different periods are BOTH enforced — the tighter one bites.
func TestRouteLimitMultiWindow(t *testing.T) {
	tenant := &Tenant{}
	matched := &Router{
		limitRules: []rateRule{
			{Type: "ip", Rate: 2, Period: time.Second},    // burst: 2/s
			{Type: "ip", Rate: 100, Period: time.Minute},  // sustained: 100/min
		},
		Method: "GET",
		Path:   "/api",
	}
	r := httptest.NewRequest("GET", "http://localhost/api", nil)
	r.RemoteAddr = "9.9.9.9:1234"

	// First 2 allowed (within 2/s), 3rd blocked by the per-second rule even though /min is fine.
	if !tenant.checkRateLimit(matched, r, httptest.NewRecorder()) {
		t.Errorf("req 1 should pass")
	}
	if !tenant.checkRateLimit(matched, r, httptest.NewRecorder()) {
		t.Errorf("req 2 should pass")
	}
	w := httptest.NewRecorder()
	if tenant.checkRateLimit(matched, r, w) {
		t.Errorf("req 3 should be blocked by the 2/s window")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}

	// After the second refills, requests flow again (the /min rule was NOT wrongly consumed on
	// the blocked request — it was rolled back).
	time.Sleep(1100 * time.Millisecond)
	if !tenant.checkRateLimit(matched, r, httptest.NewRecorder()) {
		t.Errorf("req after refill should pass")
	}
}

// A "user" rule limits per account and is SKIPPED for anonymous requests.
func TestRouteLimitTypeUser(t *testing.T) {
	tenant := &Tenant{}
	matched := &Router{
		limitRules: []rateRule{{Type: "user", Rate: 1, Period: time.Second}},
		Method:     "GET",
		Path:       "/api",
	}

	// Authenticated user: 1/s → 2nd blocked.
	ru := httptest.NewRequest("GET", "http://localhost/api", nil)
	ru.RemoteAddr = "1.1.1.1:1"
	ru.Header.Set("Authorization", "Bearer userA")
	if !tenant.checkRateLimit(matched, ru, httptest.NewRecorder()) {
		t.Errorf("user req 1 should pass")
	}
	if tenant.checkRateLimit(matched, ru, httptest.NewRecorder()) {
		t.Errorf("user req 2 should be blocked by the user rule")
	}

	// Anonymous (no auth/session): the user rule does not apply → never blocked by it.
	ra := httptest.NewRequest("GET", "http://localhost/api", nil)
	ra.RemoteAddr = "2.2.2.2:1"
	for i := 0; i < 5; i++ {
		if !tenant.checkRateLimit(matched, ra, httptest.NewRecorder()) {
			t.Errorf("anonymous req %d should pass (user rule skipped)", i+1)
		}
	}
}

// scope:"server" puts the bucket in the SHARED host store, so the SAME route limit counts
// across DIFFERENT tenants (vs the default "tenant" scope, which is per-tenant).
func TestRouteLimitServerScope(t *testing.T) {
	host := NewLimiterStore(time.Second)
	mk := func() *Tenant {
		ten := &Tenant{
			limiters: make([]*LimiterStore, ScopeMax),
		}
		ten.limiters[ScopeTenant] = NewLimiterStore(time.Second)
		ten.limiters[ScopeServer] = host
		return ten
	}
	tenantA, tenantB := mk(), mk()

	// scope:"server" is parsed off the JS rule.
	rt := &Router{}
	rt.Limit(value.New([]any{map[string]any{"type": "ip", "rate": 2, "second": 1, "scope": "server"}}))
	if len(rt.limitRules) != 1 || rt.limitRules[0].Scope != "server" {
		t.Fatalf("scope not parsed: %+v", rt.limitRules)
	}
	rt.Method, rt.Path = "GET", "/x"

	r := httptest.NewRequest("GET", "http://localhost/x", nil)
	r.RemoteAddr = "5.5.5.5:1"

	// Same IP + same route, but TWO different tenants → one shared server bucket (2/s total).
	if !tenantA.checkRateLimit(rt, r, httptest.NewRecorder()) {
		t.Errorf("tenant A req 1 should pass")
	}
	if !tenantB.checkRateLimit(rt, r, httptest.NewRecorder()) {
		t.Errorf("tenant B req 1 should pass (2nd overall)")
	}
	w := httptest.NewRecorder()
	if tenantA.checkRateLimit(rt, r, w) {
		t.Errorf("3rd request across tenants should be blocked by the SHARED server bucket")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}

	// Contrast: a tenant-scoped rule is NOT shared — each tenant gets its own bucket.
	rtT := &Router{Method: "GET", Path: "/y", limitRules: []rateRule{{Type: "ip", Rate: 1, Period: time.Second, Scope: "tenant"}}}
	r2 := httptest.NewRequest("GET", "http://localhost/y", nil)
	r2.RemoteAddr = "6.6.6.6:1"
	if !tenantA.checkRateLimit(rtT, r2, httptest.NewRecorder()) {
		t.Errorf("tenant A /y req 1 should pass")
	}
	if !tenantB.checkRateLimit(rtT, r2, httptest.NewRecorder()) {
		t.Errorf("tenant B /y req 1 should pass — separate tenant bucket, not shared")
	}
}
