package js

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var deliveredSrc = regexp.MustCompile(`<script data-kitwork-generated="([a-z]+)" src="([^"]+)" defer></script>`)

// assetPath is what a failure message says about an asset: its name, not its bytes.
func assetPath(asset *Asset) string {
	if asset == nil {
		return "<nil>"
	}
	return asset.Path
}

func deliveredFiles(t *testing.T, html string) (roles, paths []string) {
	t.Helper()
	for _, m := range deliveredSrc.FindAllStringSubmatch(html, -1) {
		roles = append(roles, m[1])
		paths = append(paths, m[2])
	}
	return roles, paths
}

// A page gets the kernel, Drive when the site asked for it, then one file per component it uses —
// kernel first, because defer runs them in document order and the rest register into it; among the
// components the order is the sorted key order, and any order works. data-kit-remember is no longer
// anything (02/10), nor is any hand-written data-kit-* (26/10/2026), and it brings no file.
func TestDeliverGivesEachPartAFileOfItsOwn(t *testing.T) {
	page := `<html><head></head><body><nav data-kitwork-component="toast"></nav><div data-kitwork-component="tabs"></div><p data-kit-remember="theme"></p></body></html>`
	out := Deliver(page, Options{Drive: true})
	roles, paths := deliveredFiles(t, out)
	if strings.Join(roles, ",") != "runtime,drive,component,component" {
		t.Fatalf("roles in order: got %v\n%s", roles, out)
	}
	if !strings.HasPrefix(paths[0], "/_kitwork/kernel.") || !strings.HasPrefix(paths[1], "/_kitwork/drive.") ||
		!strings.HasPrefix(paths[2], "/_kitwork/components/tabs.") ||
		!strings.HasPrefix(paths[3], "/_kitwork/components/toast.") {
		t.Fatalf("paths: %v", paths)
	}
	// A component's name on the wire carries no version; the hash says which bytes.
	for _, path := range paths {
		if strings.Contains(path, "@") {
			t.Errorf("%s: a delivered name carries a version", path)
		}
	}
	if strings.Contains(out, "data-kitwork-drive") {
		t.Error("no flag is written for Drive: its file being delivered is the answer")
	}
	if strings.Contains(out, "?components=") {
		t.Error("the per-page query form is gone from delivered pages")
	}
	if i := strings.Index(out, `<script data-kitwork-generated`); i > strings.Index(out, "</head>") {
		t.Error("the files belong in <head>")
	}
	// CONTROL: without Drive, no Drive file.
	if roles, _ := deliveredFiles(t, Deliver(page, Options{})); strings.Join(roles, ",") != "runtime,component,component" {
		t.Errorf("a site that did not ask for Drive does not get its file, got %v", roles)
	}
}

func TestDeliverOnlyWhereTheGrammarIsUsed(t *testing.T) {
	grammar := `<html><head></head><body><div data-kitwork-scope="open: false"><button data-kitwork-event="click: open = !open">x</button></div></body></html>`
	if roles, _ := deliveredFiles(t, Deliver(grammar, Options{})); strings.Join(roles, ",") != "runtime" {
		t.Errorf("grammar with no component gets the kernel alone, got %v", roles)
	}
	for name, page := range map[string]string{
		"plain":   `<html><head></head><body><p>hello</p></body></html>`,
		"example": `<html><head></head><body><pre><code>&lt;b data-kitwork-bind="text: x"&gt;</code></pre></body></html>`,
	} {
		if out := Deliver(page, Options{}); out != page {
			t.Errorf("%s: a page that uses no grammar must be returned untouched, got %s", name, out)
		}
		// A site that moves between pages without a reload does so from EVERY page: a page with no
		// grammar still gets the kernel and Drive, or its links would reload and drop what the swap keeps.
		if roles, _ := deliveredFiles(t, Deliver(page, Options{Drive: true})); strings.Join(roles, ",") != "runtime,drive" {
			t.Errorf("%s: on a Drive site a page with no grammar gets the kernel and Drive, got %v", name, roles)
		}
	}
	// A page that already carries the engine's runtime tag is left alone.
	already := `<html><head><script data-kitwork-generated="runtime" src="/kit.js" defer></script></head><body><div data-kitwork-component="tabs"></div></body></html>`
	if Deliver(already, Options{}) != already {
		t.Error("a second kernel would load")
	}
}

