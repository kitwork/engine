package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	theme "github.com/kitwork/engine/jit/theme"
	"github.com/kitwork/engine/ui"
)

// End to end: a site that sends `script-src 'self'` still gets its anti-flash pre-paint, because
// the engine declares the script it injected. Nothing else in the policy moves. The pre-paint follows
// the site's declared dark look (a { light, dark } pair of router.colors), not the markup (the page called
// kitwork.theme.toggle() until kitwork.theme went, 26/10/2026).
func TestServedPageCarriesThePrepaintHashInItsOwnPolicy(t *testing.T) {
	const policy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'"
	tenant := buildUISiteMode(t, map[string]string{
		"localhost/router.work": `import * as router from "kitwork/router";` + "\n" +
			`router.colors({ canvas: { light: "#f6f3ee", dark: "#141110" } });` + "\n" +
			`router.guard((context) => { context.header("Content-Security-Policy", "` + policy + `"); });` + "\n",
		"localhost/view.ui":    `<view><content /></view>`,
		"localhost/content.ui": `<content><text>a page of a site that declares its dark</text></content>`,
	}, false)

	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d", recorder.Result().StatusCode)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `<script data-kitwork-generated="appearance">`) {
		t.Fatal("the page should carry the pre-paint: a dark look is declared")
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
	tenant := buildUISiteMode(t, map[string]string{
		"localhost/router.work": `import * as router from "kitwork/router";` + "\n" +
			`router.guard((context) => { context.header("Content-Security-Policy", "` + policy + `"); });` + "\n",
		"localhost/view.ui":    `<view><content /></view>`,
		"localhost/content.ui": `<content><text>no theme here</text></content>`,
	}, false)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if sent := recorder.Result().Header.Get("Content-Security-Policy"); sent != policy {
		t.Fatalf("policy changed for a page with no pre-paint:\n  got  %q\n  want %q", sent, policy)
	}
}

// A Kitwork UI page whose tabs persist carries the tabs pre-paint (ui/tabs.go), and the policy the
// site sends allows it by its own hash; the rest of the policy stays.
// CONTROL: the same page with a tabs that keeps nothing carries no tabs pre-paint, and its policy
// gets no tabs hash.
func TestServedUIPageAllowsTheTabsPrepaintInItsPolicy(t *testing.T) {
	const policy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'"
	site := func(persist string) *httptest.ResponseRecorder {
		tenant := buildUISite(t, map[string]string{
			"localhost/router.work": `import * as router from "kitwork/router";` + "\n" +
				`router.guard((context) => { context.header("Content-Security-Policy", "` + policy + `"); });` + "\n",
			"localhost/view.ui": `<view><content /></view>`,
			"localhost/content.ui": `<content>
  <tabs as="$files"` + persist + ` alt="Files" flex="row">
    <button click="select('a')">a</button>
    <button click="select('b')">b</button>
  </tabs>
  <box show="$files.selected == 'a'"><text>a</text></box>
  <box show="$files.selected == 'b'"><text>b</text></box>
</content>`,
		})
		return serveUI(t, tenant, "http://localhost/")
	}
	persisted := site(` persist="true"`)
	sent := persisted.Result().Header.Get("Content-Security-Policy")
	if !strings.Contains(persisted.Body.String(), `<script data-kitwork-generated="tabs"`) {
		t.Fatalf("the page should carry the tabs pre-paint:\n%.2000s", persisted.Body.String())
	}
	if !strings.Contains(sent, ui.TabsPrepaintHash()) {
		t.Errorf("the served policy must allow the tabs pre-paint the engine wrote (%s):\n  %s", ui.TabsPrepaintHash(), sent)
	}
	if !strings.HasPrefix(sent, "default-src 'self'; script-src 'self' ") || !strings.HasSuffix(sent, "; style-src 'self' 'unsafe-inline'") {
		t.Errorf("the rest of the site's policy must survive untouched:\n  %s", sent)
	}
	// CONTROL: nothing kept, no tabs pre-paint, no tabs hash
	plain := site("")
	if strings.Contains(plain.Body.String(), `data-kitwork-generated="tabs"`) || strings.Contains(plain.Result().Header.Get("Content-Security-Policy"), ui.TabsPrepaintHash()) {
		t.Errorf("a tabs that keeps nothing has no pre-paint and no hash:\n  %s", plain.Result().Header.Get("Content-Security-Policy"))
	}
}
