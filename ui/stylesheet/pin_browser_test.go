package stylesheet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// pin in a real browser, a window 1000×800 (medium holds, large does not):
//   - pin="top": the header keeps its place (the content starts under it, nothing is covered) and,
//     after the page scrolls, it is still at the top edge, drawn over the content; pin="bottom" sits
//     on the bottom edge while the page scrolls;
//   - a jump to an #anchor stops clear of the top-pinned header (reboot.css's room), and of the
//     bottom bar when it scrolls up to one;
//   - pin="none" medium:pin="top": not pinned below medium, pinned from it (the window is medium);
//     pin="top" medium:pin="none": pinned below medium, back in place from it;
//   - the pinned header is above the content and below a floating layer (popover's 50).
//
// Control: the same header made sticky without a pin class (the browser's own sticky, no reboot room)
// lets the jump end under it.
func TestBrowserPinHoldsInABrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the pin browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	table := NewTable(examplePalette(t))
	var rules Rules
	classes := func(written string) string {
		var out []string
		for _, field := range strings.Fields(written) {
			name, value, _ := strings.Cut(field, "=")
			in := Intent{Name: name, Value: value}
			if i := strings.LastIndex(name, ":"); i >= 0 {
				in.Width, in.Name = name[:i], name[i+1:]
			}
			class, err := table.Class(&rules, in)
			if err != nil {
				t.Fatalf("%s: %v", field, err)
			}
			out = append(out, class)
		}
		return strings.Join(out, " ")
	}
	body := `
<header id="header" class="flex-column ` + classes("background=canvas pin=top height=80px") + `"></header>
<div id="first" class="flex-column ` + classes("height=100px") + `"></div>
<div id="from-medium" class="flex-column ` + classes("pin=none medium:pin=top height=10px") + `"></div>
<div id="below-medium" class="flex-column ` + classes("pin=top medium:pin=none height=10px") + `"></div>
<div class="flex-column ` + classes("height=1500px") + `"></div>
<h2 id="target">Đích</h2>
<div class="flex-column ` + classes("height=1500px") + `"></div>
<div id="floating" class="popover"></div>
<footer id="bar" class="flex-column ` + classes("background=canvas pin=bottom height=60px") + `"></footer>
`
	script := `<script>
	fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	window.addEventListener("load", function () {
	  var q = function (id) { return document.getElementById(id); };
	  var rect = function (id) { return q(id).getBoundingClientRect(); };
	  var style = function (id, property) { return getComputedStyle(q(id)).getPropertyValue(property).trim(); };
	  var r = {};
	  r.place = [Math.round(rect("header").top), Math.round(rect("first").top)].join(" ");
	  r.bar = String(Math.round(innerHeight - rect("bar").bottom));
	  r.widths = style("from-medium", "position") + " " + style("below-medium", "position");
	  r.layers = style("header", "z-index") + " " + style("floating", "z-index");
	  window.scrollTo(0, 600);
	  r.scrolled = [Math.round(rect("header").top), Math.round(innerHeight - rect("bar").bottom)].join(" ");
	  r.over = String(document.elementFromPoint(500, 40) === q("header"));
	  window.scrollTo(0, 0);
	  location.hash = "#target";
	  r.jump = String(Math.round(rect("target").top));
	  r.room = style("html", "scroll-padding-top") || getComputedStyle(document.documentElement).scrollPaddingTop;
	  // control: the same header sticky by the browser's own rule, no pin class: no room is kept
	  window.scrollTo(0, 0);
	  location.hash = "";
	  Array.prototype.forEach.call(document.querySelectorAll("[class*=pin-]"), function (n) { n.className = "flex-column"; });
	  q("header").setAttribute("style", "position:sticky;top:0;height:80px;z-index:40");
	  location.hash = "#target";
	  r.control = String(Math.round(rect("target").top));
	  fetch("/report", { method: "POST", body: JSON.stringify(r) });
	});
	</script>`
	html := `<!doctype html><html lang="vi" id="html"><head><meta charset="utf-8"><title>pin</title>` +
		`<link rel="stylesheet" href="/reboot.css"><link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/page.css"></head>` +
		`<body class="flex-column">` + body + script + `</body></html>`
	got := runBrowserReport(t, browser, html, map[string][]byte{
		"/reboot.css": Reboot().CSS,
		"/colors.css": Colors(examplePalette(t)).CSS,
		"/page.css":   rules.CSS(),
	})
	want := map[string]string{
		"place":    "0 80", // the header keeps its place: the content starts under it
		"bar":      "0",    // the bar sits on the bottom edge
		"widths":   "sticky static",
		"layers":   "40 50", // above the content, below what floats
		"scrolled": "0 0",   // still on the edges after the page scrolled
		"over":     "true",  // drawn over the content that scrolls under it
		"jump":     "96",    // clear of the 80px header: 6rem
		"room":     "96px",
		"control":  "0", // CONTROL: no room, the target ends under the header
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], value)
		}
	}
}

// runBrowserReport serves html at / and the files beside it (CSS, or a page it frames when the path
// ends in .html), opens it in a headless browser at 1000×800, and returns what the page POSTs to
// /report (a JSON object of strings).
func runBrowserReport(t *testing.T, browser, html string, files map[string][]byte) map[string]string {
	t.Helper()
	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report []byte
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if file, ok := files[request.URL.Path]; ok {
			kind := "text/css; charset=utf-8"
			if strings.HasSuffix(request.URL.Path, ".html") {
				kind = "text/html; charset=utf-8" // a page the test page frames
			}
			response.Header().Set("Content-Type", kind)
			_, _ = response.Write(file)
			return
		}
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, html)
		case "/hold":
			select {
			case <-reported:
			case <-time.After(15 * time.Second):
			}
		case "/report":
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			report = body
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw", "--window-size=1000,800",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	mu.Lock()
	defer mu.Unlock()
	if report == nil {
		t.Fatalf("the page never reported\n%.700s", output)
	}
	got := map[string]string{}
	if err := json.Unmarshal(report, &got); err != nil {
		t.Fatalf("report %s: %v", report, err)
	}
	return got
}
