package hydrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// The engine's own stylesheets were marked data-kitwork-jit until 10/10/2026 and data-kitwork-generated
// since. A cached page keeps the old name while its tab runs today's Drive (an old file name is answered
// with today's file), so a swap can go old → new → old. Each time, <head> must end up with exactly the
// arriving page's stylesheet: none of the page left behind, none missing.
//
// One controller script outside <main> walks the moves and reports which stylesheets were in <head>
// after each; the server records the report.
func TestBrowserDriveSwapsStylesUnderBothMarkerNames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the legacy marker browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	page := func(title, style, extra string) string {
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>` + style + `</head>` +
			`<body><main><h1>` + title + `</h1><a id="new" href="/new">new</a><a id="old" href="/old">old</a></main>` +
			extra + pageRuntimeTags + `</body></html>`
	}
	controller := `<script>
	  var seen = [];
	  function styles() {
	    return Array.prototype.map.call(document.head.querySelectorAll("style"), function (s) { return s.textContent.trim(); }).join("+");
	  }
	  function click(id) { return function () { document.getElementById(id).click(); }; }
	  var steps = [click("new"), click("old"), function () {
	    fetch("/report?seen=" + encodeURIComponent(seen.join(",")));
	  }];
	  var i = 0;
	  function next() { var step = steps[i++]; if (step) setTimeout(step, 80); }
	  document.addEventListener("kitwork:load", function () { seen.push(location.pathname + "=" + styles()); next(); });
	  window.addEventListener("load", function () { seen.push(location.pathname + "=" + styles()); setTimeout(next, 60); });
	  fetch("/hold"); // keeps the browser's clock running until the report arrives
	</script>`

	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report string
	var hits []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		hits = append(hits, request.URL.Path+" "+request.Header.Get("X-Kitwork-Hydrate"))
		mu.Unlock()
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			_, _ = response.Write([]byte(page("start", `<style data-kitwork-jit="css">start</style>`, controller)))
		case "/new":
			_, _ = response.Write([]byte(page("new", `<style data-kitwork-generated="css">new</style>`, "")))
		case "/old":
			_, _ = response.Write([]byte(page("old", `<style data-kitwork-jit="css">old</style>`, "")))
		case "/hold":
			select {
			case <-reported:
			case <-time.After(8 * time.Second):
			}
		case "/report":
			mu.Lock()
			report = request.URL.Query().Get("seen")
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == "" {
		t.Fatalf("the controller never reported; requests=%v\n%.600s", hits, output)
	}
	// start (old name) → new (new name) → old (old name): each page's own stylesheet, and only it
	if want := "/=start,/new=new,/old=old"; report != want {
		t.Errorf("stylesheets in <head> after each move = %q, want %q", report, want)
	}
	for _, h := range hits {
		if h == "/new " || h == "/old " {
			t.Errorf("a page was loaded whole instead of swapped (%s); requests=%v", h, hits)
		}
	}
}
