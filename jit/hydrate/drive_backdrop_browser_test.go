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

// A Kitwork UI page with a backdrop (<page background="cirrus">, decision D4 of 13/10/2026) paints it on
// a layer fixed to the screen, first in <body>, outside the <main> a move swaps, and names its colour
// for the browser's bar in <meta name="theme-color">. A move shows what a whole load of the next page
// would, without a flash: the walk / (cirrus) → /b (cirrus) → /c (dusk) → /d (none) → /e (dusk). After
// each move: the same backdrop keeps the very node on screen (nothing repaints); another puts a new
// layer in its place, exactly one; none takes it away; and the theme-color is the arriving page's, or
// none.
func TestBrowserDriveKeepsTheBackdrop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the backdrop Drive browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	controller := `<script>
	  var seen = [], last = null; // the layer on screen, read once the body is there (this runs in <head>)
	  function look() {
	    var layers = document.querySelectorAll("[data-kitwork-backdrop]"), layer = layers[0] || null;
	    var themes = Array.prototype.map.call(document.querySelectorAll('meta[name="theme-color"]'), function (m) { return m.content; });
	    seen.push(location.pathname + ":layers=" + layers.length + ",same=" + (layer !== null && layer === last) +
	      ",class=" + (layer ? layer.className.replace(/ /g, "+") : "") + ",first=" + (document.body.firstElementChild === layer) +
	      ",theme=" + themes.join("|"));
	    last = layer;
	  }
	  function click(href) { return function () { document.querySelector('a[href="' + href + '"]').click(); }; }
	  var steps = [click("/b"), click("/c"), click("/d"), click("/e"), function () {
	    fetch("/report?seen=" + encodeURIComponent(seen.join(" ")));
	  }];
	  var i = 0;
	  document.addEventListener("kitwork:load", function () { setTimeout(function () { look(); var s = steps[i++]; if (s) setTimeout(s, 60); }, 120); });
	  window.addEventListener("load", function () { last = document.querySelector("[data-kitwork-backdrop]"); setTimeout(function () { var s = steps[i++]; if (s) s(); }, 80); });
	  fetch("/hold"); // keeps the browser's clock running until the report arrives
	</script>`
	grounds := map[string]string{"cirrus": "#849cdd", "dusk": "#ca92b9"}
	page := func(title, backdrop, extra string) string {
		head := `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>`
		layer := ""
		if backdrop != "" {
			head += `<meta name="theme-color" media="(prefers-color-scheme: light)" content="` + grounds[backdrop] + `">`
			layer = `<div class="page-backdrop background-` + backdrop + `" aria-hidden="true" data-kitwork-backdrop></div>`
		}
		return head + extra + pageRuntimeTags + `</head><body data-kitwork-layout="page.ui">` + layer +
			`<header>Quán</header><main><h1>` + title + `</h1><a href="/b">b</a><a href="/c">c</a><a href="/d">d</a><a href="/e">e</a></main></body></html>`
	}

	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			_, _ = response.Write([]byte(page("start", "cirrus", controller)))
		case "/b":
			_, _ = response.Write([]byte(page("b", "cirrus", "")))
		case "/c", "/e":
			_, _ = response.Write([]byte(page(request.URL.Path[1:], "dusk", "")))
		case "/d":
			_, _ = response.Write([]byte(page("d", "", "")))
		case "/hold":
			select {
			case <-reported:
			case <-time.After(10 * time.Second):
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

	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=9000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == "" {
		t.Fatalf("the walk never reported:\n%.700s", output)
	}
	want := "/b:layers=1,same=true,class=page-backdrop+background-cirrus,first=true,theme=#849cdd " +
		"/c:layers=1,same=false,class=page-backdrop+background-dusk,first=true,theme=#ca92b9 " +
		"/d:layers=0,same=false,class=,first=false,theme= " +
		"/e:layers=1,same=false,class=page-backdrop+background-dusk,first=true,theme=#ca92b9"
	if report != want {
		t.Errorf("after each move:\n got %s\nwant %s", report, want)
	}
}
