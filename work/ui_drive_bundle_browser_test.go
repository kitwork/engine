package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// driveBundleServer serves a tenant to a headless browser, the controller written into the first
// load of "/" before </body>, every stylesheet answered no-store (a link Drive re-created would be a
// second fetch), and a POST /report handed to reports. moved records the X-Kitwork-Hydrate header of
// each page Drive fetched.
func driveBundleServer(t *testing.T, tenant *Tenant, controller string, reports chan map[string]string) (server *httptest.Server, hits map[string]int, moved map[string]string, lock *sync.Mutex) {
	t.Helper()
	lock = &sync.Mutex{}
	hits, moved = map[string]int{}, map[string]string{}
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if path == "/report" {
			var report map[string]string
			body, _ := io.ReadAll(request.Body)
			if err := json.Unmarshal(body, &report); err != nil {
				t.Errorf("report %s: %v", body, err)
			}
			select {
			case reports <- report:
			default:
			}
			return
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding")
		answer := httptest.NewRecorder()
		lock.Lock()
		hits[path]++
		if request.Header.Get("X-Kitwork-Hydrate") != "" {
			moved[path] = "drive"
		}
		tenant.Serve(answer, forward)
		lock.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		if strings.HasSuffix(path, ".css") {
			response.Header().Set("Cache-Control", "no-store")
		}
		if path == "/" && request.Header.Get("X-Kitwork-Hydrate") == "" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 {
				t.Errorf("/ has no </body>:\n%.500s", body)
			} else {
				body = body[:at] + controller + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(server.Close)
	return server, hits, moved, lock
}

// driveCSSFiles is a site under the default router.bundle ("auto": every page small, all its CSS in
// its head) whose walk changes one more thing at each step: /hai writes other classes than / (its
// page block changes, the site block is the same bytes); /nhanh is a branch with its own router.colors
// (its site block changes too); /tach is a branch that writes router.bundle("split") (its site part is
// a file, site.<hash>.css, and its page block changes).
var driveCSSFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: "#f6f3ee", paper: "#ffffff", ink: "#2b211b", line: "#e6ded5" });
router.fonts({ body: "Outfit" });
router.reload(false);
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content gap="4" padding="4">
  <text id="title" heading="1" color="brand">Một</text>
  <link id="next" value="/hai">Hai</link>
</content>`,
	"localhost/hai/content.ui": `<content gap="4" padding="6">
  <text id="title" heading="1" color="ink-30">Hai</text>
  <link id="next" value="/nhanh">Nhánh</link>
</content>`,
	"localhost/nhanh/router.work": `import { router } from "kitwork";

router.colors({ brand: "#2f6fde" });
`,
	"localhost/nhanh/content.ui": `<content gap="2" padding="4">
  <text id="title" heading="1" color="brand">Nhánh</text>
  <link id="next" value="/tach">Tách</link>
</content>`,
	"localhost/tach/router.work": `import { router } from "kitwork";

router.bundle("split");
`,
	"localhost/tach/content.ui": `<content gap="6" padding="2">
  <text id="title" heading="1" color="brand">Tách</text>
