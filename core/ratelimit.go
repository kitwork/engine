package core

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/work"
)

// RateLimiter is the HOST-level rate limiter — the first line of defense, evaluated in ServeHTTP
// BEFORE tenant resolution, so a flood on one domain cannot burn the whole host's resources.
// Four dimensions, each 0 = off:
//
//	Rate        — aggregate budget across ALL tenants per Period (the "global" bucket)
//	IPRate      — per client IP
//	BrowserRate — per browser fingerprint (catches IP rotation behind proxies)
//	UserRate    — per authenticated user account
//
// Buckets live in ONE shared work.LimiterStore and are consumed via work.EnforceChecks — the same
// evaluator the per-route .limit() rules use — so a rejected downstream check rolls back the
// upstream tokens it already took (a blocked request never burns global budget). Loopback/private
// IPs and DB-whitelisted IPs bypass entirely (internal services, health checks).
//
// Configure via server.kitwork.js `.rateLimit({ rate, ip, browser, user, period })` or the YAML
// `rate_limit:` block; wire with Engine.SetRateLimit before serving.
type RateLimiter struct {
	Rate        int
	IPRate      int
	BrowserRate int
	UserRate    int
	Period      time.Duration // window; min/default 1s

	storeOnce sync.Once
	store     *work.LimiterStore
}

// Store returns the shared host bucket store (created lazily, once).
func (rl *RateLimiter) Store() *work.LimiterStore {
	rl.storeOnce.Do(func() {
		if rl.Period <= 0 {
			rl.Period = time.Second
		}
		rl.store = work.NewLimiterStore(rl.Period)
	})
	return rl.store
}

// check consumes one token from every configured dimension, in order global → ip → user →
// browser. Returns true when the request may proceed; on rejection the 429 (with Retry-After) has
// already been written and earlier tokens rolled back.
func (rl *RateLimiter) check(w http.ResponseWriter, r *http.Request) bool {
	ip := work.GetClientIP(r)
	// Skip loopback/private (internal services) and DB-whitelisted IPs.
	if isPrivateOrLocalIP(ip) || isWhitelistedInDB(ip) {
		return true
	}

	store := rl.Store()
	var checks []work.LimitCheck
	if rl.Rate > 0 {
		checks = append(checks, work.LimitCheck{Store: store, Key: "global", Rate: rl.Rate, Period: rl.Period})
	}
	if rl.IPRate > 0 {
		checks = append(checks, work.LimitCheck{Store: store, Key: "ip:" + ip, Rate: rl.IPRate, Period: rl.Period})
	}
	if rl.UserRate > 0 {
		if userAccount, _ := work.GetClientUserAccount(r); userAccount != "" {
			checks = append(checks, work.LimitCheck{Store: store, Key: "user:" + userAccount, Rate: rl.UserRate, Period: rl.Period})
		}
	}
	if rl.BrowserRate > 0 {
		checks = append(checks, work.LimitCheck{Store: store, Key: "browser:" + work.GetClientBrowserFingerprint(r), Rate: rl.BrowserRate, Period: rl.Period})
	}
	return work.EnforceChecks(checks, w)
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
