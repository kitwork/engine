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

// TrustProxyHeaders controls whether GetClientIP believes X-Forwarded-For / X-Real-IP. Kitwork is
// normally the EDGE server (it owns :443 itself via AutoSSL), so those headers come straight from
// the client and are trivially spoofable — trusting them would let an attacker rotate fake IPs
// past every per-IP rate limit. Default FALSE (RemoteAddr only); set true ONLY when deployed
// behind a reverse proxy you control (config `trust_proxy: true` / `.trustProxy(true)`).
var TrustProxyHeaders bool

func GetClientIP(r *http.Request) string {
	if TrustProxyHeaders {
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			parts := strings.Split(ip, ",")
			return strings.TrimSpace(parts[0])
		}
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
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
	if !RateLimitEnabled {
		return true
	}

	ip := GetClientIP(r)
	if isPrivateOrLocalIP(ip) {
		return true
	}

	if len(t.limiters) == 0 {
		t.limiters = make([]*LimiterStore, ScopeMax)
		t.limiters[ScopeTenant] = NewLimiterStore(RateLimitPeriod)
	}

	userAcc, customUserRate := GetClientUserAccount(r)
	var checks []LimitCheck

	// 1. Process tenant default/global rate limit rules
	for _, rule := range t.rateLimitRules {
		store := t.limiters[ScopeTenant]
		if store == nil {
			continue
		}
		rateToUse := rule.Rate
		if rule.Type == "user" && customUserRate > 0 {
			rateToUse = customUserRate
		}

		var scopeKey string
		switch rule.Type {
		case "user":
			if userAcc == "" {
				continue
			}
			scopeKey = "user:" + userAcc
		case "browser":
			scopeKey = "browser:" + GetClientBrowserFingerprint(r)
		case "global":
			scopeKey = "global"
		default: // "ip"
			scopeKey = "ip:" + ip
		}
		
		key := fmt.Sprintf("tenant:%s:%d/%s", scopeKey, rateToUse, rule.Period)
		checks = append(checks, LimitCheck{store, key, rateToUse, rule.Period})
	}

	// 2. Process route-specific rules
	if matched != nil {
		for _, rule := range matched.limitRules {
			scopeIdx := ScopeTenant
			if rule.Scope == "server" {
				scopeIdx = ScopeServer
			}
			store := t.limiters[scopeIdx]
			if store == nil {
				continue
			}
			var scopeKey string
			switch rule.Type {
			case "user":
				if userAcc == "" {
					continue // anonymous request — a "user" rule does not apply
				}
				scopeKey = "user:" + userAcc
			case "browser":
				scopeKey = "browser:" + GetClientBrowserFingerprint(r)
			case "global":
				scopeKey = "global"
			default: // "ip"
				scopeKey = "ip:" + ip
			}
			// rate/period in the key keep multiple rules (same type, different window) distinct.
			key := fmt.Sprintf("route:%s:%s:%s:%d/%s", scopeKey, matched.Method, matched.Path, rule.Rate, rule.Period)
			checks = append(checks, LimitCheck{store, key, rule.Rate, rule.Period})
		}
	}

	return EnforceChecks(checks, w)
}

// LimiterStore is a rotating map of token-bucket limiters, safe for concurrent use. The host
// uses one store (server-wide buckets); each tenant uses its own. A rule's scope just decides
// which store it lands in — that is the whole tenant-vs-server distinction.
type LimiterStore struct {
	mu           sync.Mutex
	current      map[string]*RateLimiter
	previous     map[string]*RateLimiter
	lastRotation time.Time
	rotateBase   time.Duration // stale buckets are swept every 10 * rotateBase
}

// NewLimiterStore returns an empty store. rotateBase only tunes how often stale buckets are
// garbage-collected (every 10 * rotateBase); it does not affect any limit's accuracy. Min 1s.
func NewLimiterStore(rotateBase time.Duration) *LimiterStore {
	if rotateBase <= 0 {
		rotateBase = time.Second
	}
	return &LimiterStore{
		current:      make(map[string]*RateLimiter),
		previous:     make(map[string]*RateLimiter),
		lastRotation: time.Now(),
		rotateBase:   rotateBase,
	}
}

// limiter returns the bucket for key (creating it if absent), carrying a still-active bucket
// over from the previous map across a rotation. Takes its own lock.
func (s *LimiterStore) limiter(key string, rate int, period time.Duration, now time.Time) *RateLimiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	threshold := 10 * s.rotateBase
	if now.Sub(s.lastRotation) > threshold {
		s.previous = s.current
		s.current = make(map[string]*RateLimiter)
		s.lastRotation = now
	}
	if lim, ok := s.current[key]; ok {
		return lim
	}
	if lim, ok := s.previous[key]; ok {
		if now.Sub(lim.LastRefill()) < threshold {
			s.current[key] = lim
			return lim
		}
	}
	lim := NewRateLimiter(rate, period)
	s.current[key] = lim
	return lim
}

// LimitCheck is one bucket to consume: which Store, the bucket Key, and its Rate/Period.
type LimitCheck struct {
	Store  *LimiterStore
	Key    string
	Rate   int
	Period time.Duration
}

// EnforceChecks consumes one token from each check IN ORDER. If any is exhausted it rolls back
// the tokens already taken from earlier checks (so a downstream limit never burns upstream
// budget) and writes a 429. Returns true only if every check passes. Shared by host + tenant.
func EnforceChecks(checks []LimitCheck, w http.ResponseWriter) bool {
	if len(checks) == 0 {
		return true
	}
	now := time.Now()
	lims := make([]*RateLimiter, len(checks))
	for i, c := range checks {
		lims[i] = c.Store.limiter(c.Key, c.Rate, c.Period, now)
	}
	for i, c := range checks {
		if !lims[i].Allow(c.Rate, c.Period) {
			for j := 0; j < i; j++ {
				lims[j].Rollback(checks[j].Rate)
			}
			write429(w, c.Period)
			return false
		}
	}
	return true
}

func write429(w http.ResponseWriter, period time.Duration) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Retry-After", fmt.Sprintf("%.0f", period.Seconds()))
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(`{"error": "Too Many Requests", "message": "Rate limit exceeded. Please try again later."}`))
}
