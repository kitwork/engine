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

// A desktop window opened with app.desktop({ chrome: "html" }) draws its own title bar, marked
// data-kit-drag="true". Drive's progress bar goes under it, where a browser's own progress sits,
// instead of across the window's controls. A drag region that does not touch the top edge is not a
// title bar, and a page without one keeps the bar at the very top.
//
// The proof is a real Chrome: the next page answers 500ms late, so the navigation is slow enough
// for the bar to show, and the page records where the bar landed.
func TestBrowserDriveProgressSitsUnderTheTitleBar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive progress browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	cases := []struct {
		name, chrome, want string
	}{
		{"title bar", `<header data-kit-drag="true" style="position:fixed;top:0;left:0;right:0;height:40px"></header>`, "40"},
		{"no title bar", ``, "0"},
		{"drag region below the top", `<div data-kit-drag="true" style="position:fixed;top:300px;left:0;width:80px;height:40px"></div>`, "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := driveBarTop(t, browser, c.chrome); got != c.want {
				t.Fatalf("progress bar top = %q, want %q", got, c.want)
			}
		})
	}
}

var barTopAttribute = regexp.MustCompile(`data-bar-top="([^"]*)"`)

func driveBarTop(t *testing.T, browser, chrome string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(`<!doctype html><html data-kit-hydrate="v1"><head><meta charset="utf-8"><title>start</title></head>` +
				`<body style="margin:0">` + chrome + `<main><a id="next" href="/next">next</a></main>` +
				// A slow server, on the page's own clock: the answer comes 500ms later. A response held
				// open on the wire would stop headless Chrome's virtual clock, and the bar's 120ms
				// timer with it.
				`<script>window.fetch = function () {
				   return new Promise(function (resolve) {
				     setTimeout(function () {
				       resolve(new Response("<!doctype html><html><head><title>next</title></head><body><main>next</main></body></html>",
				         { headers: { "Content-Type": "text/html; charset=utf-8" } }));
				     }, 500);
				   });
				 };
				 window.addEventListener("load", function () {
				   setTimeout(function () { document.getElementById("next").click(); }, 60);
				   (function watch() {
				     var bar = document.querySelector('[data-kitwork-ui="progress"]');
				     if (bar && bar.style.opacity === "1") {
				       document.documentElement.setAttribute("data-bar-top", String(Math.round(bar.getBoundingClientRect().top)));
				       return;
				     }
				     setTimeout(watch, 20);
				   })();
				 });</script>` +
				pageRuntimeTags + `</body></html>`))
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
		"--window-size=800,600", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"),
		"--virtual-time-budget=3000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	match := barTopAttribute.FindSubmatch(output)
	if match == nil {
		t.Fatalf("the progress bar never showed during a slow navigation:\n%.800s", output)
	}
	return string(match[1])
}