// The name is a promise about the bytes: the path round-trips through AssetAt, and a different
// setting is a different name. An old hash still finds today's file, flagged as not its name.
func TestAssetsAreNamedByTheirBytes(t *testing.T) {
	defer func(minify bool) { Minify = minify }(Minify)
	Minify = false
	page := `<html><head></head><body><div data-kitwork-component="tabs"></div></body></html>`
	_, paths := deliveredFiles(t, Deliver(page, Options{Drive: true}))
	for _, path := range paths {
		if asset, exact := AssetAt(path); asset == nil || !exact {
			t.Fatalf("%s does not resolve back to its file", path)
		}
	}
	tabs, _ := AssetAt(paths[2])
	if !bytes.HasPrefix(tabs.Body, []byte(";(function () {")) || !bytes.Contains(tabs.Body, []byte(`component("tabs", tabsDef)`)) {
		t.Errorf("a component file is the component, in a scope of its own:\n%.200s", tabs.Body)
	}
	Minify = true
	if KernelAsset().Path == paths[0] {
		t.Error("minified bytes kept the readable file's name — a cache would serve the wrong one")
	}
	Minify = false

	// An old hash: today's kernel, but not under its own name.
	wrongHash := paths[0][:len(paths[0])-len(".js")-hashLength] + strings.Repeat("0", hashLength) + ".js"
	if asset, exact := AssetAt(wrongHash); asset == nil || exact || asset.Path != paths[0] {
		t.Errorf("%s: want today's kernel, not exact; got %s exact=%t", wrongHash, assetPath(asset), exact)
	}
	// Paths that name nothing, under the prefix and the one before it.
	for _, bad := range []string{
		"/_kitwork/kernel.js",
		"/_kitwork/components/../kernel." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/components/tabs@9.9.9." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/components/tabs@v1.1.0." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/components/nonesuch." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/capabilities/remember." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/fonts/kernel." + strings.Repeat("a", hashLength) + ".js",
		"/kit/kernel.js",
		"/kit/components/tabs@9.9.9." + strings.Repeat("a", hashLength) + ".js",
		"/kit/components/nonesuch." + strings.Repeat("a", hashLength) + ".js",
		"/kit.js",
		"/other/kernel." + strings.Repeat("a", hashLength) + ".js",
	} {
		if asset, _ := AssetAt(bad); asset != nil {
			t.Errorf("%s must not resolve", bad)
		}
	}
}

// Everything the engine serves is under /_kitwork/, and an old hash of any of its names still finds
// today's file — never as its exact name, so the server marks it not to be kept. The names written
// before 11/10/2026 — the /kit/ prefix, a component or service name carrying its version — were
// answered for template pages and went with them on 26/10/2026: they name nothing now. CONTROL: each
// file's current name is exact.
func TestAnOldHashFindsTodaysFile(t *testing.T) {
	defer func(minify bool) { Minify = minify }(Minify)
	Minify = false
	page := `<html><head></head><body><div data-kitwork-component="tabs"></div><b data-kitwork-event="intersect-enter: x(); click: kit.studioState.clearHistory()"></b></body></html>`
	roles, paths := deliveredFiles(t, Deliver(page, Options{Drive: true}))
	if strings.Join(roles, ",") != "runtime,drive,directive,service,service,component" {
		t.Fatalf("roles: %v %v", roles, paths)
	}
	// Every name is <role>.<hash>.js, one-of-a-kind files at the top and the rest in a folder by
	// role — and no name carries a version, whatever the role: studioState has a version of its own
	// in the engine.
	shape := regexp.MustCompile(`^/_kitwork/(?:(?:components|services|directives)/[a-zA-Z][a-zA-Z0-9-]*|kernel|drive)\.[0-9a-f]{16}\.js$`)
	for _, path := range paths {
		if !shape.MatchString(path) {
			t.Errorf("%s is not /_kitwork/[<role folder>/]<name>.<hash>.js with no version", path)
		}
	}
	stale := strings.Repeat("0", hashLength)
	for _, path := range paths {
		current, exact := AssetAt(path)
		if current == nil || !exact || !strings.HasPrefix(path, "/_kitwork/") {
			t.Fatalf("%s: the current name must be exact and under /_kitwork/ (exact=%t)", path, exact)
		}
		old := path[:len(path)-len(".js")-hashLength] + stale + ".js"
		if asset, exact := AssetAt(old); asset != current || exact {
			t.Errorf("%s: an old hash must answer with today's file %s, not as its name; got %s exact=%t", old, path, assetPath(asset), exact)
		}
		if asset, _ := AssetAt("/kit/" + strings.TrimPrefix(path, "/_kitwork/")); asset != nil {
			t.Errorf("%s: the /kit/ prefix went on 26/10/2026, got %s", path, asset.Path)
		}
	}
	for _, gone := range []string{
		"/kit/components/tabs@1.1.0." + stale + ".js",
		"/kit/kernel." + stale + ".js",
		"/_kitwork/components/tabs@1.1.0." + stale + ".js",
		"/_kitwork/components/tabs@v1.1.0." + stale + ".js",
		"/_kitwork/services/studioState@" + serviceVersion("studioState") + "." + stale + ".js",
	} {
		if asset, _ := AssetAt(gone); asset != nil {
			t.Errorf("%s: a name of before 11/10/2026 is no longer answered, got %s", gone, asset.Path)
		}
	}
}

