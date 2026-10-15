package work

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	jitjs "github.com/kitwork/engine/jit/js"
)

// engineScriptRe and engineFontRe find what a rendered page asks of the engine: each script, and each
// font file its @font-face rules and preload links name. The production minifier may drop the quotes.
var (
	engineScriptRe = regexp.MustCompile(`<script[^>]*\ssrc="?(/[^"\s>]+\.js)`)
	engineFontRe   = regexp.MustCompile(`(?:url\(|href="?)(/[^)"\s>]+\.woff2)`)
)

// enginePage is a template page that needs every kind of file the engine serves for a page: the
// kernel, a directive, a catalogue component written with the version a template page may still
// name, a site's own component that calls a native service (which brings the service and its
// assembly), and a vendored font.
func enginePage() map[string]string {
	files := componentPage(`<nav data-kit-component="menu@1.0.0"></nav><b data-kit-intersect="seen = true">x</b><div data-kit-component="workbench"></div><p class="font-outfit">x</p>`)
	files["localhost/_components/workbench.js"] = `kit.component("workbench", { init: function () { kit.studioState.clearHistory(); } });`
	return files
}

func serveEngine(tenant *Tenant, method, path string, header ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://localhost/", nil)
	request.URL.Path = path // as the server parsed it, spelling and all
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	return recorder
}

// Everything a page asks of the engine is under /_kitwork/, grouped by role, each name
// <role>.<hash>.<ext> with no version in it — scripts and fonts alike — and each such name is the
// file itself, kept for a year. Every name written before still answers with today's file, marked
// no-cache: the /kit/ prefix, a component's or a service's versioned name, an old hash, /jitfonts/
// and the unhashed /_kitwork/fonts/<family>/<weight>-<subset>.woff2.
func TestEngineFilesLiveUnderOnePrefixByRole(t *testing.T) {
	tenant := componentFixture(t, enginePage())
	page := serveEngine(tenant, http.MethodGet, "/").Body.String()

	scripts := engineScriptRe.FindAllStringSubmatch(page, -1)
	fonts := engineFontRe.FindAllStringSubmatch(page, -1)
	if len(scripts) != 6 || len(fonts) < 2 {
		t.Fatalf("want the kernel, a directive, the assembly, a service and two components, and the font files; got %d scripts, %d fonts:\n%s", len(scripts), len(fonts), page)
	}
	scriptName := regexp.MustCompile(`^/_kitwork/(?:kernel|drive|(?:components|services|directives)/[a-zA-Z][a-zA-Z0-9-]*)\.[0-9a-f]{16}\.js$`)
	fontName := regexp.MustCompile(`^/_kitwork/fonts/[a-z0-9-]+-[0-9]+-[a-z-]+\.[0-9a-f]{16}\.woff2$`)
	exact := map[string]string{} // path → body
	for _, m := range scripts {
		if !scriptName.MatchString(m[1]) {
			t.Errorf("script %s is not /_kitwork/[<role folder>/]<name>.<hash>.js", m[1])
		}
	}
	for _, m := range fonts {
		if !fontName.MatchString(m[1]) {
			t.Errorf("font %s is not /_kitwork/fonts/<family>-<weight>-<subset>.<hash>.woff2", m[1])
		}
	}
	for _, m := range append(scripts, fonts...) {
		response := serveEngine(tenant, http.MethodGet, m[1])
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Header().Get("ETag") == "" {
			t.Fatalf("%s: status %d, Cache-Control %q, ETag %q — the exact name is the file, kept", m[1], response.Code, response.Header().Get("Cache-Control"), response.Header().Get("ETag"))
		}
		exact[m[1]] = response.Body.String()
		if again := serveEngine(tenant, http.MethodGet, m[1], "If-None-Match", response.Header().Get("ETag")); again.Code != http.StatusNotModified {
			t.Errorf("%s: a matching ETag is %d, want 304", m[1], again.Code)
		}
	}
	if font := fonts[0][1]; serveEngine(tenant, http.MethodGet, font).Header().Get("Content-Type") != "font/woff2" ||
		serveEngine(tenant, http.MethodGet, font).Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("%s: a font is font/woff2, fetchable anonymously", font)
	}

	byPrefix := func(prefix string) string {
		for path := range exact {
			if strings.HasPrefix(path, prefix) {
				return path
			}
		}
		t.Fatalf("the page asked for nothing under %s", prefix)
		return ""
	}
	menu, service, outfit := byPrefix("/_kitwork/components/menu."), byPrefix("/_kitwork/services/studioState."), byPrefix("/_kitwork/fonts/outfit-400-latin.")
	stale := strings.Repeat("0", 16)
	old := map[string]string{
		"/kit/" + strings.TrimPrefix(menu, "/_kitwork/"):                                        menu,
		"/kit/components/menu@1.0.0." + stale + ".js":                                           menu,
		"/_kitwork/components/menu." + stale + ".js":                                            menu,
		"/kit/services/studioState@1.0.0." + stale + ".js":                                      service,
		"/_kitwork/services/studioState@1.0.0." + stale + ".js":                                 service,
		"/jitfonts/outfit/400-latin.woff2":                                                      outfit,
		"/_kitwork/fonts/outfit/400-latin.woff2":                                                outfit,
		"/_kitwork/fonts/outfit-400-latin." + stale + ".woff2":                                  outfit,
		"/kit/kernel." + stale + ".js":                                                          byPrefix("/_kitwork/kernel."),
		"/kit/" + strings.TrimPrefix(byPrefix("/_kitwork/directives/intersect."), "/_kitwork/"): byPrefix("/_kitwork/directives/intersect."),
	}
	for path, current := range old {
		response := serveEngine(tenant, http.MethodGet, path)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" || response.Body.String() != exact[current] {
			t.Errorf("%s: status %d, Cache-Control %q — want today's %s, not kept", path, response.Code, response.Header().Get("Cache-Control"), current)
		}
	}

	// CONTROL: a name that means nothing is the engine's 404, never kept — and the method is part of
	// the name's meaning: a POST to a real file is no file.
	for _, path := range []string{
		"/_kitwork/page." + stale + ".css",
		"/_kitwork/icons." + stale + ".css",
		"/_kitwork/logo." + stale + ".css",
		"/_kitwork/fonts." + stale + ".css",
		"/_kitwork/components/nonesuch." + stale + ".js",
		"/_kitwork/javascript/kernel." + stale + ".js",
		"/_kitwork/fonts/nonesuch-400-latin." + stale + ".woff2",
		"/jitfonts/nonesuch/400-latin.woff2",
		"/kit/components/menu@9.9.9." + stale + ".js",
		"/_kitwork/",
		"/_kitwork",
	} {
		if response := serveEngine(tenant, http.MethodGet, path); response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status %d, Cache-Control %q — want the engine's 404, not kept", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	if response := serveEngine(tenant, http.MethodPost, menu); response.Code != http.StatusNotFound {
		t.Errorf("POST %s: status %d, want 404", menu, response.Code)
	}
	if strings.Contains(page, "/kit/") || strings.Contains(page, "/jitfonts/") {
		t.Errorf("the page still writes an old name:\n%s", page)
	}
}

