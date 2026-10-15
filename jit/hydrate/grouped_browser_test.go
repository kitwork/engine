package hydrate

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The grouped attributes in a real browser: real attribute selectors ([data-kitwork-bind] …), real
// event dispatch (click, a bubbling keydown, an input a person typed), the real CSS object model
// refusing nothing the kernel let through. The node shim proves the logic; this proves the same page
// works where it will run. The page reports what it saw into <pre id="result"> as JSON.

func runGroupedBrowserPage(t *testing.T, browser string, files map[string]string, budget string) []byte {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, ok := files[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		if strings.HasSuffix(request.URL.Path, ".js") {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--window-size=800,600", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"),
		"--virtual-time-budget=" + budget, "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	return output
}

var groupedResultRe = regexp.MustCompile(`(?s)<pre id="result">(.*?)</pre>`)

// Every fact is paired with its control in the same page: the hand-written data-kit-text beside the
// grouped bind, a click inside beside the click outside, another key beside Escape, the safe style
// pair beside the refused one, the legacy alias beside the grouped one.
func TestBrowserGroupedDirectives(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the grouped browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := `<!doctype html><html><head><meta charset="utf-8"><title>grouped</title></head><body>
<main id="app" data-kitwork-scope="open: false; query: 'start'; count: 0; size: 25; esc: 0; limit: 3; image: 'url(https://example.test/x.png)'; rows: [{ id: 'a', n: 'A' }, { id: 'b', n: 'B' }]"
      data-kitwork-event="keydown.escape: esc++">
  <div id="menu" data-kitwork-event="click-outside: open = false">
    <button id="toggle" data-kitwork-event="click: open = !open; click: count++" data-kitwork-bind="aria-expanded: open">toggle</button>
    <div id="panel" class="card" hidden data-kitwork-bind="show: open; text: 'count=' + count" data-kitwork-class="active: open; py-1.5: open; idle: !open"></div>
  </div>
  <div id="oldbind" data-kitwork-bind="class.active: open"></div>
  <span id="legacy" data-kit-text="count"></span>
  <input id="field" data-kitwork-bind="value: query">
  <span id="echo" data-kitwork-bind="text: query"></span>
  <div id="bar" style="height:2px" data-kitwork-style="width: size + '%'; background-image: image; --progress: size"></div>
  <ul id="list"><li data-kitwork-for="row, i of rows; key: row.id" data-kitwork-bind="text: i + row.n"></li></ul>
  <p id="cond" data-kitwork-if="open">open</p>
  <div id="host" data-kitwork-component="counter@1.0.0: $counter" data-kitwork-scope="step: limit"><b id="value" data-kitwork-bind="text: value"></b></div>
  <div id="old" data-kit-component="counter" data-kit-alias="$old" data-kit-scope="step: 2"></div>
  <button id="bump" data-kitwork-event="click: $counter.inc(); click: $old.inc()">bump</button>
  <i id="oldvalue" data-kit-text="$old.value"></i>
</main>
<pre id="result"></pre>
<script>
var errors = [];
var realError = console.error;
console.error = function (error, directive) { errors.push(String(error && error.message || error) + "@" + directive); realError.apply(console, arguments); };
window.addEventListener("load", function () { setTimeout(function () {
  var $ = function (id) { return document.getElementById(id); };
  var facts = {};
  facts.startHidden = $("panel").hidden;
  facts.startAria = $("toggle").getAttribute("aria-expanded");
  facts.startCond = !!$("cond");
  facts.legacyStart = $("legacy").textContent;
  facts.startClass = $("panel").className;
  $("toggle").click();
  facts.openHidden = $("panel").hidden;
  facts.openActive = $("panel").classList.contains("active");
  facts.openClass = $("panel").className;
  facts.oldBindClass = $("oldbind").className;
  facts.oldBindAttribute = $("oldbind").getAttribute("class.active");
  facts.openAria = $("toggle").getAttribute("aria-expanded");
  facts.openText = $("panel").textContent;
  facts.openCond = !!$("cond");
  facts.legacyAfter = $("legacy").textContent;
  $("panel").click();
  facts.insideClickHidden = $("panel").hidden;
  document.body.click();
  facts.outsideClickHidden = $("panel").hidden;
  facts.outsideClass = $("panel").className;
  facts.outsideCond = !!$("cond");
  $("field").dispatchEvent(new KeyboardEvent("keydown", { key: "a", bubbles: true }));
  facts.escAfterOtherKey = window.kit.scopeFor($("app")).esc;
  $("field").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  facts.escAfterEscape = window.kit.scopeFor($("app")).esc;
  facts.fieldStart = $("field").value;
  $("field").value = "typed";
  $("field").dispatchEvent(new Event("input", { bubbles: true }));
  facts.echo = $("echo").textContent;
  facts.width = $("bar").style.width;
  facts.backgroundImage = $("bar").style.backgroundImage;
  facts.progress = $("bar").style.getPropertyValue("--progress");
  facts.height = $("bar").style.height;
  facts.rows = Array.prototype.map.call($("list").children, function (li) { return li.textContent + "/" + li.getAttribute("data-kit-key"); }).join(",");
  facts.valueStart = $("value").textContent;
  $("bump").click();
  facts.valueAfter = $("value").textContent;
  facts.oldAfter = $("oldvalue").textContent;
  facts.errors = errors;
  document.getElementById("result").textContent = JSON.stringify(facts);
}, 50); });
</script>
<script data-kitwork-generated="runtime" src="/kit/kernel.js" defer></script>
<script data-kitwork-generated="component" src="/kit/counter.js" defer></script>
</body></html>`
	files := map[string]string{
		"/":               page,
		"/kit/kernel.js":  KernelFile(false),
		"/kit/counter.js": `window.kit.component("counter", { value: 0, step: 1, inc: function () { this.value += this.step; } });`,
	}
	output := runGroupedBrowserPage(t, browser, files, "3000")
	match := groupedResultRe.FindSubmatch(output)
	if match == nil || len(strings.TrimSpace(string(match[1]))) == 0 {
		t.Fatalf("the page reported nothing:\n%.1500s", output)
	}
	var facts map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(string(match[1]))), &facts); err != nil {
		t.Fatalf("the page's report is not JSON: %v\n%s", err, match[1])
	}
	want := map[string]any{
		"startHidden": true, "startAria": "false", "startCond": false, "legacyStart": "0",
		"openHidden": false, "openActive": true, "openAria": "true", "openText": "count=1", "openCond": true,
		"legacyAfter": "1", // control: the hand-written spelling paints in the same pass
		// data-kitwork-class: each named class toggled whole (py-1.5 keeps its dot), the static
		// "card" kept, in the order classList adds them
		"startClass": "card idle", "openClass": "card active py-1.5", "outsideClass": "card idle",
		// class.<name> in data-kitwork-bind toggles no class; the attribute it writes instead is the
		// control that the pair was read at all
		"oldBindClass": "", "oldBindAttribute": "",
		"insideClickHidden": false, "outsideClickHidden": true, "outsideCond": false,
		"escAfterOtherKey": float64(0), "escAfterEscape": float64(1),
		"fieldStart": "start", "echo": "typed",
		"width": "25%", "backgroundImage": "", "progress": "25", "height": "2px",
		"rows":       "0A/a,1B/b",
		"valueStart": "0", "valueAfter": "3", "oldAfter": "2",
	}
	for key, value := range want {
		if facts[key] != value {
			t.Errorf("%s = %#v, want %#v", key, facts[key], value)
		}
	}
	errors, _ := facts["errors"].([]any)
	refused := 0
	for _, e := range errors {
		if e == "hydrate: unsafe value for data-kitwork-style background-image@data-kitwork-style background-image" {
			refused++
		} else {
			t.Errorf("unexpected report: %v", e)
		}
	}
	if refused == 0 {
		t.Errorf("the refused url() must be reported naming data-kitwork-style background-image, got %v", errors)
	}
}

