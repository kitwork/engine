package core

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/work"
)

const localDevelopmentSuffix = ".localhost"

// requestHostResolution keeps the browser-visible host separate from the
// canonical site key. Local aliases must not create a second SiteRuntime.
type requestHostResolution struct {
	Requested string
	Domain    string
	Local     bool
}

func resolveRequestHost(
	authority,
	defaultHostname string,
	allowLocal bool,
	knownMapping func(string) (string, bool),
	exactSite func(string) bool,
) (requestHostResolution, error) {
	requested, err := normalizeRequestHost(authority)
	if err != nil {
		return requestHostResolution{}, err
	}
	resolved := requestHostResolution{
		Requested: requested,
		Domain:    requested,
		Local:     isLoopbackHost(requested) || strings.HasSuffix(requested, localDevelopmentSuffix),
	}

	// Preserve the existing localhost fallback even when AllowLocal is false.
	// This keeps tests and explicitly loopback-bound production processes
	// compatible while virtual *.localhost decoding remains local-only.
	if isLoopbackHost(requested) && defaultHostname != "" {
		fallback, fallbackErr := normalizeRequestHost(defaultHostname)
		if fallbackErr != nil {
			return requestHostResolution{}, fmt.Errorf("invalid configured hostname: %w", fallbackErr)
		}
		resolved.Domain = fallback
		return resolved, nil
	}

	if !allowLocal || !strings.HasSuffix(requested, localDevelopmentSuffix) {
		return resolved, nil
	}
	if knownMapping != nil {
		if domain, ok := knownMapping(requested); ok {
			resolved.Domain = domain
			return resolved, nil
		}
	}

	candidate := strings.TrimSuffix(requested, localDevelopmentSuffix)
	// A single label is reserved for a future explicit username/alias registry.
	// Until then, kitwork.localhost remains a literal legacy site name.
	if !strings.Contains(candidate, ".") || net.ParseIP(candidate) != nil {
		return resolved, nil
	}
	// Existing multi-label *.localhost folders retain exact precedence. This
	// keeps legacy sites such as studio.kitwork.localhost working while a host
	// with no exact source, such as kitwork.io.localhost, maps to kitwork.io.
	if exactSite != nil && exactSite(requested) {
		return resolved, nil
	}

	resolved.Domain = candidate
	return resolved, nil
}

func normalizeRequestHost(authority string) (string, error) {
	authority = strings.TrimSpace(authority)
	if authority == "" {
		return "", fmt.Errorf("empty host")
	}
	if strings.ContainsAny(authority, "/\\?#@\x00\r\n\t ") {
		return "", fmt.Errorf("malformed host")
	}

	host := authority
	switch {
	case strings.HasPrefix(authority, "["):
		if strings.HasSuffix(authority, "]") {
			host = strings.TrimSuffix(strings.TrimPrefix(authority, "["), "]")
		} else {
			parsedHost, port, err := net.SplitHostPort(authority)
			if err != nil || !validPort(port) {
				return "", fmt.Errorf("malformed host")
			}
			host = parsedHost
		}
		if net.ParseIP(host) == nil {
			return "", fmt.Errorf("malformed IP host")
		}
	case strings.Count(authority, ":") > 1:
		if net.ParseIP(authority) == nil {
			return "", fmt.Errorf("malformed host")
		}
	case strings.Contains(authority, ":"):
		parsedHost, port, err := net.SplitHostPort(authority)
		if err != nil || !validPort(port) {
			return "", fmt.Errorf("malformed host")
		}
		host = parsedHost
	}

	host = strings.ToLower(strings.TrimRight(host, "."))
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	if !validDNSHostname(host) {
		return "", fmt.Errorf("malformed DNS host")
	}
	return host, nil
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 0 && value <= 65535
}

func validDNSHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (e *Engine) resolveRequestHost(authority string, allowLocal bool) (requestHostResolution, error) {
	return resolveRequestHost(
		authority,
		e.Hostname,
		allowLocal,
		e.localHostMapping,
		e.hasExactSiteSource,
	)
}

func (e *Engine) localHostMapping(requested string) (string, bool) {
	e.mu.RLock()
	domain, ok := e.localHosts[requested]
	e.mu.RUnlock()
	return domain, ok
}

// rememberLocalHostResolution caches only a virtual host that reached a real
// site successfully. Its cardinality is therefore bounded by loaded sites,
// rather than by arbitrary Host headers sent to the local listener.
func (e *Engine) rememberLocalHostResolution(resolved requestHostResolution) {
	if resolved.Requested == resolved.Domain ||
		!strings.HasSuffix(resolved.Requested, localDevelopmentSuffix) {
		return
	}
	e.mu.Lock()
	if !e.closed {
		e.localHosts[resolved.Requested] = resolved.Domain
	}
	e.mu.Unlock()
}

func (e *Engine) forgetLocalHostResolution(resolved requestHostResolution) {
	if resolved.Requested == resolved.Domain {
		return
	}
	e.mu.Lock()
	delete(e.localHosts, resolved.Requested)
	e.mu.Unlock()
}

// hasExactSiteSource is used only for the first request to an ambiguous local
// development host. Loaded sites take the lock-only fast path; filesystem
// probing remains outside production request routing.
func (e *Engine) hasExactSiteSource(domain string) bool {
	e.mu.RLock()
	_, loaded := e.cache[domain]
	e.mu.RUnlock()
	if loaded {
		return true
	}

	switch e.rootLayout {
	case work.RootLayoutSingle:
		// One app root is not evidence that an arbitrary virtual host is an
		// intentionally named legacy site.
		return false
	case work.RootLayoutMultiDomain:
		return fileExists(filepath.Join(e.root, domain, work.RouterFileName))
	case work.RootLayoutMultiTenant:
		return nestedSiteSourceExists(e.root, domain)
	}

	switch e.root {
	case "", "./", "../", "/", ".", "..":
		// A standalone root is not evidence that an arbitrary virtual host is
		// an intentionally named legacy site.
		return false
	}
	if directoryExists(filepath.Join(e.root, domain)) ||
		fileExists(filepath.Join(e.root, work.SitesDirName, domain, work.RouterFileName)) ||
		fileExists(filepath.Join(e.root, "test", domain, work.RouterFileName)) {
		return true
	}

	entries, err := os.ReadDir(e.root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && directoryExists(filepath.Join(e.root, entry.Name(), domain)) {
			return true
		}
	}
	return false
}

func nestedSiteSourceExists(root, domain string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && fileExists(filepath.Join(
			root,
			entry.Name(),
			domain,
			work.RouterFileName,
		)) {
			return true
		}
	}
	return false
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
