package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	dom "github.com/kitwork/engine/domain"
	"github.com/kitwork/engine/work"
)

func TestResolveRequestHost(t *testing.T) {
	exactSites := map[string]bool{
		"studio.kitwork.localhost": true,
	}
	exact := func(domain string) bool { return exactSites[domain] }

	tests := []struct {
		name        string
		authority   string
		fallback    string
		allowLocal  bool
		wantRequest string
		wantDomain  string
		wantLocal   bool
		wantError   bool
	}{
		{
			name:        "production domain with port",
			authority:   "KITWORK.IO:443",
			wantRequest: "kitwork.io", wantDomain: "kitwork.io",
		},
		{
			name:      "bare localhost uses configured site",
			authority: "localhost:8080", fallback: "kitwork.io", allowLocal: true,
			wantRequest: "localhost", wantDomain: "kitwork.io", wantLocal: true,
		},
		{
			name:      "IPv4 loopback uses configured site",
			authority: "127.0.0.1:8080", fallback: "kitwork.io", allowLocal: true,
			wantRequest: "127.0.0.1", wantDomain: "kitwork.io", wantLocal: true,
		},
		{
			name:      "IPv6 loopback uses configured site",
			authority: "[::1]:8080", fallback: "kitwork.io", allowLocal: true,
			wantRequest: "::1", wantDomain: "kitwork.io", wantLocal: true,
		},
		{
			name:      "domain local host maps to canonical site",
			authority: "kitwork.io.localhost:8080", fallback: "other.example", allowLocal: true,
			wantRequest: "kitwork.io.localhost", wantDomain: "kitwork.io", wantLocal: true,
		},
		{
			name:      "nested domain local host maps to canonical site",
			authority: "API.KITWORK.IO.localhost.", allowLocal: true,
			wantRequest: "api.kitwork.io.localhost", wantDomain: "api.kitwork.io", wantLocal: true,
		},
		{
			name:      "legacy multi-label local site has exact precedence",
			authority: "studio.kitwork.localhost", allowLocal: true,
			wantRequest: "studio.kitwork.localhost", wantDomain: "studio.kitwork.localhost", wantLocal: true,
		},
		{
			name:      "single label remains reserved for explicit aliases",
			authority: "kitwork.localhost", allowLocal: true,
			wantRequest: "kitwork.localhost", wantDomain: "kitwork.localhost", wantLocal: true,
		},
		{
			name:      "production does not decode local suffix",
			authority: "kitwork.io.localhost", allowLocal: false,
			wantRequest: "kitwork.io.localhost", wantDomain: "kitwork.io.localhost", wantLocal: true,
		},
		{name: "empty host", wantError: true},
		{name: "path in host", authority: "kitwork.io/other", wantError: true},
		{name: "invalid label", authority: "-kitwork.io", wantError: true},
		{name: "invalid port", authority: "kitwork.io:not-a-port", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveRequestHost(test.authority, test.fallback, test.allowLocal, nil, exact)
			if test.wantError {
				if err == nil {
					t.Fatalf("resolveRequestHost(%q) succeeded: %+v", test.authority, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Requested != test.wantRequest || got.Domain != test.wantDomain || got.Local != test.wantLocal {
				t.Fatalf("resolveRequestHost(%q) = %+v, want requested=%q domain=%q local=%v",
					test.authority, got, test.wantRequest, test.wantDomain, test.wantLocal)
			}
		})
	}
}

func TestEngineLocalVirtualHostSharesCanonicalSiteRuntime(t *testing.T) {
	previousAllowLocal := work.AllowLocal
	work.AllowLocal = true
	t.Cleanup(func() { work.AllowLocal = previousAllowLocal })
	previousCanonical := dom.Canonical
	previousRedirects := dom.Redirects
	dom.Configure("www", nil)
	t.Cleanup(func() {
		dom.Canonical = previousCanonical
		dom.Redirects = previousRedirects
	})

	root := t.TempDir()
	writeHostTenant(t, root, "app-id", "kitwork.io", "canonical")
	writeHostTenant(t, root, "legacy-id", "studio.kitwork.localhost", "legacy")

	engine := New(root, 0, false, "kitwork.io")
	t.Cleanup(engine.Close)

	hit := func(authority string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
		request.Host = authority
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}

	for _, authority := range []string{"localhost:8080", "kitwork.io.localhost:8080", "127.0.0.1:8080", "[::1]:8080"} {
		response := hit(authority)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "canonical") {
			t.Fatalf("%s resolved to %d %q", authority, response.Code, response.Body.String())
		}
	}

	engine.mu.RLock()
	canonical := engine.cache["kitwork.io"]
	_, virtualLoaded := engine.cache["kitwork.io.localhost"]
	mappedDomain := engine.localHosts["kitwork.io.localhost"]
	engine.mu.RUnlock()
	if canonical == nil || virtualLoaded || mappedDomain != "kitwork.io" {
		t.Fatalf("local virtual host ownership: canonical=%v virtual=%v mapping=%q",
			canonical != nil, virtualLoaded, mappedDomain)
	}
	if canonical.current().Domain() != "kitwork.io" {
		t.Fatalf("canonical tenant domain = %q", canonical.current().Domain())
	}

	legacy := hit("studio.kitwork.localhost:8080")
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), "legacy") {
		t.Fatalf("legacy local site resolved to %d %q", legacy.Code, legacy.Body.String())
	}
	production := hit("kitwork.io:8080")
	if production.Code != http.StatusMovedPermanently || production.Header().Get("Location") != "http://www.kitwork.io/" {
		t.Fatalf("production-shaped host redirect = %d %q",
			production.Code, production.Header().Get("Location"))
	}

	bad := hit("kitwork.io/other")
	if bad.Code != http.StatusBadRequest || strings.Contains(bad.Body.String(), "kitwork.io") {
		t.Fatalf("malformed host response = %d %q", bad.Code, bad.Body.String())
	}

	engine.Close()
	engine.mu.RLock()
	remainingMappings := len(engine.localHosts)
	engine.mu.RUnlock()
	if remainingMappings != 0 {
		t.Fatalf("engine close retained %d local host mappings", remainingMappings)
	}
}

