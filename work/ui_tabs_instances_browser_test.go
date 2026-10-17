package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// tabsInstanceFiles is a site whose component code-window holds, in its own .ui, a tabs that persists
// (router and page, so page+router) and its two panels; the page uses it twice, each in a box of its own.
var tabsInstanceFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="4">
  <box id="one"><box component="code-window" /></box>
  <box id="two"><box component="code-window" /></box>
</content>`,
	"localhost/code-window.ui": `<box gap="2">
  <box component="tabs" as="$files" persist="true" alt="Files" flex="row" gap="1">
    <button click="select('router')" selected:underline="true">router.work</button>
    <button click="select('page')" selected:underline="true">page.ui</button>
  </box>
  <box show="$files.selected == 'router'"><text>router</text></box>
  <box show="$files.selected == 'page'"><text>page</text></box>
</box>`,
	"localhost/code-window.work": `import { component } from "kitwork";

component.define("code-window", () => {
  var note = 1;
});
`,
}

// TestBrowserTabsPrepaintPaintsEveryInstance proves, in a real browser with no script of Kitwork's
// loaded (every .js answers 404, so only the server's HTML and the inline pre-paint act), that the
// pre-paint of a component used twice paints both instances: with page kept under page+router, each
// instance shows its page panel AND has its page tab chosen (aria-selected, so its selected: look), the
// first instance as the second. CONTROL: with nothing kept, both are as the server painted them, router.
func TestBrowserTabsPrepaintPaintsEveryInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the tabs instances browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, tabsInstanceFiles)
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, ".js") {
			http.NotFound(response, request) // no kernel, no component: what shows is the HTML and the pre-paint
			return
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 900, 900, "")
	d.loaded(exampleSettle)
	const read = `["one", "two"].map(function (id) {
  var box = document.getElementById(id);
  var tab = Array.prototype.filter.call(box.querySelectorAll('[role="tab"]'), function (b) { return b.getAttribute("aria-selected") === "true"; })
    .map(function (b) { return b.value + (b.tabIndex === 0 ? "" : "(not in the tab order)"); }).join(",") || "none";
  var shown = Array.prototype.filter.call(box.querySelectorAll('[role="tabpanel"]'), function (p) { return !p.hidden; })
    .map(function (p) { return p.textContent.trim(); }).join(",") || "none";
  return id + " tab:" + tab + " panel:" + shown;
}).join(" | ")`
	if got, want := d.evaluate(read), "one tab:router panel:router | two tab:router panel:router"; got != want {
		t.Errorf("nothing kept: as the server painted\n got %s\nwant %s", got, want)
	}
	d.evaluate(`localStorage.setItem("kitwork:tabs:page+router", JSON.stringify("page")); location.replace("/?kept"); 1`)
	for try := 0; ; try++ {
		result, _ := d.try("Runtime.evaluate", map[string]any{"expression": `location.search === "?kept" && document.readyState === "complete"`, "returnByValue": true})
		if strings.Contains(string(result), `"value":true`) {
			break
		}
		if try == 50 {
			t.Fatal("the page never loaded again")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got, want := d.evaluate(read), "one tab:page panel:page | two tab:page panel:page"; got != want {
		t.Errorf("page kept: the pre-paint paints both instances\n got %s\nwant %s", got, want)
	}
}
