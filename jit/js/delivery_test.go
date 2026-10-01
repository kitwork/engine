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

var deliveredSrc = regexp.MustCompile(`<script data-kitwork-jit="([a-z]+)"(?: data-kitwork-drive)? src="([^"]+)" defer></script>`)

func deliveredFiles(t *testing.T, html string) (roles, paths []string) {
	t.Helper()
	for _, m := range deliveredSrc.FindAllStringSubmatch(html, -1) {
		roles = append(roles, m[1])
		paths = append(paths, m[2])
	}
	return roles, paths
}

// A page gets the kernel, Drive when the site asked for it, then one file per capability and
// component it uses — kernel first, because defer runs them in document order and the rest register
// into it; among the modules the order is the sorted key order, and any order works.
func TestDeliverGivesEachPartAFileOfItsOwn(t *testing.T) {
	page := `<html><head></head><body><nav data-kit-component="menu"></nav><div data-kit-component="clipboard"></div><p data-kit-remember="theme"></p></body></html>`
	out := Deliver(page, nil, Options{Drive: true})
	roles, paths := deliveredFiles(t, out)
	if strings.Join(roles, ",") != "runtime,drive,capability,component,component" {
		t.Fatalf("roles in order: got %v\n%s", roles, out)
	}
	if !strings.HasPrefix(paths[0], "/kit/kernel.") || !strings.HasPrefix(paths[1], "/kit/drive.") ||
		!strings.HasPrefix(paths[2], "/kit/capabilities/remember.") || !strings.HasPrefix(paths[3], "/kit/components/clipboard@2.0.0.") ||
		!strings.HasPrefix(paths[4], "/kit/components/menu@1.0.0.") {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(out, `data-kitwork-jit="runtime" data-kitwork-drive src=`) {
		t.Error("the kernel's tag must carry the Drive flag the client looks for")
	}
	if strings.Contains(out, "?components=") {
		t.Error("the per-page query form is gone from delivered pages")
	}
	if i := strings.Index(out, `<script data-kitwork-jit`); i > strings.Index(out, "</head>") {
		t.Error("the files belong in <head>")
	}
	// CONTROL: without Drive, no Drive file and no flag.
	if roles, _ := deliveredFiles(t, Deliver(page, nil, Options{})); strings.Join(roles, ",") != "runtime,capability,component,component" || strings.Contains(Deliver(page, nil, Options{}), "data-kitwork-drive") {
		t.Errorf("a site that did not ask for Drive gets neither its file nor its flag, got %v", roles)
	}
}

func TestDeliverOnlyWhereTheGrammarIsUsed(t *testing.T) {
	grammar := `<html><head></head><body><div data-kit-scope="open: false"><button data-kit-click="open = !open">x</button></div></body></html>`
	if roles, _ := deliveredFiles(t, Deliver(grammar, nil, Options{})); strings.Join(roles, ",") != "runtime" {
		t.Errorf("grammar with no component gets the kernel alone, got %v", roles)
	}
	for name, page := range map[string]string{
		"plain":   `<html><head></head><body><p>hello</p></body></html>`,
		"example": `<html><head></head><body><pre><code>&lt;b data-kit-text="x"&gt;</code></pre></body></html>`,
	} {
		if out := Deliver(page, nil, Options{Drive: true}); out != page {
			t.Errorf("%s: a page that uses no grammar must be returned untouched, got %s", name, out)
		}
	}
	// A page that already carries the engine's runtime tag is left alone.
	already := `<html><head><script data-kitwork-jit="runtime" src="/kit.js" defer></script></head><body><div data-kit-component="menu"></div></body></html>`
	if Deliver(already, nil, Options{}) != already {
		t.Error("a second kernel would load")
	}
}

// The name is a promise about the bytes: the path round-trips through AssetAt, and a different
// setting is a different name. An old hash still finds today's file, flagged as not its name.
func TestAssetsAreNamedByTheirBytes(t *testing.T) {
	defer func(minify bool) { Minify = minify }(Minify)
	Minify = false
	page := `<html><head></head><body><div data-kit-component="tree"></div></body></html>`
	_, paths := deliveredFiles(t, Deliver(page, nil, Options{Drive: true}))
	for _, path := range paths {
		if asset, exact := AssetAt(path, nil); asset == nil || !exact {
			t.Fatalf("%s does not resolve back to its file", path)
		}
	}
	tree, _ := AssetAt(paths[2], nil)
	if !bytes.HasPrefix(tree.Body, []byte(";(function () {")) || !bytes.Contains(tree.Body, []byte(`component("tree@v1.0.0"`)) {
		t.Errorf("a component file is the component, in a scope of its own:\n%.200s", tree.Body)
	}
	Minify = true
	if KernelAsset().Path == paths[0] {
		t.Error("minified bytes kept the readable file's name — a cache would serve the wrong one")
	}
	Minify = false

	// An old hash: today's kernel, but not under its own name.
	wrongHash := paths[0][:len(paths[0])-len(".js")-hashLength] + strings.Repeat("0", hashLength) + ".js"
	if asset, exact := AssetAt(wrongHash, nil); asset == nil || exact || asset.Path != paths[0] {
		t.Errorf("%s: want today's kernel, not exact; got %v exact=%t", wrongHash, asset, exact)
	}
	// Paths that name nothing.
	for _, bad := range []string{
		"/kit/kernel.js",
		"/kit/components/../kernel." + strings.Repeat("a", hashLength) + ".js",
		"/kit/components/tree@9.9.9." + strings.Repeat("a", hashLength) + ".js",
		"/kit/components/nonesuch." + strings.Repeat("a", hashLength) + ".js",
		"/kit/capabilities/nonesuch." + strings.Repeat("a", hashLength) + ".js",
		"/kit.js",
	} {
		if asset, _ := AssetAt(bad, nil); asset != nil {
			t.Errorf("%s must not resolve", bad)
		}
	}
}

type fakeSite map[string]string

func (s fakeSite) Component(name string) string { return s[name] }

// A site's own component is a file too, found only in that site's set, and renamed when it changes.
func TestSiteComponentsAreFilesOfTheirOwn(t *testing.T) {
	site := fakeSite{"bid-stepper": `window.kit.component("bid-stepper", { amount: 0 });`}
	page := `<html><head></head><body><div data-kit-component="bid-stepper"></div></body></html>`
	_, paths := deliveredFiles(t, Deliver(page, site, Options{}))
	if len(paths) != 2 || !strings.HasPrefix(paths[1], "/kit/components/bid-stepper.") {
		t.Fatalf("paths: %v", paths)
	}
	if asset, exact := AssetAt(paths[1], site); asset == nil || !exact {
		t.Error("the site's file does not resolve for that site")
	}
	if asset, _ := AssetAt(paths[1], nil); asset != nil {
		t.Error("a site's file resolved without a site")
	}
	if asset, _ := AssetAt(paths[1], fakeSite{}); asset != nil {
		t.Error("a site's file resolved for a site that does not own it")
	}
	edited := fakeSite{"bid-stepper": site["bid-stepper"] + " "}
	if _, again := deliveredFiles(t, Deliver(page, edited, Options{})); again[1] == paths[1] {
		t.Error("an edited component kept its old name")
	}
}

// A site-wide pin decides what an unversioned name means; a version in the markup still wins.
func TestVersionsPinTheCatalogueForTheSite(t *testing.T) {
	page := `<html><head></head><body><div data-kit-component="clipboard"></div></body></html>`
	if _, paths := deliveredFiles(t, Deliver(page, nil, Options{Versions: map[string]string{"clipboard": "1.0.0"}})); !strings.HasPrefix(paths[1], "/kit/components/clipboard@1.0.0.") {
		t.Errorf("the pin was not applied: %v", paths)
	}
	written := `<html><head></head><body><div data-kit-component="clipboard@v2.0.0"></div></body></html>`
	if _, paths := deliveredFiles(t, Deliver(written, nil, Options{Versions: map[string]string{"clipboard": "1.0.0"}})); !strings.HasPrefix(paths[1], "/kit/components/clipboard@2.0.0.") {
		t.Errorf("the markup's version must win over the pin: %v", paths)
	}
	if !HasComponentVersion("dialog", "2.0.0") || HasComponentVersion("dialog", "9.0.0") || strings.Join(ComponentVersionsOf("clipboard"), ",") != "1.0.0,2.0.0" {
		t.Errorf("versions: %v", ComponentVersionsOf("clipboard"))
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
<div data-kit-component="clipboard"><pre data-kit-element="code">npm i kitwork</pre><output id="copied" data-kit-text="copied ? 'yes' : 'no'"></output></div>
<div data-kit-component="toast"><output id="toast" data-kit-text="visible ? 'shown' : 'quiet'"></output></div>
<a id="go" href="/tree">tree</a>
</main><script>
document.addEventListener("DOMContentLoaded", function () { (async function () {
`+proofScriptHead+`
  await wait(100);
  assert(document.getElementById("copied").textContent === "no", "the component file registered before the first paint");
  assert(window.__errors.length === 0, "nothing was reported missing on load: " + window.__errors.join(" | "));
  document.getElementById("go").click();
  for (var i = 0; i < 40 && !document.getElementById("two"); i++) await wait(100);
  await wait(150);
  var tree = document.querySelector('[data-kit-component="tree"]');
  assert(tree, "Drive swapped in the tree page");
  assert(document.querySelector('script[src^="/kit/components/tree@1.0.0."]'), "the tree's file was added to <head>");
  focusIn(document.getElementById("one"));
  key(document.getElementById("one"), "ArrowDown"); await wait(60);
  assert(active() === "two", "the newly loaded tree works after the swap, focus is on " + active());
  assert(window.__errors.length === 0, "the swap waited for the file, so nothing was reported missing: " + window.__errors.join(" | "));
  assert(document.querySelectorAll('script[src^="/kit/kernel."]').length === 1, "the kernel was not loaded twice");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } }); });
</script></body></html>`, nil, Options{Drive: true})
	second := Deliver(`<!doctype html><html><head></head><body><main>
<ul role="tree" aria-label="Files" data-kit-component="tree">
  <li id="one" role="treeitem" data-kit-element="item" data-id="one" data-kit-bind:tabindex="focused === 'one' ? 0 : -1">one</li>
  <li id="two" role="treeitem" data-kit-element="item" data-id="two" data-kit-bind:tabindex="focused === 'two' ? 0 : -1">two</li>
</ul>
</main></body></html>`, nil, Options{Drive: true})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if asset, _ := AssetAt(request.URL.Path, nil); asset != nil {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(asset.Body)
			return
		}
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(first))
		case "/tree":
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
