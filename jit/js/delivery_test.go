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
// anything (02/10) and brings no file.
func TestDeliverGivesEachPartAFileOfItsOwn(t *testing.T) {
	page := `<html><head></head><body><nav data-kit-component="menu"></nav><div data-kit-component="clipboard"></div><p data-kit-remember="theme"></p></body></html>`
	out := Deliver(page, nil, Options{Drive: true})
	roles, paths := deliveredFiles(t, out)
	if strings.Join(roles, ",") != "runtime,drive,component,component" {
		t.Fatalf("roles in order: got %v\n%s", roles, out)
	}
	if !strings.HasPrefix(paths[0], "/_kitwork/kernel.") || !strings.HasPrefix(paths[1], "/_kitwork/drive.") ||
		!strings.HasPrefix(paths[2], "/_kitwork/components/clipboard.") ||
		!strings.HasPrefix(paths[3], "/_kitwork/components/menu.") {
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
	if roles, _ := deliveredFiles(t, Deliver(page, nil, Options{})); strings.Join(roles, ",") != "runtime,component,component" {
		t.Errorf("a site that did not ask for Drive does not get its file, got %v", roles)
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
		if out := Deliver(page, nil, Options{}); out != page {
			t.Errorf("%s: a page that uses no grammar must be returned untouched, got %s", name, out)
		}
		// A site that moves between pages without a reload does so from EVERY page: a page with no
		// grammar still gets the kernel and Drive, or its links would reload and drop what the swap keeps.
		if roles, _ := deliveredFiles(t, Deliver(page, nil, Options{Drive: true})); strings.Join(roles, ",") != "runtime,drive" {
			t.Errorf("%s: on a Drive site a page with no grammar gets the kernel and Drive, got %v", name, roles)
		}
	}
	// A page that already carries the engine's runtime tag is left alone.
	already := `<html><head><script data-kitwork-generated="runtime" src="/kit.js" defer></script></head><body><div data-kit-component="menu"></div></body></html>`
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
	if !bytes.HasPrefix(tree.Body, []byte(";(function () {")) || !bytes.Contains(tree.Body, []byte(`component("tree", treeDef)`)) {
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
		t.Errorf("%s: want today's kernel, not exact; got %s exact=%t", wrongHash, assetPath(asset), exact)
	}
	// Paths that name nothing, under the prefix and the one before it.
	for _, bad := range []string{
		"/_kitwork/kernel.js",
		"/_kitwork/components/../kernel." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/components/tree@9.9.9." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/components/tree@v1.0.0." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/components/nonesuch." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/capabilities/remember." + strings.Repeat("a", hashLength) + ".js",
		"/_kitwork/fonts/kernel." + strings.Repeat("a", hashLength) + ".js",
		"/kit/kernel.js",
		"/kit/components/tree@9.9.9." + strings.Repeat("a", hashLength) + ".js",
		"/kit/components/nonesuch." + strings.Repeat("a", hashLength) + ".js",
		"/kit.js",
		"/other/kernel." + strings.Repeat("a", hashLength) + ".js",
	} {
		if asset, _ := AssetAt(bad, nil); asset != nil {
			t.Errorf("%s must not resolve", bad)
		}
	}
}

// Everything the engine serves is under /_kitwork/; the names it wrote before 11/10/2026 still find
// their file — under the old /kit/ prefix, and a catalogue component under the name that carried
// its version — but never as their exact name, so the server marks them not to be kept, the way it
// marks an old hash. CONTROL: each file's current name is exact.
func TestOldNamesFindTodaysFile(t *testing.T) {
	defer func(minify bool) { Minify = minify }(Minify)
	Minify = false
	page := `<html><head></head><body><div data-kit-component="tree"></div><div data-kit-component="clipboard@1.0.0"></div><b data-kit-intersect="x()" data-kit-click="kit.studioState.clearHistory()"></b></body></html>`
	roles, paths := deliveredFiles(t, Deliver(page, nil, Options{Drive: true}))
	if strings.Join(roles, ",") != "runtime,drive,directive,service,service,component,component" {
		t.Fatalf("roles: %v %v", roles, paths)
	}
	// Every name is <role>.<hash>.js, one-of-a-kind files at the top and the rest in a folder by
	// role — and no name carries a version, whatever the role: the page named clipboard@1.0.0, and
	// studioState has a version of its own in the engine.
	shape := regexp.MustCompile(`^/_kitwork/(?:(?:components|services|directives)/[a-zA-Z][a-zA-Z0-9-]*|kernel|drive)\.[0-9a-f]{16}\.js$`)
	for _, path := range paths {
		if !shape.MatchString(path) {
			t.Errorf("%s is not /_kitwork/[<role folder>/]<name>.<hash>.js with no version", path)
		}
	}
	for _, path := range paths {
		current, exact := AssetAt(path, nil)
		if current == nil || !exact || !strings.HasPrefix(path, "/_kitwork/") {
			t.Fatalf("%s: the current name must be exact and under /_kitwork/ (exact=%t)", path, exact)
		}
		old := "/kit/" + strings.TrimPrefix(path, "/_kitwork/")
		if asset, exact := AssetAt(old, nil); asset != current || exact {
			t.Errorf("%s: the old prefix must answer with today's file %s, not as its name; got %s exact=%t", old, path, assetPath(asset), exact)
		}
	}

	tree := componentAsset("tree", nil)
	v1 := componentAsset("clipboard@v1.0.0", nil)
	v2 := componentAsset("clipboard", nil)
	if v1 == nil || v2 == nil || v1.Path == v2.Path || !bytes.Contains(v1.Body, []byte("}, 1000);")) || !bytes.Contains(v2.Body, []byte("}, 2000);")) {
		t.Fatalf("the two clipboard sources must be two files under one name: %s %s", assetPath(v1), assetPath(v2))
	}
	stale := strings.Repeat("0", hashLength)
	for _, c := range []struct{ path, want string }{
		// The name a page carried before: the version that name had, as it is today.
		{"/kit/components/tree@1.0.0." + stale + ".js", tree.Path},
		{"/kit/components/clipboard@1.0.0." + stale + ".js", v1.Path},
		{"/kit/components/clipboard@2.0.0." + stale + ".js", v2.Path},
		{"/_kitwork/components/clipboard@1.0.0." + stale + ".js", v1.Path},
		// The name without a version and a hash no build makes: the latest.
		{"/_kitwork/components/clipboard." + stale + ".js", v2.Path},
		{"/kit/kernel." + stale + ".js", KernelAsset().Path},
	} {
		if asset, exact := AssetAt(c.path, nil); asset == nil || exact || asset.Path != c.want {
			t.Errorf("%s: want %s, not exact; got %s exact=%t", c.path, c.want, assetPath(asset), exact)
		}
	}
	// The hash, not the name, says which version: v1's current name is v1's file, exactly — and the
	// same name under the old prefix is still v1's file (not the latest), only not as its own name.
	if asset, exact := AssetAt(v1.Path, nil); asset != v1 || !exact {
		t.Errorf("%s: an older version's current name must be that version's file, exactly; got %s exact=%t", v1.Path, assetPath(asset), exact)
	}
	oldV1 := "/kit/" + strings.TrimPrefix(v1.Path, "/_kitwork/")
	if asset, exact := AssetAt(oldV1, nil); asset != v1 || exact {
		t.Errorf("%s: want v1's file %s, not exact; got %s exact=%t", oldV1, v1.Path, assetPath(asset), exact)
	}
}

// A catalogue component's file registers its bare name and nothing else: a versioned name is not
// something the wire knows (11/10/2026). A template page that still writes a version in its host
// works because the kernel drops the version before it looks the blueprint up — the browser proof
// below.
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
	if files < 20 {
		t.Fatalf("read %d component files: the catalogue was not where this test looks", files)
	}
}

