package work

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// driveStaleFiles is a site that bundles its JavaScript parts (router.bundle({ javascript: "split" })):
// / has a dropdown, /hai a dropdown and an appearance switch, so each page's bundle is another set.
var driveStaleFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.bundle({ javascript: "split" });
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content>
  <text id="title">Một</text>
  <box component="dropdown"><button id="open" click="toggle()" expanded="{{ open }}">Mở</button></box>
  <link id="next" value="/hai">Hai</link>
</content>`,
	"localhost/hai/content.ui": `<content>
  <text id="title">Hai</text>
  <box component="dropdown"><button id="open" click="toggle()" expanded="{{ open }}">Mở</button></box>
  <box id="mode"><button component="appearance" click="toggle()">Đổi</button></box>
</content>`,
}

// driveStaleController clicks what the page offers once it has loaded (the dropdown, the appearance
// switch on /hai) and reads, a moment later, whether the dropdown answered, the dark mode, and whether
// Drive runs; then moves to /hai by its link when Drive runs (else it reports: a whole load would lose
// it) and does the same on arrival.
const driveStaleController = `<script>
(function () {
  var log = {};
  function send() {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(log) }).then(close, close);
  }
  function check(at, then) {
    setTimeout(function () {
      var was = document.getElementById("open").getAttribute("aria-expanded");
      document.getElementById("open").click();
      var mode = document.querySelector("#mode button");
      if (mode) mode.click();
      setTimeout(function () {
        var drive = !!(window.kit && window.kit.drive);
        log[at] = "toggled=" + (document.getElementById("open").getAttribute("aria-expanded") !== was) +
          " dark=" + document.documentElement.classList.contains("dark") + " drive=" + drive;
        if (drive && then) then(); else send();
      }, 150);
    }, 150);
  }
  document.addEventListener("kitwork:load", function () { check("hai", null); });
  window.addEventListener("load", function () { check("start", function () { document.getElementById("next").click(); }); });
})();
</script>`

// staleBundleName is a bundle name no generation made: each hex digit of the real one moved by one.
var staleBundleName = regexp.MustCompile(`/_kitwork/bundle\.([0-9a-f]{16})\.js`)

func staleName(name string) string {
	return staleBundleName.ReplaceAllStringFunc(name, func(path string) string {
		hash := []byte(staleBundleName.FindStringSubmatch(path)[1])
		for i, c := range hash {
			hash[i] = "123456789abcdef0"[strings.IndexByte("0123456789abcdef", c)]
		}
		return "/_kitwork/bundle." + string(hash) + ".js"
	})
}

// TestBrowserStaleBundleNameKeepsThePageWorking serves every page with its bundle's name replaced by
// one the generation never made, which is what a browser holding a page from before a deploy asks for
// (its HTML kept, served by the generation before, or fetched ahead by Drive's hover): the page still
// gets its components and Drive, on a whole load and on a Drive move to a page whose bundle is
// another set. Until the fix of 14/10/2026 such a name was a 404: the dropdown never answered and Drive never
// ran. CONTROL: every bundle the browser asked for was a name the generation never made.
func TestBrowserStaleBundleNameKeepsThePageWorking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the stale bundle browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, driveStaleFiles)
	var lock sync.Mutex
	asked, moved := map[string]int{}, map[string]bool{}
	reports := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/report" {
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
		tenant.Serve(answer, forward)
		lock.Lock()
		asked[request.URL.Path]++
		if request.Header.Get("X-Kitwork-Hydrate") != "" {
			moved[request.URL.Path] = true
		}
		lock.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		if strings.HasPrefix(answer.Header().Get("Content-Type"), "text/html") {
			body = staleName(body)
			if request.URL.Path == "/" && request.Header.Get("X-Kitwork-Hydrate") == "" {
				at := strings.LastIndex(body, "</body>")
				body = body[:at] + driveStaleController + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(server.Close)
	got := runBrowser(t, browser, server.URL+"/", "stale bundle", 1000, "", reports)

	want := map[string]string{
		"start": "toggled=true dark=false drive=true",
		"hai":   "toggled=true dark=true drive=true",
	}
	for _, step := range []string{"start", "hai"} {
		if got[step] != want[step] {
			t.Errorf("%s:\n got  %s\n want %s", step, got[step], want[step])
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if !moved["/hai"] {
		t.Errorf("/hai was not moved to by Drive (requests %v)", asked)
	}
	bundles := 0
	for path := range asked {
		if !strings.HasPrefix(path, "/_kitwork/bundle.") {
			continue
		}
		bundles++
		if plan := tenant.renderPlan(); plan != nil {
			if _, made := plan.ui.script(strings.TrimSuffix(strings.TrimPrefix(path, "/_kitwork/"), ".js")); made {
				t.Errorf("CONTROL: the browser asked for %s, a bundle the generation made", path)
			}
		}
	}
	if bundles != 2 {
		t.Errorf("CONTROL: the browser asked for the two pages' bundles, got %v", asked)
	}
}
