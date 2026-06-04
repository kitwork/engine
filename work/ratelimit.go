package work

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate Limiter Global settings (used as package defaults if needed)
var RateLimitEnabled bool = true
var DefaultTenantRate int = 1200
var DefaultTenantIpRate int = 120
var DefaultTenantBrowserRate int = 240
var DefaultTenantUserRate int = 360
var RateLimitPeriod time.Duration = time.Second

// Token bucket rate limiter
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

func NewRateLimiter(rate int, period time.Duration) *RateLimiter {
	return &RateLimiter{
		tokens:     float64(rate),
		lastRefill: time.Now(),
	}
}

func (lim *RateLimiter) Allow(rate int, period time.Duration) bool {
	lim.mu.Lock()
	defer lim.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(lim.lastRefill)
	lim.lastRefill = now

	refill := float64(elapsed) * float64(rate) / float64(period)
	lim.tokens += refill
	if lim.tokens > float64(rate) {
		lim.tokens = float64(rate)
	}

	if lim.tokens >= 1.0 {
		lim.tokens -= 1.0
		return true
	}
	return false
}

func (lim *RateLimiter) Rollback(rate int) {
	lim.mu.Lock()
	defer lim.mu.Unlock()
	lim.tokens += 1.0
	if lim.tokens > float64(rate) {
		lim.tokens = float64(rate)
	}
}

func (lim *RateLimiter) LastRefill() time.Time {
	lim.mu.Lock()
	defer lim.mu.Unlock()
	return lim.lastRefill
}

func GetClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

func GetClientBrowserFingerprint(r *http.Request) string {
	if cookie, err := r.Cookie("__device_id"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if devID := r.Header.Get("X-Device-ID"); devID != "" {
		return devID
	}
	ua := r.Header.Get("User-Agent")
	lang := r.Header.Get("Accept-Language")
	h := sha256.New()
	h.Write([]byte(ua + "|" + lang))
	return hex.EncodeToString(h.Sum(nil))
}

func ParseJWTRateLimit(tokenStr string) (int, bool) {
	tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
	tokenStr = strings.TrimSpace(tokenStr)

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return 0, false
	}

	payloadSegment := parts[1]
	paddingRestored := payloadSegment
	switch len(payloadSegment) % 4 {
	case 2:
		paddingRestored += "=="
	case 3:
		paddingRestored += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(paddingRestored)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(payloadSegment)
		if err != nil {
			return 0, false
		}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return 0, false
	}

	for _, key := range []string{"user_rate", "rate_limit", "limit"} {
		if val, ok := claims[key]; ok {
			if num, ok := val.(float64); ok {
				return int(num), true
			}
		}
	}
	return 0, false
}

func GetClientUserAccount(r *http.Request) (string, int) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		h := sha256.New()
		h.Write([]byte(auth))
		userKey := "auth:" + hex.EncodeToString(h.Sum(nil))[:16]

		if customRate, ok := ParseJWTRateLimit(auth); ok {
			return userKey, customRate
		}
		return userKey, 0
	}
	for _, cookieName := range []string{"session_id", "session", "token", "jwt", "uid"} {
		if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
			h := sha256.New()
			h.Write([]byte(cookie.Value))
			userKey := "cookie:" + cookieName + ":" + hex.EncodeToString(h.Sum(nil))[:16]

			if customRate, ok := ParseJWTRateLimit(cookie.Value); ok {
				return userKey, customRate
			}
			return userKey, 0
		}
	}
	return "", 0
}

func isPrivateOrLocalIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func (t *Tenant) checkRateLimit(matched *Router, r *http.Request, w http.ResponseWriter) bool {
	if !t.rateLimitEnabled {
		return true
	}

	ip := GetClientIP(r)
	if isPrivateOrLocalIP(ip) {
		return true
	}
	now := time.Now()

	t.rateLimiterMu.Lock()
	if t.currentLimiters == nil {
		t.currentLimiters = make(map[string]*RateLimiter)
	}
	if t.previousLimiters == nil {
		t.previousLimiters = make(map[string]*RateLimiter)
	}

	// 0. Perform Map Rotation if rotation threshold is exceeded (10 * rateLimitPeriod)
	rotationThreshold := 10 * t.rateLimitPeriod
	if now.Sub(t.lastRotation) > rotationThreshold {
		t.previousLimiters = t.currentLimiters
		t.currentLimiters = make(map[string]*RateLimiter)
		t.lastRotation = now
	}

	// Helper to get or create a rate limiter with double buffering (Map Rotation)
	getLimiter := func(key string, rate int, period time.Duration) *RateLimiter {
		// First search in current active map
		lim, exists := t.currentLimiters[key]
		if exists {
			return lim
		}

		// Second search in previous map
		lim, exists = t.previousLimiters[key]
		if exists {
			lim.mu.Lock()
			refillAge := now.Sub(lim.lastRefill)
			lim.mu.Unlock()

			// Carry over if it was active within the rotation window
			if refillAge < rotationThreshold {
				t.currentLimiters[key] = lim
				return lim
			}
		}

		// Otherwise, create a new limiter
		lim = NewRateLimiter(rate, period)
		t.currentLimiters[key] = lim
		return lim
	}

	// 1. Get Tenant Global Limiter
	var tenantGlobalLim *RateLimiter
	if t.rateLimitRate > 0 {
		tenantGlobalLim = getLimiter("tenant:global", t.rateLimitRate, t.rateLimitPeriod)
	}

	// 2. Get Tenant IP Limiter
	var tenantIpLim *RateLimiter
	if t.rateLimitIpRate > 0 {
		tenantIpLim = getLimiter("tenant:ip:"+ip, t.rateLimitIpRate, t.rateLimitPeriod)
	}

	// 3. Get Tenant User Limiter
	var tenantUserLim *RateLimiter
	userAcc, customUserRate := GetClientUserAccount(r)
	userRateToUse := t.rateLimitUserRate
	if customUserRate > 0 {
		userRateToUse = customUserRate
	}
	if userRateToUse > 0 && userAcc != "" {
		tenantUserLim = getLimiter("tenant:user:"+userAcc, userRateToUse, t.rateLimitPeriod)
	}

	// 4. Get Route Limiter
	var routeLim *RateLimiter
	if matched != nil && matched.hasLimit {
		routeKey := "route:" + ip + ":" + matched.Method + ":" + matched.Path
		routeLim = getLimiter(routeKey, matched.limitRate, matched.limitPeriod)
	}
	t.rateLimiterMu.Unlock()

	// 4. Check Tenant Global Limit
	if tenantGlobalLim != nil {
		if !tenantGlobalLim.Allow(t.rateLimitRate, t.rateLimitPeriod) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", t.rateLimitPeriod.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Tenant traffic limit exceeded."}`))
			return false
		}
	}

	// 5. Check Tenant IP Limit
	if tenantIpLim != nil {
		if !tenantIpLim.Allow(t.rateLimitIpRate, t.rateLimitPeriod) {
			if tenantGlobalLim != nil {
				tenantGlobalLim.Rollback(t.rateLimitRate)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", t.rateLimitPeriod.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Tenant rate limit exceeded for this IP."}`))
			return false
		}
	}

	// 6. Check Tenant User Limit
	if tenantUserLim != nil {
		if !tenantUserLim.Allow(userRateToUse, t.rateLimitPeriod) {
			if tenantIpLim != nil {
				tenantIpLim.Rollback(t.rateLimitIpRate)
			}
			if tenantGlobalLim != nil {
				tenantGlobalLim.Rollback(t.rateLimitRate)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", t.rateLimitPeriod.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Tenant rate limit exceeded for this account."}`))
			return false
		}
	}

	// 7. Check Route-Specific Limit
	if routeLim != nil {
		if !routeLim.Allow(matched.limitRate, matched.limitPeriod) {
			if tenantUserLim != nil {
				tenantUserLim.Rollback(userRateToUse)
			}
			if tenantIpLim != nil {
				tenantIpLim.Rollback(t.rateLimitIpRate)
			}
			if tenantGlobalLim != nil {
				tenantGlobalLim.Rollback(t.rateLimitRate)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", matched.limitPeriod.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Endpoint rate limit exceeded."}`))
			return false
		}
	}

	return true
}

func (t *Tenant) CleanOldLimiters() {
	t.rateLimiterMu.Lock()
	defer t.rateLimiterMu.Unlock()
	t.previousLimiters = make(map[string]*RateLimiter)
}
