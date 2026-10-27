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

// enginePage is a Kitwork UI site whose page needs every kind of script and font the engine serves
// for a page: the kernel and Drive, a directive (intersect-enter), catalogue components (tabs, a
// dropdown, and the progressbar and reader Drive brings) and a vendored font (router.fonts). Until
// 27/10/2026 it was a template page that also called a native service from its markup; no .ui page
// names one (markup never calls kit, and no catalogue component calls a service), so serviceFile
// asks the engine for the service's name.
func enginePage() map[string]string {
	return uiPage(`router.fonts({ body: "Outfit" });`,
		`<content><tabs><button click="select('a')">A</button><button click="select('b')">B</button></tabs><dropdown><button click="open = !open" intersect-enter="open = true">menu</button></dropdown></content>`)
}

// enginePageScripts is what enginePage asks for, by role and name.
var enginePageScripts = []string{"kernel", "drive", "directives/intersect", "components/dropdown", "components/progressbar", "components/reader", "components/tabs"}

// serviceFile is the native service studioState's file as the engine names it today, from the files
// it would give a page that called the service.
func serviceFile(t *testing.T) string {
	t.Helper()
	for _, asset := range jitjs.PageAssets(`<button data-kitwork-event="click: kit.studioState.clearHistory()">x</button>`, jitjs.Options{}) {
		if strings.HasPrefix(asset.Path, "/_kitwork/services/studioState.") {
			return asset.Path
		}
	}
	t.Fatal("the engine gives no studioState file to a page that calls it")
	return ""
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
// file itself, kept for a year. A name written before that still means something answers with
// today's file, marked no-cache: an old hash, /jitfonts/ and the unhashed
// /_kitwork/fonts/<family>/<weight>-<subset>.woff2. The /kit/ prefix and the names with a version
// (menu@1.0.0, studioState@1.0.0) were template pages' and answer nothing since 26/10/2026.
func TestEngineFilesLiveUnderOnePrefixByRole(t *testing.T) {
	tenant := identitySite(t, enginePage())
	page := serveEngine(tenant, http.MethodGet, "/").Body.String()

	scripts := engineScriptRe.FindAllStringSubmatch(page, -1)
	fonts := engineFontRe.FindAllStringSubmatch(page, -1)
	var named []string
	for _, m := range scripts {
		named = append(named, strings.TrimPrefix(strings.SplitN(m[1], ".", 2)[0], "/_kitwork/"))
	}
	if strings.Join(named, " ") != strings.Join(enginePageScripts, " ") || len(fonts) < 2 {
		t.Fatalf("want the scripts %v and the font files; got %v, %d fonts:\n%s", enginePageScripts, named, len(fonts), page)
	}
	// the service no page here names is served by its name all the same
	scripts = append(scripts, []string{"", serviceFile(t)})
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
	tabs, service, outfit := byPrefix("/_kitwork/components/tabs."), byPrefix("/_kitwork/services/studioState."), byPrefix("/_kitwork/fonts/outfit-400-latin.")
	kernel, intersect := byPrefix("/_kitwork/kernel."), byPrefix("/_kitwork/directives/intersect.")
	stale := strings.Repeat("0", 16)
	old := map[string]string{
		"/_kitwork/components/tabs." + stale + ".js":           tabs,
		"/_kitwork/services/studioState." + stale + ".js":      service,
		"/_kitwork/kernel." + stale + ".js":                    kernel,
		"/_kitwork/directives/intersect." + stale + ".js":      intersect,
		"/jitfonts/outfit/400-latin.woff2":                     outfit,
		"/_kitwork/fonts/outfit/400-latin.woff2":               outfit,
		"/_kitwork/fonts/outfit-400-latin." + stale + ".woff2": outfit,
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
		"/_kitwork/components/tabs@1.1.0." + stale + ".js", // a version in the name: answered until 26/10/2026
		"/_kitwork/services/studioState@1.0.0." + stale + ".js",
		"/_kitwork/",
		"/_kitwork",
	} {
		if response := serveEngine(tenant, http.MethodGet, path); response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status %d, Cache-Control %q — want the engine's 404, not kept", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	// the /kit/ prefix is no longer the engine's (26/10/2026): it is a path of the site's like any
	// other, and the site has none, so it is a 404, never the file
	for _, path := range []string{"/kit/" + strings.TrimPrefix(tabs, "/_kitwork/"), "/kit/kernel." + stale + ".js"} {
		if response := serveEngine(tenant, http.MethodGet, path); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "kit.component") {
			t.Errorf("%s: status %d — want a 404, not today's file", path, response.Code)
		}
	}
	if response := serveEngine(tenant, http.MethodPost, tabs); response.Code != http.StatusNotFound {
		t.Errorf("POST %s: status %d, want 404", tabs, response.Code)
	}
	if strings.Contains(page, "/kit/") || strings.Contains(page, "/jitfonts/") {
		t.Errorf("the page still writes an old name:\n%s", page)
	}
}

// A tenant folder named _kitwork is never served, however the request spells its way there — case,
// a trailing dot or space, a doubled or backslashed separator, a Windows stream name — and a file on
// the tenant's disk at the very name of an engine file never shadows it, even where the site mounts
// that folder at /_kitwork itself (router.assets). CONTROL: the same files in a folder of another
// name, mounted the same way, ARE served from the disk, so the fixture would leak if the engine did
// not answer first.
func TestATenantFolderNamedKitworkIsNeverServed(t *testing.T) {
	kernel := jitjs.KernelAsset()
	files := enginePage()
	files["localhost/router.work"] += "\n" + `router.assets("_kitwork", "_kitwork");` + "\n" + `router.assets("_kitworks", "_kitworks");`
	files["localhost/_kitwork/secret.txt"] = "TENANT-SECRET"
	files["localhost/_kitwork/fonts/secret.txt"] = "TENANT-SECRET"
	files["localhost/"+strings.TrimPrefix(kernel.Path, "/")] = "TENANT-SECRET kernel"
	files["localhost/_kitworks/secret.txt"] = "TENANT-SECRET control"
	tenant := identitySite(t, files)

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