// A catalogue component's file registers its bare name and nothing else: a versioned name is not
// something the wire knows (11/10/2026). (A template page's host that wrote a version, which the kernel
// read without it, went on 26/10/2026.)
func TestComponentFilesRegisterOnlyTheirName(t *testing.T) {
	registration := regexp.MustCompile(`\bkit(?:work)?\.component\(\s*["']([^"']+)["']`)
	entries, err := jsFS.ReadDir("components")
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, entry := range entries {
		versions, _ := jsFS.ReadDir("components/" + entry.Name())
		for _, version := range versions {
			source, err := jsFS.ReadFile("components/" + entry.Name() + "/" + version.Name())
			if err != nil {
				t.Fatal(err)
			}
			files++
			var names []string
			for _, m := range registration.FindAllStringSubmatch(string(source), -1) {
				names = append(names, m[1])
			}
			if len(names) != 1 || names[0] != entry.Name() {
				t.Errorf("components/%s/%s registers %v, want exactly [%s]", entry.Name(), version.Name(), names, entry.Name())
			}
		}
	}
	if files < 12 { // twelve since 26/10/2026, one file each
		t.Fatalf("read %d component files: the catalogue was not where this test looks", files)
	}
}

// A name that carries a version is not delivered (26/10/2026): the engine never writes one, and the
// catalogue ships the latest of each component under its bare name. CONTROL: the bare name ships.
func TestAVersionWrittenInAHostIsNotDelivered(t *testing.T) {
	page := func(host string) string { return `<html><head></head><body>` + host + `</body></html>` }
	if _, paths := deliveredFiles(t, Deliver(page(`<div data-kitwork-component="toast"></div>`), Options{})); len(paths) != 2 ||
		!strings.HasPrefix(paths[1], "/_kitwork/components/toast.") {
		t.Fatalf("CONTROL: the bare name ships its file, got %v", paths)
	}
	for _, host := range []string{
		`<div data-kitwork-component="toast@1.1.0"></div>`,
		`<div data-kitwork-component="toast@v1.1.0"></div>`,
		`<div data-kitwork-component="toast@1.1.0: $toast"></div>`,
	} {
		if roles, paths := deliveredFiles(t, Deliver(page(host), Options{})); strings.Join(roles, ",") != "runtime" {
			t.Errorf("%s: a versioned name brings the kernel alone, got %v %v", host, roles, paths)
		}
	}
}

