package js

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	jittheme "github.com/kitwork/engine/jit/theme"
)

// A site whose default is dark (router.appearance("dark") — the server writes
// <html data-kitwork-appearance="dark">) as a visitor's browser loads it: the engine's pre-paint
// paints the first frame, the kernel starts, and the appearance component shows the same thing. Each load starts
// from its own stored state, set by a synchronous script in <head> before the pre-paint runs:
//
//   - nothing stored: the site's dark default is shown, mode is "dark"; then system() stores "system",
//     and on a light device the page turns light — system beats the dark default;
//   - "appearance" light and the old "theme" dark: the new key wins, and a stored choice beats the
//     site's default;
//   - only the old "theme" light: a choice saved before the rename still beats the default.
func TestBrowserAppearanceSiteDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the appearance site-default browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	for _, load := range []struct {
		name, store, first, mode string
		system                   bool
	}{
		{name: "nothing stored", store: ``, first: "dark", mode: "dark", system: true},
		{name: "new key over old", store: `localStorage.setItem("appearance", "light"); localStorage.setItem("theme", "dark");`, first: "light", mode: "light"},
		{name: "old key only", store: `localStorage.setItem("theme", "light");`, first: "light", mode: "light"},
	} {
		systemStep := ``
		if load.system {
			systemStep = `
  byId("system").click(); await wait(30);
  assert(localStorage.getItem("appearance") === "system", "system() must store \"system\": " + localStorage.getItem("appearance"));
  assert(byId("mode").textContent === "system" && !html.classList.contains("dark") && html.getAttribute("data-theme") === "light" && icon() === "icon icon-moon",
    "system() on a dark-default site with a light device: " + byId("mode").textContent + " / " + html.className + " / " + icon());`
		}
		page := `<!doctype html><html data-kitwork-appearance="dark"><head><meta charset="utf-8">` +
			// a light device for the whole page, the pre-paint included, so "system" means light here
			`<script data-setup>window.matchMedia = function (query) { return { matches: false, media: query, addListener: function () {}, addEventListener: function () {} }; };` +
			`try { localStorage.clear(); ` + load.store + ` } catch (e) {}</script>` +
			`<script data-kitwork-generated="appearance"></script></head><body>
<div id="host" data-kitwork-component="appearance">
  <button id="toggle" data-kitwork-event="click: toggle()"><i id="icon" class="icon" data-kitwork-class="icon-sun: dark; icon-moon: !dark"></i></button>
  <span id="mode" data-kitwork-bind="text: mode"></span>
  <button id="system" data-kitwork-event="click: system()">system</button>
</div>
<script>
// What the pre-paint painted, read before the kernel touches anything (this script runs deferred,
// before the kernel's boot at DOMContentLoaded).
var firstPaint = (document.documentElement.classList.contains("dark") ? "dark" : "light") + "/" + document.documentElement.getAttribute("data-theme");
(async function () {
  await wait(150);
  var byId = function (id) { return document.getElementById(id); };
  var html = document.documentElement;
  var icon = function () { return byId("icon").className; };
  var first = "` + load.first + `", mode = "` + load.mode + `";
  assert(firstPaint === first + "/" + first, "the pre-paint's first frame: " + firstPaint + ", want " + first);
  assert(window.kit.theme === window.kit.appearance, "kit.theme must be the same object as kit.appearance");
  assert(byId("mode").textContent === mode, "mode after the kernel started: " + byId("mode").textContent + ", want " + mode);
  assert(icon() === (first === "dark" ? "icon icon-sun" : "icon icon-moon") && window.kit.appearance.dark === (first === "dark"),
    "the component shows what the pre-paint painted: " + icon() + " / dark " + window.kit.appearance.dark);` + systemStep + `
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script></body></html>`
		page = jittheme.Render(page)
		if !strings.Contains(page, `<script data-kitwork-generated="appearance">(function(){`) {
			t.Fatalf("%s: the pre-paint did not fill its marker:\n%s", load.name, page)
		}
		page = strings.Replace(page, "<script>", "<script>"+proofScriptHead, 1)
		delivered := deliverTestPage(t, page, nil, nil)
		output, err := serveAppearancePage(t, browser, delivered)
		if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
			continue
		}
		if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
			end := i + 400
			if end > len(output) {
				end = len(output)
			}
			t.Fatalf("%s: appearance site-default proof failed: %s", load.name, output[i:end])
		}
		t.Fatalf("%s: appearance site-default proof did not pass (err=%v):\n%.2000s", load.name, err, output)
	}
}

func serveAppearancePage(t *testing.T, browser string, delivered *testPage) ([]byte, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if delivered.serveFiles(response, request) {
			return
		}
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(delivered.html))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"),
		"--virtual-time-budget=8000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	return exec.CommandContext(ctx, browser, args...).CombinedOutput()
}

