package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	theme "github.com/kitwork/engine/jit/theme"
)

// End to end: a site that sends `script-src 'self'` still gets its anti-flash pre-paint, because
// the engine declares the script it injected. Nothing else in the policy moves.
func TestServedPageCarriesThePrepaintHashInItsOwnPolicy(t *testing.T) {
	const policy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'"
	tenant := componentFixture(t, map[string]string{
		"localhost/filesystem.kitwork": "",
		"localhost/index.kitwork.html": `<!doctype html><html><head></head><body>{{ @page }}</body></html>`,
		"localhost/page.kitwork.html":  `<button data-kit-click="kit.theme.toggle()">theme</button>`,
		"localhost/router.kitwork.js": `import { router } from "kitwork";` + "\n" +
			`router.css({ darkMode: ["class"] });` + "\n" +
			`router.guard((ctx, response) => { response.header("Content-Security-Policy", "` + policy + `"); });`,
	})

	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d", recorder.Result().StatusCode)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `<script data-kitwork-jit="theme">`) {
		t.Fatal("the page should carry the pre-paint: darkMode is declared")
	}
	sent := recorder.Result().Header.Get("Content-Security-Policy")
	if !strings.Contains(sent, theme.PrepaintHash()) {
		t.Fatalf("the served policy must allow the script the engine injected:\n  %s", sent)
	}
	if !strings.Contains(sent, "style-src 'self' 'unsafe-inline'") || !strings.Contains(sent, "default-src 'self'") {
		t.Fatalf("the rest of the site's policy must survive untouched:\n  %s", sent)
	}
}

// A page with no pre-paint keeps the policy the site wrote, byte for byte.
func TestServedPageWithoutPrepaintKeepsItsPolicy(t *testing.T) {
	const policy = "default-src 'self'; script-src 'self'"
	tenant := componentFixture(t, map[string]string{
		"localhost/filesystem.kitwork": "",
		"localhost/index.kitwork.html": `<!doctype html><html><head></head><body>{{ @page }}</body></html>`,
		"localhost/page.kitwork.html":  `<p>no theme here</p>`,
		"localhost/router.kitwork.js": `import { router } from "kitwork";` + "\n" +
			`router.guard((ctx, response) => { response.header("Content-Security-Policy", "` + policy + `"); });`,
	})
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if sent := recorder.Result().Header.Get("Content-Security-Policy"); sent != policy {
		t.Fatalf("policy changed for a page with no pre-paint:\n  got  %q\n  want %q", sent, policy)
	}
}
