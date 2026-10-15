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

// Drive makes one element for itself: the transition stylesheet. (It made a live region for screen
// readers too until 11/10/2026; the new page's title is now said through the reader component, which
// the server puts on the page, so Drive must make no region of its own.) The fetched HTML never has
// the stylesheet, so a move must neither remove nor duplicate it, and that is remembered in memory
// (clientOwned in morph.js), not by an attribute: data-kitwork-generated marks it, but it also marks
// what the SERVER generated, which a move must replace like anything else.
//
// The pages have no <main>, so a move morphs the whole <body>, the case that used to lose overlays. The
// walk is / → /b (fade) → /c (no transition) → /b. After each move: no region made by Drive; the
// transition stylesheet, once made, still there exactly once (also after a move with no transition);
// and the server's own stylesheet exactly once, never piling up.
//
// A second walk changes layout on every move, so the morph runs on the whole <html> (the shell),
// <head> included: there the transition stylesheet survives only because morph leaves what the client
// made alone.
func TestBrowserDriveKeepsWhatItMadeForItself(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the client-owned browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	for _, shell := range []bool{false, true} {
		name := "body"
		if shell {
			name = "whole html, layout changing"
		}
		t.Run(name, func(t *testing.T) { walkClientOwned(t, browser, shell) })
	}
}

func walkClientOwned(t *testing.T, browser string, shell bool) {

	controller := `<script>
	  var seen = [];
	  function count(selector) { return document.querySelectorAll(selector).length; }
	  function look() {
	    seen.push(location.pathname + ":regions=" + count('[aria-live]') +
	      ",transition=" + count('style[data-kitwork-generated="transition"]') +
	      ",css=" + count('style[data-kitwork-generated="css"]'));
	  }
	  function click(href) { return function () { document.querySelector('a[href="' + href + '"]').click(); }; }
	  var steps = [click("/b"), click("/c"), click("/b"), function () {
	    fetch("/report?seen=" + encodeURIComponent(seen.join(" ")));
	  }];
	  var i = 0;
	  document.addEventListener("kitwork:load", function () { setTimeout(function () { look(); var s = steps[i++]; if (s) setTimeout(s, 60); }, 120); });
	  window.addEventListener("load", function () { setTimeout(function () { var s = steps[i++]; if (s) s(); }, 80); });
	  fetch("/hold"); // keeps the browser's clock running until the report arrives
	</script>`
	layouts := map[string]string{"start": "one", "b": "two", "c": "one"}
	page := func(title, transition, extra string) string {
		attrs, body := "", ""
		if transition != "" {
			attrs += ` data-kitwork-transition="` + transition + `"`
		}
		if shell {
			attrs += ` data-kitwork-shell`
			body = ` data-kitwork-layout="` + layouts[title] + `"`
		}
		html := `<html` + attrs + `>`
		return `<!doctype html>` + html + `<head><meta charset="utf-8"><title>` + title + `</title>` +
			`<style data-kitwork-generated="css">.page-` + title + `{}</style>` + extra + pageRuntimeTags + `</head>` +
			`<body` + body + `><h1>` + title + `</h1><a href="/b">b</a><a href="/c">c</a></body></html>`
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
			_, _ = response.Write([]byte(page("start", "", controller)))
		case "/b":
			_, _ = response.Write([]byte(page("b", "fade", "")))
		case "/c":
			_, _ = response.Write([]byte(page("c", "", "")))
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
	want := "/b:regions=0,transition=1,css=1 /c:regions=0,transition=1,css=1 /b:regions=0,transition=1,css=1"
	if report != want {
		t.Errorf("after each move:\n got %s\nwant %s", report, want)
	}
}