func TestEngineLocalVirtualHostConcurrentFirstLoad(t *testing.T) {
	previousAllowLocal := work.AllowLocal
	work.AllowLocal = true
	t.Cleanup(func() { work.AllowLocal = previousAllowLocal })

	root := t.TempDir()
	writeHostTenant(t, root, "app-id", "kitwork.io", "canonical")
	engine := New(root, 0, false, "kitwork.io")
	t.Cleanup(engine.Close)

	const requests = 16
	responses := make(chan *httptest.ResponseRecorder, requests)
	var group sync.WaitGroup
	group.Add(requests)
	for range requests {
		go func() {
			defer group.Done()
			request := httptest.NewRequest(http.MethodGet, "http://kitwork.io.localhost:8080/", nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			responses <- response
		}()
	}
	group.Wait()
	close(responses)

	for response := range responses {
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "canonical") {
			t.Fatalf("concurrent local request resolved to %d %q", response.Code, response.Body.String())
		}
	}
	engine.mu.RLock()
	cacheCount := len(engine.cache)
	canonical := engine.cache["kitwork.io"]
	mappingCount := len(engine.localHosts)
	mappedDomain := engine.localHosts["kitwork.io.localhost"]
	engine.mu.RUnlock()
	if cacheCount != 1 || canonical == nil || mappingCount != 1 || mappedDomain != "kitwork.io" {
		t.Fatalf("concurrent local ownership: cache=%d canonical=%v mappings=%d domain=%q",
			cacheCount, canonical != nil, mappingCount, mappedDomain)
	}
}

func writeHostTenant(t testing.TB, root, identity, domain, body string) {
	t.Helper()
	directory := filepath.Join(root, identity, domain)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRouterBody(t, filepath.Join(directory, work.RouterFileName), body)
}