// A tenant folder named _kitwork is never served, however the request spells its way there — case,
// a trailing dot or space, a doubled or backslashed separator, a Windows stream name — and a file on
// the tenant's disk at the very name of an engine file never shadows it. CONTROL: the same files in
// a folder of another name ARE served from the disk, so the fixture would leak if the engine did not
// answer first.
func TestATenantFolderNamedKitworkIsNeverServed(t *testing.T) {
	kernel := jitjs.KernelAsset()
	files := enginePage()
	files["localhost/_kitwork/secret.txt"] = "TENANT-SECRET"
	files["localhost/_kitwork/fonts/secret.txt"] = "TENANT-SECRET"
	files["localhost/"+strings.TrimPrefix(kernel.Path, "/")] = "TENANT-SECRET kernel"
	files["localhost/_kitworks/secret.txt"] = "TENANT-SECRET control"
	tenant := componentFixture(t, files)

	for _, path := range []string{
		"/_kitwork/secret.txt",
		"/_KITWORK/secret.txt",
		"/_Kitwork/fonts/secret.txt",
		"/_kitwork./secret.txt",
		"/_kitwork /secret.txt",
		"/_kitwork.. /secret.txt",
		"//_kitwork/secret.txt",
		`/_kitwork\secret.txt`,
		`\_kitwork\secret.txt`,
		"/./_kitwork/secret.txt",
		"/_kitwork::$INDEX_ALLOCATION/secret.txt",
		"/_kitwork:stream/secret.txt",
		"/_kitwork/fonts/secret.txt",
	} {
		response := serveEngine(tenant, http.MethodGet, path)
		if strings.Contains(response.Body.String(), "TENANT-SECRET") || response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%q: status %d, Cache-Control %q, body %.60q — want the engine's 404", path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
	if response := serveEngine(tenant, http.MethodGet, kernel.Path); response.Body.String() != string(kernel.Body) {
		t.Errorf("%s: the tenant's file at an engine name was served instead of the engine's: %.60q", kernel.Path, response.Body.String())
	}

	if response := serveEngine(tenant, http.MethodGet, "/_kitworks/secret.txt"); response.Code != http.StatusOK || response.Body.String() != "TENANT-SECRET control" {
		t.Fatalf("CONTROL: a folder of another name is the tenant's own and is served: status %d %.60q", response.Code, response.Body.String())
	}
}