// A template page written before 11/10/2026 names a version in its host — data-kit-component=
// "dialog@2.0.0" — and still works: the file it gets registers only the bare name, and the kernel
// drops the version before it looks the blueprint up, so the host is seeded and nothing is reported
// missing. CONTROL in the page: a host whose name nothing registers IS reported, so the hook would
// have caught a kernel that looked "clipboard@1.0.0" up as written.
func TestBrowserAVersionWrittenInAHostFindsTheBareName(t *testing.T) {
	runBrowserProof(t, "versioned host", nil, `
<div data-kit-component="clipboard@1.0.0"><b id="clip" data-kit-text="copied === false ? 'seeded' : 'unseeded'"></b></div>
<dialog data-kit-component="dialog@2.0.0"><b id="dialog" data-kit-text="open === false ? 'seeded' : 'unseeded'"></b></dialog>
<div id="nobody" data-kit-component="nobody@1.0.0"></div>
<script>
window.__errors = [];
(function () { var original = console.error; console.error = function (e) { window.__errors.push(String(e && e.message || e)); original.apply(console, arguments); }; })();
(async function () {
  await wait(150);
  var byId = function (id) { return document.getElementById(id); };
  var names = Object.keys(window.kit.blueprints);
  assert(names.indexOf("clipboard") >= 0 && names.indexOf("dialog") >= 0, "the bare names are registered: " + names.join(","));
  assert(names.every(function (name) { return name.indexOf("@") < 0; }), "no versioned name is registered: " + names.join(","));
  assert(byId("clip").textContent === "seeded" && byId("dialog").textContent === "seeded",
    "a version written in the host still seeds it from the bare name: " + byId("clip").textContent + " / " + byId("dialog").textContent);
  var missing = window.__errors.filter(function (e) { return e.indexOf("no component registered") >= 0; });
  assert(missing.length === 1 && missing[0].indexOf('"nobody"') >= 0, "only the host nothing registers is reported missing: " + missing.join(" | "));
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

type fakeSite map[string]string

func (s fakeSite) Component(name string) string { return s[name] }

// A site's own component is a file too, found only in that site's set, and renamed when it changes.
func TestSiteComponentsAreFilesOfTheirOwn(t *testing.T) {
	site := fakeSite{"bid-stepper": `window.kit.component("bid-stepper", { amount: 0 });`}
	page := `<html><head></head><body><div data-kit-component="bid-stepper"></div></body></html>`
	_, paths := deliveredFiles(t, Deliver(page, site, Options{}))
	if len(paths) != 2 || !strings.HasPrefix(paths[1], "/_kitwork/components/bid-stepper.") {
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

// Which version's source a page gets is the engine's business: the latest, unless a template page's
// markup names one — in either form the engine reads. The name is the component's either way; only
// the bytes, and so the hash, differ.
func TestTheLatestShipsUnlessTheMarkupNamesAVersion(t *testing.T) {
	fileOf := func(host string) *Asset {
		t.Helper()
		_, paths := deliveredFiles(t, Deliver(`<html><head></head><body>`+host+`</body></html>`, nil, Options{}))
		if len(paths) != 2 || !strings.HasPrefix(paths[1], "/_kitwork/components/clipboard.") {
			t.Fatalf("%s: paths %v", host, paths)
		}
		asset, exact := AssetAt(paths[1], nil)
		if asset == nil || !exact {
			t.Fatalf("%s: %s does not resolve to its file", host, paths[1])
		}
		return asset
	}
	latest := fileOf(`<div data-kit-component="clipboard"></div>`)
	if !bytes.Contains(latest.Body, []byte("}, 2000);")) {
		t.Errorf("an unversioned host gets the latest source:\n%.300s", latest.Body)
	}
	for _, host := range []string{
		`<div data-kit-component="clipboard@1.0.0"></div>`,
		`<div data-kit-component="clipboard@v1.0.0"></div>`,
		`<div data-kitwork-component="clipboard@1.0.0: $copy"></div>`,
	} {
		if older := fileOf(host); older.Path == latest.Path || !bytes.Contains(older.Body, []byte("}, 1000);")) {
			t.Errorf("%s: the version the markup names ships, under the component's name: %s", host, older.Path)
		}
	}
	if strings.Join(componentVersionsOf("clipboard"), ",") != "1.0.0,2.0.0" {
		t.Errorf("versions: %v", componentVersionsOf("clipboard"))
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
  assert(document.querySelector('script[src^="/_kitwork/components/tree."]'), "the tree's file was added to <head>");
  focusIn(document.getElementById("one"));
  key(document.getElementById("one"), "ArrowDown"); await wait(60);
  assert(active() === "two", "the newly loaded tree works after the swap, focus is on " + active());
  assert(window.__errors.length === 0, "the swap waited for the file, so nothing was reported missing: " + window.__errors.join(" | "));
  assert(document.querySelectorAll('script[src^="/_kitwork/kernel."]').length === 1, "the kernel was not loaded twice");
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

// What a <pre>/<code> block prints is an example, not a host: a docs page that shows the markup of a
// component or a native service downloads neither. A real host still counts — beside the example,
// and on a <pre> itself — which is the control: a scan that ignored everything would fail it.
func TestExamplesBringNoFiles(t *testing.T) {
	docs := `<html><head></head><body><p data-kit-text="title">x</p>
<pre><code>&lt;section data-kit-component="tree" data-kit-intersect="x()" data-kit-click="kit.studioState.clearHistory()"&gt;</code></pre></body></html>`
	if roles, paths := deliveredFiles(t, Deliver(docs, nil, Options{})); len(roles) != 1 || roles[0] != "runtime" {
		t.Fatalf("an example brought files: %v %v", roles, paths)
	}
	live := `<html><head></head><body><div data-kit-component="tabs"></div><pre data-kit-component="menu"><code>x</code></pre>
<pre><code>data-kit-component="tree"</code></pre></body></html>`
	_, paths := deliveredFiles(t, Deliver(live, nil, Options{}))
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "/_kitwork/components/tabs.") || !strings.Contains(joined, "/_kitwork/components/menu.") || strings.Contains(joined, "/_kitwork/components/tree.") {
		t.Fatalf("real hosts must count, the printed one must not: %v", paths)
	}
}

// deliver is Deliver for a page that belongs to no site and asks for no options — what most
// component tests need to see: which files a page gets.
func deliver(html string) string { return Deliver(html, nil, Options{}) }