// component="appearance" as the engine writes it (grouped data-kitwork-* attributes): the icon swaps on dark,
// a second host reached through its alias agrees, and under "system" dark is what the operating
// system shows. The theme component on the same page still reads resolved off kit.theme — the same
// object as kit.appearance — so both views always agree.
func TestBrowserAppearanceComponentShowsDark(t *testing.T) {
	runBrowserProof(t, "appearance", nil, `
<div id="host" data-kitwork-component="appearance">
  <button id="toggle" data-kitwork-event="click: toggle()">
    <i id="icon" class="icon" data-kitwork-class="icon-sun: dark; icon-moon: !dark"></i>
    <span id="mode" data-kitwork-bind="text: mode"></span>
  </button>
</div>
<div id="second" data-kitwork-component="appearance: $look">
  <span id="shown" data-kitwork-bind="text: dark ? 'dark' : 'light'"></span>
</div>
<button id="dark" data-kitwork-event="click: $look.set('dark')">dark</button>
<button id="system" data-kitwork-event="click: $look.system()">system</button>
<b id="legacy" data-kit-component="theme" data-kit-text="resolved"></b>
<script>
(async function () {
  await wait(150);
  var byId = function (id) { return document.getElementById(id); };
  var html = document.documentElement;
  var icon = function () { return byId("icon").className; };

  assert(window.kit.theme === window.kit.appearance, "kit.theme must be the same object as kit.appearance");
  // Nothing stored, a light OS (headless default): system, and not dark.
  assert(byId("mode").textContent === "system" && icon() === "icon icon-moon" && byId("shown").textContent === "light",
    "first paint: " + byId("mode").textContent + " / " + icon() + " / " + byId("shown").textContent);
  // resolved is not the appearance component's (it is the theme component's)
  var scope = window.kit.scopeFor(byId("icon"));
  assert(scope.dark === false && typeof scope.mode === "string" && typeof scope.resolved !== "string",
    "the appearance scope holds mode and dark, not resolved: " + typeof scope.resolved);
  assert(byId("legacy").textContent === "light", "control: the theme component still reads resolved: " + byId("legacy").textContent);

  byId("dark").click(); await wait(30);
  assert(html.classList.contains("dark") && icon() === "icon icon-sun" && byId("mode").textContent === "dark" && byId("shown").textContent === "dark",
    "set('dark') through the alias: " + icon() + " " + byId("mode").textContent + " " + byId("shown").textContent);
  assert(localStorage.getItem("appearance") === "dark", "the choice is stored under appearance: " + localStorage.getItem("appearance"));
  assert(byId("legacy").textContent === "dark", "the theme component agrees: " + byId("legacy").textContent);

  byId("toggle").click(); await wait(30);
  assert(!html.classList.contains("dark") && icon() === "icon icon-moon" && byId("mode").textContent === "light" && byId("shown").textContent === "light",
    "toggle() in its host, and the second host agrees: " + icon() + " " + byId("shown").textContent);

  // system on a dark OS: dark is what is shown, not what was chosen.
  var realMatchMedia = window.matchMedia;
  window.matchMedia = function (query) { return { matches: query === "(prefers-color-scheme: dark)", media: query }; };
  byId("system").click(); await wait(30);
  window.matchMedia = realMatchMedia;
  assert(byId("mode").textContent === "system" && html.classList.contains("dark") && icon() === "icon icon-sun" && byId("shown").textContent === "dark",
    "system() on a dark OS: " + byId("mode").textContent + " " + icon() + " " + byId("shown").textContent);

  // A choice saved before the rename, under "theme" alone, is still the visitor's.
  localStorage.removeItem("appearance");
  localStorage.setItem("theme", "light");
  window.kit.render(); await wait(30);
  assert(byId("mode").textContent === "light", "the old theme key is still read: " + byId("mode").textContent);
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// What the server's dev check knows the appearance component holds — read off its source. A page
// that still writes {{ resolved }} inside component="appearance" is told, because resolved is not in the list.
func TestAppearanceComponentDeclaresModeAndDark(t *testing.T) {
	names, ok := ComponentNames(nil)("appearance")
	if !ok {
		t.Fatal("the appearance component's names could not be read")
	}
	for _, want := range []string{"mode", "dark", "toggle", "set", "system"} {
		if !slices.Contains(names, want) {
			t.Errorf("appearance must declare %q, got %v", want, names)
		}
	}
	if slices.Contains(names, "resolved") {
		t.Errorf("resolved is gone from appearance, got %v", names)
	}
	// control: the theme component keeps the older contract
	legacy, ok := ComponentNames(nil)("theme")
	if !ok || !slices.Contains(legacy, "resolved") || !slices.Contains(legacy, "mode") {
		t.Errorf("the theme component must keep mode and resolved, got %v (%v)", legacy, ok)
	}
}