</content>`,
}

// driveCSSController walks / → /hai → /nhanh → /tach by clicking the links (Drive moves). At each
// arrival (kitwork:load, which Drive sends at the end of the swap) it reads the engine's <style> blocks
// and stylesheet links against those before the move — per role, "kept" when the same element, "new"
// when another —, which were taken off during the move, how many of each role stand (a file still off
// the screen, media="print", not counted), and the colour the title is painted with ("ink-30" when it
// is the site's ink 30% toward its canvas, as the browser mixes them). It also reads the order of the
// swap's changes to the document (one MutationObserver on <html>, its records taken at the arrival):
// "css-first" when every change to the engine's stylesheets came before the first change of the
// content, so no frame could show the new content without its CSS or the old content in the new
// look. On the move to /tach, when the site file has loaded (a load listener that runs before Drive's
// own), it reads the title still on screen, its colour and the file's media: off the screen until the
// swap.
const driveCSSController = `<script>
(function () {
  var log = {}, before = {}, arrivals = ["hai", "nhanh", "tach"], current = "", records = [];
  function generated() { return Array.prototype.slice.call(document.head.querySelectorAll('style[data-kitwork-generated],link[rel="stylesheet"][data-kitwork-generated]')); }
  function role(node) { return (node.nodeName === "LINK" ? "file:" : "") + node.getAttribute("data-kitwork-generated"); }
  function stylesheet(node) { return node && (node.nodeName === "STYLE" || node.nodeName === "LINK") && node.getAttribute && node.getAttribute("data-kitwork-generated"); }
  var observer = new MutationObserver(function (list) { records = records.concat(list); });
  observer.observe(document.documentElement, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ["media"] });
  function remember() { before = {}; generated().forEach(function (node) { before[role(node)] = node; }); records = []; }
  var shade = (function () {
    var sample = document.createElement("span");
    sample.style.color = "color-mix(in oklab,#2b211b 70%,#f6f3ee)";
    document.documentElement.appendChild(sample);
    var color = getComputedStyle(sample).color;
    sample.remove();
    return color;
  })();
  function title() {
    var node = document.getElementById("title");
    var color = getComputedStyle(node).color;
    return node.textContent + "=" + (color === shade ? "ink-30" : color.replace(/ /g, ""));
  }
  function count(name) { return generated().filter(function (node) { return role(node) === name && node.getAttribute("media") !== "print"; }).length; }
  function read() {
    records = records.concat(observer.takeRecords());
    var removed = [], firstContent = -1, lastStylesheet = -1;
    records.forEach(function (record, i) {
      var touched = Array.prototype.slice.call(record.addedNodes || []).concat(Array.prototype.slice.call(record.removedNodes || []));
      Array.prototype.forEach.call(record.removedNodes || [], function (node) { if (stylesheet(node)) removed.push(role(node)); });
      var css = stylesheet(record.target) || touched.some(stylesheet);
      if (css) lastStylesheet = i;
      else if (firstContent < 0 && document.body.contains(record.target.nodeType === 1 ? record.target : record.target.parentNode)) firstContent = i;
    });
    var words = generated().map(function (node) { return role(node) + "=" + (before[role(node)] === node ? "kept" : "new"); });
    words.push("removed=" + (removed.sort().join(",") || "none"));
    words.push("css=" + count("css") + " site=" + count("site") + " sitefile=" + count("file:site"));
    words.push("title=" + title());
    if (current) words.push(firstContent > lastStylesheet ? "css-first" : "content-first(" + lastStylesheet + ">" + firstContent + ")");
    return words.join(" ");
  }
  document.addEventListener("load", function (e) {
    var node = e.target;
    if (!node || node.nodeName !== "LINK" || node.getAttribute("data-kitwork-generated") !== "site") return;
    log["loaded-" + current] = "title=" + title() + " media=" + node.getAttribute("media");
  }, true);
  function send() {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(log) }).then(close, close);
  }
  function move() { remember(); current = arrivals[0]; setTimeout(function () { document.getElementById("next").click(); }, 50); }
  document.addEventListener("kitwork:load", function () {
    var at = arrivals.shift();
    if (!at) return;
    log[at] = read();
    if (arrivals.length) move(); else send();
  });
  window.addEventListener("load", function () { log.start = read(); move(); });
})();
</script>`

// TestBrowserDriveSwapsInlineCSS walks a site whose pages carry their CSS in their head (router.bundle,
// by default) and checks a Drive move applies the arriving page's blocks and takes the page left's
// off in the same step: after each move exactly one page block and one site part stand, and the title
// has the arriving page's colour; at the first change of the content the new CSS is already on and the
// old block already gone (no flash of either). A block both pages carry byte for byte (the site block
// on / → /hai) stays the same element, never taken off; the page block, and the site block of a
// branch with its own colours, are traded. Moving to a page whose site part is a file (router.bundle
// "split" in /tach) loads that file off the screen while the old content and its look stay, then
// switches it on and takes the inline site block off at the swap. The trades are the control: the
// reading that says "kept" says "new" when the bytes changed.
func TestBrowserDriveSwapsInlineCSS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive inline CSS browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, driveCSSFiles)
	// the site is the walk's: inline everywhere but /tach's site part
	for _, page := range []string{"/", "/hai", "/nhanh", "/tach"} {
		head := headOf(serveUI(t, tenant, "http://localhost"+page).Body.String())
		inlineSite := strings.Contains(head, `<style data-kitwork-generated="site"`)
		if inlineSite != (page != "/tach") || siteStylesheetRe.MatchString(head) != (page == "/tach") {
			t.Fatalf("the site is not the walk's: %s carries its site part inline: %v\n%.600s", page, inlineSite, head)
		}
	}
	reports := make(chan map[string]string, 1)
	server, hits, moved, lock := driveBundleServer(t, tenant, driveCSSController, reports)
	got := runBrowser(t, browser, server.URL+"/", "drive inline css", 1000, "", reports)

	brand, branch := "rgb(200,116,44)", "rgb(47,111,222)"
	want := map[string]string{
		"start":       "site=new css=new removed=none css=1 site=1 sitefile=0 title=Một=" + brand,
		"hai":         "site=kept css=new removed=css css=1 site=1 sitefile=0 title=Hai=ink-30 css-first",
		"nhanh":       "site=new css=new removed=css,site css=1 site=1 sitefile=0 title=Nhánh=" + branch + " css-first",
		"loaded-tach": "title=Nhánh=" + branch + " media=print",
		"tach":        "file:site=new css=new removed=css,site css=1 site=0 sitefile=1 title=Tách=" + brand + " css-first",
	}
	for _, step := range []string{"start", "hai", "nhanh", "loaded-tach", "tach"} {
		if got[step] != want[step] {
			t.Errorf("%s:\n got  %s\n want %s", step, got[step], want[step])
		}
	}
	lock.Lock()
	defer lock.Unlock()
	for _, page := range []string{"/hai", "/nhanh", "/tach"} {
		if moved[page] == "" {
			t.Errorf("%s was loaded whole, not moved to by Drive (requests %v)", page, hits)
		}
	}
}

// driveScriptFiles is a site whose root writes router.bundle({ javascript: "inline" }): / has no
// component, /hai a dropdown (its part arrives written in the page Drive fetches), /ba is a branch
// with { javascript: "split" } and an appearance switch beside the dropdown (one bundle file holding
// Drive, the progressbar, the reader, the dropdown and the appearance component).
var driveScriptFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.bundle({ javascript: "inline" });
`,
	"localhost/page.ui":    `<page><content /></page>`,
	"localhost/content.ui": `<content><text id="title">Một</text><link id="next" value="/hai">Hai</link></content>`,
	"localhost/hai/content.ui": `<content>
  <text id="title">Hai</text>
  <box component="dropdown"><button id="open" click="toggle()" expanded="{{ open }}">Mở</button></box>
  <link id="next" value="/ba">Ba</link>
</content>`,
	"localhost/ba/router.work": `import { router } from "kitwork";

router.bundle({ javascript: "split" });
`,
	"localhost/ba/content.ui": `<content>
  <text id="title">Ba</text>
  <box component="dropdown"><button id="open" click="toggle()" expanded="{{ open }}">Mở</button></box>
  <box id="mode"><button component="appearance" click="toggle()">Đổi</button></box>
</content>`,
}

