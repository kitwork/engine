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
	tenant := &Tenant{
		rateLimitEnabled: true,
		rateLimitPeriod:  time.Second,
		currentLimiters:  make(map[string]*RateLimiter),
		previousLimiters: make(map[string]*RateLimiter),
		lastRotation:     time.Now(),
	}

	// Route limit: 3 requests
	matchedRoute := &Router{
		hasLimit:    true,
		limitRate:   3,
		limitPeriod: time.Second,
		Method:      "GET",
		Path:        "/test",
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

	// Test Route-level API Limit configuration in JavaScript
	router := &Router{tenant: tenant}
	
	// Test String Case
	router.Limit(value.New("5/s"))
	if !router.hasLimit || router.limitRate != 5 || router.limitPeriod != time.Second {
		t.Errorf("expected string limit to be set, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}

	// Test Map Case
	mVal := value.New(map[string]any{
		"rate":   15,
		"period": "2s",
	})
	router.Limit(mVal)
	if !router.hasLimit || router.limitRate != 15 || router.limitPeriod != 2*time.Second {
		t.Errorf("expected map limit to be set, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}

	// Test Map Case with unit key: second
	mValSec := value.New(map[string]any{
		"rate":   10,
		"second": 1,
	})
	router.Limit(mValSec)
	if !router.hasLimit || router.limitRate != 10 || router.limitPeriod != time.Second {
		t.Errorf("expected map limit with second key to be set, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}

	// Test Map Case with unit key: minute
	mValMin := value.New(map[string]any{
		"rate":   100,
		"minute": 1,
	})
	router.Limit(mValMin)
	if !router.hasLimit || router.limitRate != 100 || router.limitPeriod != time.Minute {
		t.Errorf("expected map limit with minute key to be set, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}

	// Test Multiple params Case (rate number, duration string)
	router.Limit(value.New(25), value.New("5s"))
	if !router.hasLimit || router.limitRate != 25 || router.limitPeriod != 5*time.Second {
		t.Errorf("expected multiple params (number, string) to work, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}

	// Test Multiple params Case (rate number, duration number of seconds)
	router.Limit(value.New(30), value.New(2))
	if !router.hasLimit || router.limitRate != 30 || router.limitPeriod != 2*time.Second {
		t.Errorf("expected multiple params (number, number) to work, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}

	// Test Multiple params Case (rate number, time.Duration type)
	router.Limit(value.New(40), value.New(time.Minute))
	if !router.hasLimit || router.limitRate != 40 || router.limitPeriod != time.Minute {
		t.Errorf("expected multiple params (number, Duration) to work, got rate %d, period %v", router.limitRate, router.limitPeriod)
	}
}

func TestTenantLevelRateLimit(t *testing.T) {
	// Initialize tenant with a tenant-wide global limit of 3 and per-IP limit of 2
	tenant := &Tenant{
		rateLimitEnabled: true,
		rateLimitRate:    3,
		rateLimitIpRate:  2,
		rateLimitPeriod:  time.Second,
		currentLimiters:  make(map[string]*RateLimiter),
		previousLimiters: make(map[string]*RateLimiter),
		lastRotation:     time.Now(),
	}

	// No route-specific limits configured
	matchedRoute := &Router{
		hasLimit: false,
	}

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
	tenant := &Tenant{
		rateLimitEnabled: true,
		rateLimitPeriod:  time.Millisecond,
		currentLimiters:  make(map[string]*RateLimiter),
		previousLimiters: make(map[string]*RateLimiter),
		lastRotation:     time.Now(),
	}

	matchedRoute := &Router{
		hasLimit:    true,
		limitRate:   1,
		limitPeriod: time.Second,
		Method:      "GET",
		Path:        "/test",
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
	tenant.lastRotation = time.Now().Add(-2 * time.Second)

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
		rateLimitEnabled:  true,
		rateLimitUserRate: 2,
		rateLimitPeriod:   time.Second,
		currentLimiters:   make(map[string]*RateLimiter),
		previousLimiters:  make(map[string]*RateLimiter),
		lastRotation:      time.Now(),
	}

	matchedRoute := &Router{
		hasLimit: false,
	}

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
		rateLimitEnabled:  true,
		rateLimitUserRate: 2,
		rateLimitPeriod:   time.Second,
		currentLimiters:   make(map[string]*RateLimiter),
		previousLimiters:  make(map[string]*RateLimiter),
		lastRotation:      time.Now(),
	}

	matchedRoute := &Router{
		hasLimit: false,
	}

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
