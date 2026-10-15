package hydrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"
)

// Two moves at once. A newer click aborts the older fetch, but the older chain still settles later
// (an abort rejects asynchronously; a page fetched ahead was never given a signal; a swap can wait for a
// component file or a View Transition). The older move must then touch nothing: not the newer move's
// bar, loading class or watchdog, and above all not the page, so the last click is the page shown.
//
// Real Chrome. fetch is replaced on the page's own clock (virtual time), honouring the abort signal as
// the real one does. The page records what it saw and the test reads it from the dumped DOM.
func TestBrowserDriveLastClickWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the overlapping-moves browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	cases := []struct {
		name, delays, transitions, clicks, want string
	}{
		// a answers after 600ms, b after 800ms, c after 100ms; clicked at 60, 300 and 500ms. At 450ms b
		// is still loading, so the loading class and the bar are on; c is the page in the end.
		{"three quick clicks", `{"/a":600,"/b":800,"/c":100}`, `{}`,
			`[["/a",60],["/b",300],["/c",500]]`, `at450=loading,bar end=/c:content c:c`},
		// a fades: in headless Chrome its swap waits for the transition guard (1s), so a's change is still
		// pending when b, clicked at 400ms, lands at 440ms. a's late change (at about 1.1s) must not undo b.
		// At 450ms b is done (idle) and its bar is finishing: kit.progress holds 100% for 300ms.
		{"a waiting swap overtaken", `{"/a":40,"/b":40}`, `{"/a":"fade"}`,
			`[["/a",60],["/b",400]]`, `at450=idle,bar end=/b:content b:b`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := overlappingMoves(t, browser, c.delays, c.transitions, c.clicks); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

var overlappingReport = regexp.MustCompile(`data-moves-report="([^"]*)"`)

func overlappingMoves(t *testing.T, browser, delays, transitions, clicks string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>start</title></head>` +
				`<body><main><h1>start</h1><a href="/a">a</a><a href="/b">b</a><a href="/c">c</a></main>` +
				`<script>
				 var delays = ` + delays + `, transitions = ` + transitions + `, clicks = ` + clicks + `;
				 function page(path) {
				   var name = path.slice(1), tr = transitions[path] ? ' data-kitwork-transition="' + transitions[path] + '"' : "";
				   return "<!doctype html><html" + tr + "><head><title>" + name + "</title></head><body><main><h1>content " + name + "</h1></main></body></html>";
				 }
				 window.fetch = function (url, options) {
				   var path = new URL(url, location.href).pathname;
				   return new Promise(function (resolve, reject) {
				     var timer = setTimeout(function () {
				       resolve(new Response(page(path), { headers: { "Content-Type": "text/html; charset=utf-8" } }));
				     }, delays[path] || 10);
				     if (options && options.signal) options.signal.addEventListener("abort", function () {
				       clearTimeout(timer);
				       reject(new DOMException("The user aborted a request.", "AbortError"));
				     });
				   });
				 };
				 window.addEventListener("load", function () {
				   var root = document.documentElement, at450 = "";
				   clicks.forEach(function (click) {
				     setTimeout(function () { window.kit.drive.visit(new URL(click[0], location.href).href, true, 0); }, click[1]);
				   });
				   setTimeout(function () {
				     at450 = (root.classList.contains("kitwork-loading") ? "loading" : "idle") + "," + (window.kit.progress.active ? "bar" : "nobar");
				   }, 450);
				   setTimeout(function () {
				     var h1 = document.querySelector("main h1");
				     root.setAttribute("data-moves-report", "at450=" + at450 + " end=" + location.pathname + ":" + (h1 ? h1.textContent : "") + ":" + document.title);
				   }, 2200);
				 });</script>` + pageRuntimeTags + `</body></html>`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=4000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	match := overlappingReport.FindSubmatch(output)
	if match == nil {
		t.Fatalf("the page never reported:\n%.800s", output)
	}
	return string(match[1])
}