// driveScriptController counts every component registration after the first load (kit.component
// wrapped), walks / → /hai → /ba by the links, and at each arrival clicks what the page offers and
// reads the answer a tick later: whether the dropdown's aria-expanded changed (a dropdown the morph
// kept from the page before keeps its state), the appearance switch's dark mode, how
// many parts Drive ran from the page it fetched (script[data-kitwork-part] without a type: the ones it
// added), and the registrations so far.
const driveScriptController = `<script>
(function () {
  var log = {}, registered = {}, arrivals = ["hai", "ba"];
  function count() { // the kernel is a deferred file: it stands once the page has loaded
    var component = window.kit.component;
    window.kit.component = function (name) { registered[name] = (registered[name] || 0) + 1; return component.apply(this, arguments); };
  }
  function counts() { return Object.keys(registered).sort().map(function (name) { return name + ":" + registered[name]; }).join(",") || "none"; }
  function ran() { return document.head.querySelectorAll("script[data-kitwork-part]:not([type])").length; }
  function send() {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(log) }).then(close, close);
  }
  function move() { setTimeout(function () { document.getElementById("next").click(); }, 50); }
  document.addEventListener("kitwork:load", function () {
    var at = arrivals.shift();
    if (!at) return;
    setTimeout(function () {
      var was = document.getElementById("open").getAttribute("aria-expanded");
      document.getElementById("open").click();
      var mode = document.querySelector("#mode button");
      if (mode) mode.click();
      setTimeout(function () {
        log[at] = "toggled=" + (document.getElementById("open").getAttribute("aria-expanded") !== was) +
          " dark=" + document.documentElement.classList.contains("dark") + " ran=" + ran() + " registered=" + counts() +
          " bundle=" + document.head.querySelectorAll('script[data-kitwork-generated="bundle"]').length;
        if (arrivals.length) move(); else send();
      }, 60);
    }, 60);
  });
  window.addEventListener("load", function () { count(); log.start = "registered=" + counts() + " ran=" + ran(); move(); });
})();
</script>`

