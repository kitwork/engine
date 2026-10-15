package work

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	jitjs "github.com/kitwork/engine/jit/js"
)

var kitScriptRe = regexp.MustCompile(`src="?(/(?:kit|_kitwork)/[^"\s>]+)`)

// pageJavaScript is every script the home page asks for under the engine's prefix (/_kitwork/, or the
// older /kit/), fetched through the tenant and joined: what a visitor's browser actually downloads
// for that page.
func pageJavaScript(t *testing.T, tenant *Tenant) string {
	t.Helper()
	get := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return recorder
	}
	page := get("/").Body.String()
	var all strings.Builder
	for _, match := range kitScriptRe.FindAllStringSubmatch(page, -1) {
		response := get(match[1])
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", match[1], response.Code)
		}
		all.WriteString(response.Body.String())
	}
	if all.Len() == 0 {
		t.Fatalf("the page asks for no file of the engine's:\n%s", page)
	}
	return all.String()
}

// A site that never asked for Drive has no use for the navigator or the DOM morph it drives —
// nothing else in the runtime calls kit.morph. Shipping them anyway is the same waste as shipping a
// component nobody named, and it is the largest single piece of the runtime.
func TestDriveIsNotShippedToSitesThatDidNotAskForIt(t *testing.T) {
	serve := func(t *testing.T, router string) string {
		t.Helper()
		tenant, _ := writeTestSite(t, router, `<main><b data-kit-text="'x'">x</b></main>`)
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		return pageJavaScript(t, tenant)
	}

	gz := func(s string) int {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return b.Len()
	}

	without := serve(t, "")
	with := serve(t, `router.reload(false);`)

	// Marker strings from the CODE, not from comments: the production asset is minified, so a comment
	// would prove nothing. X-Kitwork-Hydrate is the header Drive fetches with; kit.morph is what it
	// drives, and kit.internal.clientOwned is the set only the two of them keep (the kernel spells
	// TEXTAREA too since it reads data-kitwork-bind's two-way value:, so that word is no marker).
	for _, mark := range []string{"X-Kitwork-Hydrate", "clientOwned"} {
		if strings.Contains(without, mark) {
			t.Fatalf("a site that never asked for Drive still received %q", mark)
		}
		if !strings.Contains(with, mark) {
			t.Fatalf("router.reload(false) should ship %q", mark)
		}
	}

	// The kernel itself must be there either way — this is about the navigator, not the runtime.
	for _, mark := range []string{"data-kit-scope", "DOMContentLoaded"} {
		if !strings.Contains(without, mark) || !strings.Contains(with, mark) {
			t.Fatalf("the kernel and boot must be in both assets (%q)", mark)
		}
	}

	saved := gz(with) - gz(without)
	t.Logf("page JavaScript gzip: %d with Drive, %d without — %d bytes saved on every page", gz(with), gz(without), saved)
	if saved < 2000 {
		t.Fatalf("expected Drive to be worth thousands of bytes on a minified asset, measured %d", saved)
	}
}

// The native bridge and the 18 capability proxies that ride it only reach anything inside a desktop
// or mobile shell. In an ordinary browser window there is no WebView handle, so every one of those
// calls rejects with UNSUPPORTED — and the app's own manifest already says which world it is in.
func TestNativeBridgeGoesOnlyToAppsWithAShell(t *testing.T) {
	serve := func(t *testing.T) string {
		t.Helper()
		tenant, _ := writeTestSite(t, "", `<main><b data-kit-text="'x'">x</b></main>`)
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		return pageJavaScript(t, tenant)
	}

	// The engine sets both from the manifest at boot (kitwork.go): the tenant's flag and the one the
	// per-file delivery reads.
	previous, previousFiles := NativeShell, jitjs.Native
	defer func() { NativeShell, jitjs.Native = previous, previousFiles }()

	NativeShell, jitjs.Native = false, false
	web := serve(t)
	NativeShell, jitjs.Native = true, true
	shell := serve(t)

	// Markers from the CODE: the minifier deletes comments and renames variables.
	for _, mark := range []string{"messageHandlers", "requires a native bridge"} {
		if strings.Contains(web, mark) {
			t.Fatalf("a web-only app still received %q", mark)
		}
		if !strings.Contains(shell, mark) {
			t.Fatalf("a desktop app should receive %q", mark)
		}
	}
	// The kernel still carries the two facts the bridge used to own.
	for _, mark := range []string{"KitworkError", "isNative"} {
		if !strings.Contains(web, mark) {
			t.Fatalf("a web-only app must still have %q from the kernel", mark)
		}
	}
	t.Logf("page JavaScript gzip: %d with a shell, %d web-only — %d bytes saved", gzipLen(shell), gzipLen(web), gzipLen(shell)-gzipLen(web))
}

func gzipLen(s string) int {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Len()
}