// A Kitwork UI page's progressbar host is data-kitwork-component="progressbar" (with or without a
// version or an alias), and Drive must find it there to read its data-kitwork-delay: delay 0 shows
// even a fast move. CONTROL: a host Drive does not recognise as a progressbar leaves Drive's own
// 120ms, which does not show the same move — so the test fails if Drive missed the grouped host.
func TestBrowserDriveFindsAGroupedProgressbarHost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the grouped progressbar proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	cases := []struct {
		name, host, showsUp string
	}{
		{"grouped host", `<div data-kitwork-component="progressbar" data-kitwork-delay="0"></div>`, "true"},
		{"grouped host with a version", `<div data-kitwork-component="progressbar@1.0.0" data-kitwork-delay="0"></div>`, "true"},
		{"grouped host with an alias", `<div data-kitwork-component="progressbar: $bar" data-kitwork-delay="0"></div>`, "true"},
		{"CONTROL: not a progressbar host", `<div data-kitwork-component="progressbars" data-kitwork-delay="0"></div>`, "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := watchProgressAfter(t, browser, c.host, 40); got != c.showsUp {
				t.Fatalf("kit.progress seen=%s, want %s", got, c.showsUp)
			}
		})
	}
}

// Drive takes an ordinary link and leaves alone a link whose data-kitwork-event has a click entry,
// as it does a data-kit-click link: the handler runs, and the browser follows the link its own way.
// The proof is the request header Drive adds (X-Kitwork-Hydrate: 1), recorded by the server.
func TestBrowserDriveLeavesAGroupedClickLinkAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the grouped Drive proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := func(title, body string) string {
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title></head><body data-kitwork-scope="hits: 0">` +
			body + pageRuntimeTags + `</body></html>`
	}
	var mu sync.Mutex
	headers := map[string]string{}
	files := map[string]string{
		"/kit/kernel.js": pageRuntimeFile("/kit/kernel.js"),
		"/kit/drive.js":  pageRuntimeFile("/kit/drive.js"),
	}
	// A navigation the browser makes itself never lets --dump-dom finish, so the browser is stopped
	// shortly after the server has seen the request that decides the test.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var decided sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		if _, seen := headers[request.URL.Path]; !seen {
			headers[request.URL.Path] = request.Header.Get("X-Kitwork-Hydrate")
		}
		mu.Unlock()
		if request.URL.Path == "/handled" {
			decided.Do(func() { time.AfterFunc(300*time.Millisecond, cancel) })
		}
		if body, ok := files[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(body))
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/":
			_, _ = response.Write([]byte(page("start", `<a id="plain" href="/soft">soft</a>`+
				`<script>window.addEventListener("load", function () {
				   setTimeout(function () { document.getElementById("plain").click(); }, 60);
				 });</script>`)))
		case "/soft":
			_, _ = response.Write([]byte(page("soft", `<a id="handled" href="/handled" data-kitwork-event="click.once: hits++; keydown.escape: hits = 0">handled</a>`+
				`<script>window.addEventListener("kitwork:load", function () {
				   setTimeout(function () { var a = document.getElementById("handled"); if (a) a.click(); }, 60);
				 });
				 setTimeout(function () { var a = document.getElementById("handled"); if (a) a.click(); }, 400);</script>`)))
		case "/handled":
			_, _ = response.Write([]byte(page("handled", `<b>arrived</b>`)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=5000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	mu.Lock()
	defer mu.Unlock()
	soft, softSeen := headers["/soft"]
	if !softSeen || soft != "1" {
		t.Fatalf("control: the ordinary link must be driven (X-Kitwork-Hydrate=%q, seen=%v)\n%.600s", soft, softSeen, output)
	}
	handled, handledSeen := headers["/handled"]
	if !handledSeen {
		t.Fatalf("the link with a grouped click was never followed; requests=%v\n%.600s", headers, output)
	}
	if handled != "" {
		t.Fatalf("a link whose data-kitwork-event has a click entry must be left to the browser, but Drive fetched it (X-Kitwork-Hydrate=%q)", handled)
	}
}
