package js

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// The verb system is gone; the sites that used it now say the same things with the kernel's own
// grammar and its components. This drives exactly those patterns in a real headless browser, on
// the composed /kit.js plus the modules jit/js would inject for such a page:
//
//	toggle  → data-kit-scope="navOpen: false" + data-kit-click="navOpen = !navOpen" + data-kit-show
//	tab     → data-kit-scope="tab: 'a'" + bind:aria-selected / bind:data-state / show
//	dialog  → data-kit-element="search" + $element.search.showModal() / .close()
//	copy    → data-kit-component="clipboard" + copy('…') + data-kit-class="copied ? 'is-copied' : ''"
//	theme   → data-kit-click="kit.theme.toggle()"
func TestBrowserMigratedPatternsRunOnTheKernel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping kernel migration browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html>
<html lang="en" data-kit-app="proof"><head><meta charset="utf-8"><title>kernel migration</title></head><body>
  <header data-kit-scope="navOpen: false">
    <button id="menu" type="button" data-kit-click="navOpen = !navOpen" data-kit-bind:aria-expanded="navOpen">menu</button>
    <nav id="mobile-nav" hidden data-kit-show="navOpen">links</nav>
  </header>
  <section data-kit-scope="tab: 'a'">
    <button id="tab-a" role="tab" data-kit-click="tab = 'a'" data-kit-bind:aria-selected="tab === 'a'" data-kit-bind:data-state="tab === 'a' ? 'active' : 'inactive'">A</button>
    <button id="tab-b" role="tab" data-kit-click="tab = 'b'" data-kit-bind:aria-selected="tab === 'b'" data-kit-bind:data-state="tab === 'b' ? 'active' : 'inactive'">B</button>
    <div id="panel-a" role="tabpanel" data-kit-show="tab === 'a'">panel a</div>
    <div id="panel-b" role="tabpanel" hidden data-kit-show="tab === 'b'">panel b</div>
  </section>
  <button id="open" type="button" data-kit-click="$element.search.showModal()">search</button>
  <dialog id="site-search" data-kit-element="search"><button id="close" type="button" data-kit-click="$element.search.close()">x</button></dialog>
  <button id="copy" type="button" data-kit-component="clipboard" data-kit-click="copy('hello kitwork')" data-kit-class="copied ? 'is-copied' : ''">copy</button>
  <button id="theme" type="button" data-kit-click="kit.theme.toggle()">theme</button>
  <script>
  // Headless Chrome grants no clipboard permission; the proof is the kernel wiring (expression →
  // component method → async state write → repaint), so the platform call is recorded, not made.
  window.__copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) { window.__copied.push(text); return Promise.resolve(); } } });
  </script>
  <script src="/kit.js"></script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
    function assert(condition, message) { if (!condition) fail(message); }
    function tick() { return new Promise(function (resolve) { setTimeout(resolve, 20); }); }
    var byId = function (id) { return document.getElementById(id); };
    await tick();

    byId("menu").click(); await tick();
    assert(!byId("mobile-nav").hidden && byId("menu").getAttribute("aria-expanded") === "true", "toggle: nav should open and aria-expanded follow");
    byId("menu").click(); await tick();
    assert(byId("mobile-nav").hidden, "toggle: nav should close again");

    byId("tab-b").click(); await tick();
    assert(byId("panel-b").hidden === false && byId("panel-a").hidden === true, "tab: panel b shows, panel a hides");
    assert(byId("tab-b").getAttribute("aria-selected") === "true" && byId("tab-b").getAttribute("data-state") === "active", "tab: aria-selected/data-state move to b");
    assert(byId("tab-a").getAttribute("aria-selected") === "false" && byId("tab-a").getAttribute("data-state") === "inactive", "tab: a is inactive");

    byId("open").click(); await tick();
    assert(byId("site-search").open === true, "dialog: $element.search.showModal() should open the dialog");
    byId("close").click(); await tick();
    assert(byId("site-search").open === false, "dialog: $element.search.close() should close it");

    byId("copy").click(); await tick(); await tick();
    assert(window.__copied[0] === "hello kitwork", "clipboard: copy('…') should reach the platform with the literal");
    assert(byId("copy").classList.contains("is-copied"), "clipboard: the async copied write should repaint is-copied");

    var dark = root.classList.contains("dark");
    byId("theme").click(); await tick();
    assert(root.classList.contains("dark") !== dark, "theme: kit.theme.toggle() should flip the dark class");

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`
	rendered := Render(page)
	if !strings.Contains(rendered, "components=component%3Aclipboard") {
		t.Fatalf("Render should inject the clipboard component for this page:\n%s", rendered)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(hydrate.Runtime() + "\n" + ModulesJS(ModuleKeys(scanModules(page, nil)))))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=5000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 300
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("kernel migration proof failed: %s", output[i:end])
	}
	t.Fatalf("kernel migration proof did not pass (err=%v):\n%.2000s", err, output)
}

func findHeadlessBrowser() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off", "no":
		return ""
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
