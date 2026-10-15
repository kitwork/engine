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

// router.transition reaches the browser as <html data-kitwork-transition> on the page arriving, and
// Drive moves the content that way: it marks <html data-kitwork-moving> for the length of a View
// Transition. "slide" is "slide back" on the Back button; a page with no transition swaps at once.
//
// One controller script outside <main> (Drive swaps only <main>, so it lives through every move)
// walks the steps and records every value Drive gives data-kitwork-moving, then reports them to the
// server. Control: the move to /plain, a page with no transition, must add nothing.
func TestBrowserDriveTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the transition browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	links := `<a id="a" href="/a">a</a><a id="b" href="/b">b</a><a id="plain" href="/plain">plain</a><a id="f" href="/f">f</a>`
	page := func(title, transition, extra string) string {
		html := `<html>`
		if transition != "" {
			html = `<html data-kitwork-transition="` + transition + `">`
		}
		return `<!doctype html>` + html + `<head><meta charset="utf-8"><title>` + title +
			`</title><style data-kitwork-generated="css">` + title + `</style></head><body><main><h1>` + title + `</h1>` + links + `</main>` + extra + pageRuntimeTags + `</body></html>`
	}
	controller := `<script>
	  var moves = [];
	  var root = document.documentElement, set = root.setAttribute;
	  root.setAttribute = function (name, value) {
	    if (name === "data-kitwork-moving") moves.push(value);
	    return set.apply(this, arguments);
	  };
	  // the page's own stylesheet at the moment a View Transition is asked for: the old snapshot is taken
	  // then, so it must still be the page being left (the arriving page's block already stands, off
	  // the screen until the swap: media="print", not counted)
	  var snaps = [];
	  var startViewTransition = document.startViewTransition && document.startViewTransition.bind(document);
	  if (startViewTransition) document.startViewTransition = function (update) {
	    snaps.push(Array.prototype.map.call(document.head.querySelectorAll('style[data-kitwork-generated="css"]:not([media="print"])'), function (s) { return s.textContent; }).join("+"));
	    return startViewTransition(update);
	  };
	  function click(id) { return function () { document.getElementById(id).click(); }; }
	  var steps = [click("a"), click("b"), function () { history.back(); }, function () { history.forward(); }, click("plain"), click("f"), function () {
	    fetch("/report?moves=" + encodeURIComponent(moves.join(",")) +
	      "&snaps=" + encodeURIComponent(snaps.join(",")) +
	      "&final=" + encodeURIComponent(root.getAttribute("data-kitwork-transition") || "") +
	      "&path=" + encodeURIComponent(location.pathname) +
	      "&style=" + (document.querySelector('style[data-kitwork-generated="transition"]') ? "1" : "0"));
	  }];
	  fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	  var i = 0;
	  function next() { var step = steps[i++]; if (step) setTimeout(step, 80); }
	  document.addEventListener("kitwork:load", next);
	  window.addEventListener("load", function () { setTimeout(next, 60); });
	</script>`

	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report map[string]string
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
			_, _ = response.Write([]byte(page("start", "", controller)))
		case "/a":
			_, _ = response.Write([]byte(page("a", "slide", "")))
		case "/b":
			_, _ = response.Write([]byte(page("b", "slide", "")))
		case "/plain":
			_, _ = response.Write([]byte(page("plain", "", "")))
		case "/f":
			_, _ = response.Write([]byte(page("f", "fade", "")))
		case "/hold":
			select {
			case <-reported:
			case <-time.After(10 * time.Second):
			}
		case "/report":
			q := request.URL.Query()
			mu.Lock()
			report = map[string]string{"moves": q.Get("moves"), "final": q.Get("final"), "path": q.Get("path"), "style": q.Get("style"), "snaps": q.Get("snaps")}
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=8000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == nil {
		t.Fatalf("the controller never reported; requests=%v\n%.600s", hits, output)
	}
	if report["path"] != "/f" {
		t.Fatalf("the walk did not end on /f (path=%q); requests=%v", report["path"], hits)
	}
	// a then b slide forward, Back slides back to a, Forward slides forward to b again, plain swaps at
	// once (nothing), f fades
	if want := "slide,slide,slide back,slide,fade"; report["moves"] != want {
		t.Errorf("moves = %q, want %q", report["moves"], want)
	}
	if report["final"] != "fade" {
		t.Errorf("after the move to /f, <html> must carry that page's transition, got %q", report["final"])
	}
	// each View Transition is asked for while the page being left still has its own stylesheet
	if want := "start,a,b,a,plain"; report["snaps"] != want {
		t.Errorf("stylesheet at each old snapshot = %q, want %q (the page being left)", report["snaps"], want)
	}
	if report["style"] != "1" {
		t.Error("the transition's CSS was never added to the page")
	}
	for _, h := range hits {
		if h == "/a " || h == "/b " || h == "/plain " || h == "/f " {
			t.Errorf("a page was loaded whole instead of moved to (%s); requests=%v", h, hits)
		}
	}
	t.Logf("moves=%q final=%q", report["moves"], report["final"])
}
