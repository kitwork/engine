package core

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/work"
)

type RateLimiter struct {
	Enabled          bool
	Rate             int
	IpRate           int
	BrowserRate      int
	Period           time.Duration
	currentLimiters  map[string]*work.RateLimiter
	previousLimiters map[string]*work.RateLimiter
	lastRotation     time.Time
	mu               sync.Mutex
}

func isPrivateOrLocalIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func isWhitelistedInDB(ipStr string) bool {
	if database.System == nil {
		return false
	}

	// Query to check if the IP exists in the ip_whitelist table
	query := "SELECT EXISTS(SELECT 1 FROM ip_whitelist WHERE ip = $1)"

	var exists bool
	err := database.System.QueryRow(query, ipStr).Scan(&exists)
	if err != nil {
		// Table may not exist yet or connection error; fall back to false
		return false
	}
	return exists
}

func (rl *RateLimiter) check(r *http.Request, w http.ResponseWriter) bool {
	if !rl.Enabled {
		return true
	}

	ip := work.GetClientIP(r)

	// 1. Skip rate limiting for local loopback (itself) and private network IPs (internal services)
	if isPrivateOrLocalIP(ip) {
		return true
	}

	// 2. Skip rate limiting for database-whitelisted IPs
	if isWhitelistedInDB(ip) {
		return true
	}

	now := time.Now()
	
	rl.mu.Lock()
	if rl.currentLimiters == nil {
		rl.currentLimiters = make(map[string]*work.RateLimiter)
	}
	if rl.previousLimiters == nil {
		rl.previousLimiters = make(map[string]*work.RateLimiter)
	}

	// 0. Perform Map Rotation if rotation threshold is exceeded (10 * Period)
	rotationThreshold := 10 * rl.Period
	if now.Sub(rl.lastRotation) > rotationThreshold {
		rl.previousLimiters = rl.currentLimiters
		rl.currentLimiters = make(map[string]*work.RateLimiter)
		rl.lastRotation = now
	}

	getLimiter := func(key string, rate int, period time.Duration) *work.RateLimiter {
		lim, exists := rl.currentLimiters[key]
		if exists {
			return lim
		}

		lim, exists = rl.previousLimiters[key]
		if exists {
			if now.Sub(lim.LastRefill()) < rotationThreshold {
				rl.currentLimiters[key] = lim
				return lim
			}
		}

		lim = work.NewRateLimiter(rate, period)
		rl.currentLimiters[key] = lim
		return lim
	}

	// 3. Get Global Server Limiter
	var globalLim *work.RateLimiter
	if rl.Rate > 0 {
		globalLim = getLimiter("global", rl.Rate, rl.Period)
	}

	// 4. Get Per-IP Limiter
	var ipLim *work.RateLimiter
	if rl.IpRate > 0 {
		ipLim = getLimiter("ip:"+ip, rl.IpRate, rl.Period)
	}

	// 5. Get Per-Browser Limiter
	var browserLim *work.RateLimiter
	if rl.BrowserRate > 0 {
		fingerprint := work.GetClientBrowserFingerprint(r)
		browserLim = getLimiter("browser:"+fingerprint, rl.BrowserRate, rl.Period)
	}
	rl.mu.Unlock()

	// 7. Check Global Server Limit (Aggregate System capacity)
	if globalLim != nil {
		if !globalLim.Allow(rl.Rate, rl.Period) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", rl.Period.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Server is temporarily busy. Please try again later."}`))
			return false
		}
	}

	// 8. Check Per-IP Limit (DDoS Protection)
	if ipLim != nil {
		if !ipLim.Allow(rl.IpRate, rl.Period) {
			if globalLim != nil {
				globalLim.Rollback(rl.Rate) // Rollback global consumption
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", rl.Period.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Rate limit exceeded. Please try again later."}`))
			return false
		}
	}

	// 9. Check Per-Browser Limit
	if browserLim != nil {
		if !browserLim.Allow(rl.BrowserRate, rl.Period) {
			if ipLim != nil {
				ipLim.Rollback(rl.IpRate)
			}
			if globalLim != nil {
				globalLim.Rollback(rl.Rate)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", rl.Period.Seconds()))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests", "message": "Browser session rate limit exceeded. Please try again later."}`))
			return false
		}
	}

	return true
}