// The order that matters only in a browser: the kernel arrives as the FIRST of several deferred
// files, so it must not start until the component files after it have registered — otherwise every
// component on the page is first reported missing (this fails against the old boot.js). And Drive,
// moving to a page that names a component this one never loaded, loads that file and the component
// works after the swap.
//
// What this cannot show is Drive WAITING for that file before it swaps: under --virtual-time-budget
// the headless browser holds its tasks while a request is pending, so even a deliberately slow file
// has arrived before the swap runs, with or without the wait. The wait is there for a real network.
func TestBrowserSeparateFilesBootInOrderAndDriveLoadsWhatIsNew(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping delivery browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	hook := `<script>window.__errors = []; (function () { var original = console.error; console.error = function (e) { window.__errors.push(String(e && e.message || e)); original.apply(console, arguments); }; })();</script>`
	first := Deliver(`<!doctype html><html><head>`+hook+`</head><body><main>
<div data-kitwork-component="dropdown"><b id="open" data-kitwork-bind="text: open === false ? 'seeded' : 'unseeded'"></b></div>
<div data-kitwork-component="toast"><b id="limit" data-kitwork-bind="text: limit"></b></div>
<a id="go" href="/tabs">tabs</a>
</main><script>
document.addEventListener("DOMContentLoaded", function () { (async function () {
`+proofScriptHead+`
  await wait(100);
  assert(document.getElementById("open").textContent === "seeded" && document.getElementById("limit").textContent === "3",
    "the component files registered before the first paint: " + document.getElementById("open").textContent + " / " + document.getElementById("limit").textContent);
  assert(window.__errors.length === 0, "nothing was reported missing on load: " + window.__errors.join(" | "));
  document.getElementById("go").click();
  for (var i = 0; i < 40 && !document.getElementById("two"); i++) await wait(100);
  await wait(150);
  var tabs = document.querySelector('[data-kitwork-component="tabs"]');
  assert(tabs, "Drive swapped in the tabs page");
  assert(document.querySelector('script[src^="/_kitwork/components/tabs."]'), "the tabs' file was added to <head>");
  focusIn(document.getElementById("one"));
  key(document.getElementById("one"), "ArrowRight"); await wait(60);
  assert(active() === "two" && document.getElementById("two").getAttribute("aria-selected") === "true",
    "the newly loaded tabs work after the swap, focus is on " + active());
  assert(window.__errors.length === 0, "the swap waited for the file, so nothing was reported missing: " + window.__errors.join(" | "));
  assert(document.querySelectorAll('script[src^="/_kitwork/kernel."]').length === 1, "the kernel was not loaded twice");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } }); });
</script></body></html>`, Options{Drive: true})
	second := Deliver(`<!doctype html><html><head></head><body><main>
<div role="tablist" aria-label="Files" data-kitwork-component="tabs" data-kitwork-scope="selected: 'one'">`+tabButton("one")+tabButton("two")+`</div>
</main></body></html>`, Options{Drive: true})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if asset, _ := AssetAt(request.URL.Path); asset != nil {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(asset.Body)
			return
		}
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(first))
		case "/tabs":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(second))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
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
		t.Fatalf("delivery proof failed: %s", output[i:end])
	}
	t.Fatalf("delivery proof did not pass (err=%v):\n%.1500s", err, output)
}

// What a <pre>/<code> block prints is an example, not a host: a docs page that shows the markup of a
// component or a native service downloads neither. A real host still counts — beside the example,
// and on a <pre> itself — which is the control: a scan that ignored everything would fail it.
func TestExamplesBringNoFiles(t *testing.T) {
	docs := `<html><head></head><body><p data-kitwork-bind="text: title">x</p>
<pre><code>&lt;section data-kitwork-component="carousel" data-kitwork-event="intersect-enter: x(); click: kit.studioState.clearHistory()"&gt;</code></pre></body></html>`
	if roles, paths := deliveredFiles(t, Deliver(docs, Options{})); len(roles) != 1 || roles[0] != "runtime" {
		t.Fatalf("an example brought files: %v %v", roles, paths)
	}
	live := `<html><head></head><body><div data-kitwork-component="tabs"></div><pre data-kitwork-component="toast"><code>x</code></pre>
<pre><code>data-kitwork-component="carousel"</code></pre></body></html>`
	_, paths := deliveredFiles(t, Deliver(live, Options{}))
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "/_kitwork/components/tabs.") || !strings.Contains(joined, "/_kitwork/components/toast.") || strings.Contains(joined, "/_kitwork/components/carousel.") {
		t.Fatalf("real hosts must count, the printed one must not: %v", paths)
	}
}

// deliver is Deliver for a page that asks for no options — what most component tests need to see:
// which files a page gets.
func deliver(html string) string { return Deliver(html, Options{}) }

// tabButton is one tab as the engine writes it for <button click="select('name')"> inside <tabs>
// (ui/tabs.go): role="tab", its name as value, aria-selected and tabindex bound to the choice. Its id is
// its name.
func tabButton(name string) string {
	return `<button id="` + name + `" type="button" role="tab" value="` + name + `" data-kitwork-bind="aria-selected: selected == '` + name +
		`'; tabindex: selected == '` + name + `' ? 0 : -1" data-kitwork-event="click: select('` + name + `')">` + name + `</button>`
}