// TestBrowserDriveRunsTheJavaScriptPartsAMoveBrings walks a site whose pages write their JavaScript
// parts in themselves (router.bundle({ javascript: "inline" })) into a branch that bundles them
// (router.bundle({ javascript: "split" })). A move to a page with a component the page on screen has
// not run (the dropdown on /hai) runs that part, and only that one, before the swap: the dropdown
// answers its first click. A move to a page whose bundle holds parts already run and one new (the
// appearance component on /ba) runs the new one only: each component registers once over the walk,
// the dropdown included although /ba's bundle holds it again. CONTROL: on the first load nothing was
// registered after the controller started, and the counts that say 1 would say 2 if a part ran twice.
func TestBrowserDriveRunsTheJavaScriptPartsAMoveBrings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive JavaScript parts browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, driveScriptFiles)
	if head := headOf(serveUI(t, tenant, "http://localhost/hai").Body.String()); !inlinePartRe.MatchString(serveUI(t, tenant, "http://localhost/hai").Body.String()) || strings.Contains(head, `data-kitwork-generated="bundle"`) {
		t.Fatalf("the site is not the walk's: /hai writes its parts in the page:\n%.600s", head)
	}
	if head := headOf(serveUI(t, tenant, "http://localhost/ba").Body.String()); !strings.Contains(head, `data-kitwork-generated="bundle"`) {
		t.Fatalf("the site is not the walk's: /ba bundles its parts:\n%.600s", head)
	}
	reports := make(chan map[string]string, 1)
	server, hits, moved, lock := driveBundleServer(t, tenant, driveScriptController, reports)
	got := runBrowser(t, browser, server.URL+"/", "drive javascript parts", 1000, "", reports)

	want := map[string]string{
		"start": "registered=none ran=0",
		"hai":   "toggled=true dark=false ran=1 registered=dropdown:1 bundle=0",
		"ba":    "toggled=true dark=true ran=1 registered=appearance:1,dropdown:1 bundle=1",
	}
	for _, step := range []string{"start", "hai", "ba"} {
		if got[step] != want[step] {
			t.Errorf("%s:\n got  %s\n want %s", step, got[step], want[step])
		}
	}
	lock.Lock()
	defer lock.Unlock()
	for _, page := range []string{"/hai", "/ba"} {
		if moved[page] == "" {
			t.Errorf("%s was loaded whole, not moved to by Drive (requests %v)", page, hits)
		}
	}
}
