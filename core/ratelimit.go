package core

import (
	"net"
	"time"

	"github.com/kitwork/engine/database"
)

// HostRule is one server-wide rate-limit rule — same { type, rate, period } model as a tenant
// route rule, but evaluated against the shared host store (so it counts across ALL tenants).
type HostRule struct {
	Type   string // "global" (aggregate) | "ip" | "user" | "browser"
	Rate   int
	Period time.Duration
}

// type RateLimiter struct {
// 	Enabled     bool
// 	Rate        int // aggregate server capacity (the "global" bucket)
// 	IpRate      int
// 	BrowserRate int
// 	UserRate    int
// 	Period      time.Duration
// 	Rules       []HostRule         // array-style host rules (unified with the tenant/route model)
// 	store       *work.LimiterStore // shared host buckets; also handed to tenants for scope:"server"
// }

// Store returns the shared host limiter store (created lazily). Tenants receive it via
// SetHostLimiters so their scope:"server" rules land in the same server-wide buckets.
// func (rl *RateLimiter) Store() *work.LimiterStore {
// 	if rl.store == nil {
// 		rl.store = work.NewLimiterStore(rl.Period)
// 	}
// 	return rl.store
// }

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

// func (rl *RateLimiter) check(r *http.Request, w http.ResponseWriter) bool {
// 	if !rl.Enabled {
// 		return true
// 	}

// 	ip := work.GetClientIP(r)
// 	// Skip loopback/private (internal services) and DB-whitelisted IPs.
// 	if isPrivateOrLocalIP(ip) || isWhitelistedInDB(ip) {
// 		return true
// 	}

// 	store := rl.Store()
// 	userAcc, _ := work.GetClientUserAccount(r)

// 	// Server-wide buckets to consume (order = rollback order). The legacy fixed fields and the
// 	// array-style Rules share the SAME host store + evaluator as the tenant/route limits.
// 	var checks []work.LimitCheck
// 	if rl.Rate > 0 {
// 		checks = append(checks, work.LimitCheck{Store: store, Key: "global", Rate: rl.Rate, Period: rl.Period})
// 	}
// 	if rl.IpRate > 0 {
// 		checks = append(checks, work.LimitCheck{Store: store, Key: "ip:" + ip, Rate: rl.IpRate, Period: rl.Period})
// 	}
// 	if rl.UserRate > 0 && userAcc != "" {
// 		checks = append(checks, work.LimitCheck{Store: store, Key: "user:" + userAcc, Rate: rl.UserRate, Period: rl.Period})
// 	}
// 	if rl.BrowserRate > 0 {
// 		checks = append(checks, work.LimitCheck{Store: store, Key: "browser:" + work.GetClientBrowserFingerprint(r), Rate: rl.BrowserRate, Period: rl.Period})
// 	}

// 	for _, rule := range rl.Rules {
// 		var scopeKey string
// 		switch rule.Type {
// 		case "user":
// 			if userAcc == "" {
// 				continue
// 			}
// 			scopeKey = "user:" + userAcc
// 		case "browser":
// 			scopeKey = "browser:" + work.GetClientBrowserFingerprint(r)
// 		case "ip":
// 			scopeKey = "ip:" + ip
// 		default: // "global"
// 			scopeKey = "global"
// 		}
// 		checks = append(checks, work.LimitCheck{
// 			Store:  store,
// 			Key:    fmt.Sprintf("rule:%s:%d/%s", scopeKey, rule.Rate, rule.Period),
// 			Rate:   rule.Rate,
// 			Period: rule.Period,
// 		})
// 	}

// 	return work.EnforceChecks(checks, w)
// }
