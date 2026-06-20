package domain

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/database"
)

// Redirect rules, set once at startup via Configure.
var (
	// Canonical: "apex" → strip www; "www" → add www; "" → off.
	Canonical string
	// Redirects maps a host → target (a host, or a full http(s):// URL).
	Redirects map[string]string
)

// Configure installs the domain-redirect rules. Hosts are matched lower-case.
func Configure(canonical string, redirects map[string]string) {
	Canonical = strings.ToLower(strings.TrimSpace(canonical))
	Redirects = make(map[string]string, len(redirects))
	for k, v := range redirects {
		Redirects[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
}

func isLocalOrIP(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	return net.ParseIP(host) != nil
}

// Target computes the single canonical destination for a request, applying (in
// one shot, so at most ONE 301 is ever emitted): domain→domain map, www↔apex
// canonical, and http→https when forceHTTPS. Returns ("", false) when the request
// is already canonical. Loop-safe: if the rules resolve back to the original
// scheme+host, no redirect is emitted.
func Target(scheme, host, path, rawQuery string, forceHTTPS bool) (string, bool) {
	scheme = strings.ToLower(scheme)
	host = strings.ToLower(host)
	origScheme, origHost := scheme, host

	// 1. domain → domain map. A full-URL target short-circuits (keeps path/query).
	if t, ok := Redirects[host]; ok && t != "" && t != host {
		if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
			return appendPath(t, path, rawQuery), true
		}
		host = strings.ToLower(t)
	}

	// 2. canonical www ↔ apex (never touch localhost / raw IPs).
	if !isLocalOrIP(host) {
		switch Canonical {
		case "apex":
			host = strings.TrimPrefix(host, "www.")
		case "www":
			if !strings.HasPrefix(host, "www.") {
				host = "www." + host
			}
		}
	}

	// 3. force https.
	if forceHTTPS {
		scheme = "https"
	}

	if scheme == origScheme && host == origHost {
		return "", false // already canonical → no redirect (also breaks any rule cycle)
	}
	u := scheme + "://" + host + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u, true
}

func appendPath(base, path, rawQuery string) string {
	u := strings.TrimRight(base, "/") + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

// RedirectURL builds a redirect target from `to` (a host OR a full http(s):// URL),
// keeping the request path + query.
func RedirectURL(scheme, to, path, rawQuery string) string {
	if strings.HasPrefix(to, "http://") || strings.HasPrefix(to, "https://") {
		return appendPath(to, path, rawQuery)
	}
	u := scheme + "://" + to + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

// --- DB-driven per-domain redirect (the `redirect_to` column on the domain table) ---

type redirEntry struct {
	target string
	at     time.Time
}

var (
	redirCache   = map[string]redirEntry{}
	redirCacheMu sync.RWMutex
	// RedirectTTL controls how long a domain's redirect_to lookup is cached.
	RedirectTTL = 5 * time.Minute
)

// DBRedirectTarget returns the system `domain.redirect_to` for a host, cached for
// RedirectTTL (both hits AND misses are cached, so a non-redirecting domain costs
// at most one DB query per TTL). "" means no redirect. Fail-open on DB errors.
func DBRedirectTarget(host string) string {
	host = strings.ToLower(host)
	now := time.Now()

	redirCacheMu.RLock()
	if e, ok := redirCache[host]; ok && now.Sub(e.at) < RedirectTTL {
		redirCacheMu.RUnlock()
		return e.target
	}
	redirCacheMu.RUnlock()

	target, err := database.DomainRedirect(host)
	if err != nil {
		target = "" // no row / missing column / DB error → don't redirect
	}
	redirCacheMu.Lock()
	redirCache[host] = redirEntry{target: target, at: now}
	redirCacheMu.Unlock()
	return target
}

// HostOf strips any :port from a request Host header.
func HostOf(hostHeader string) string {
	if i := strings.IndexByte(hostHeader, ':'); i >= 0 {
		return hostHeader[:i]
	}
	return hostHeader
}

// RedirectFallback is the :80 ACME fallback handler: autocert serves ACME
// challenges, everything else is forced to https (+ canonical/map) here.
func RedirectFallback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := HostOf(r.Host)
		if u, ok := Target("http", host, r.URL.Path, r.URL.RawQuery, true); ok {
			http.Redirect(w, r, u, http.StatusMovedPermanently)
			return
		}
		if to := DBRedirectTarget(host); to != "" && to != host {
			http.Redirect(w, r, RedirectURL("https", to, r.URL.Path, r.URL.RawQuery), http.StatusMovedPermanently)
			return
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}
