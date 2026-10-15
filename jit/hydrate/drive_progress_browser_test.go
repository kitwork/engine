package hydrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"
)

// progressbarHostForTest is the host ui.ProgressbarMarkup writes (this package cannot import ui: ui
// imports it), with the line's colour inline so the proof needs no CSS pass.
const progressbarHostForTest = `<div style="position:fixed;left:0;right:0;top:0;height:0;z-index:50;pointer-events:none" data-kitwork-component="progressbar"` +
	` data-kitwork-bind="show: active; aria-valuenow: value" hidden role="progressbar" aria-label="Loading the next page" aria-valuemin="0" aria-valuemax="100"` +
	` data-kitwork-style="top: top"><div style="height:1px;background:#c8742c" data-kitwork-style="width: value + '%'"></div></div>`

// progressbarComponentFile is the catalogue's progressbar, read from its source, wrapped as the
// engine wraps a component file.
func progressbarComponentFile(t *testing.T) []byte {
	source, err := os.ReadFile(filepath.Join("..", "js", "components", "progressbar", "v1.0.0.js"))
	if err != nil {
		t.Fatalf("the progressbar component: %v", err)
	}
	return []byte(";(function () {\n" + string(source) + "\n})();")
}

// A desktop window opened with app.desktop({ chrome: "html" }) draws its own title bar, marked
// data-kit-drag="true". The bar between pages goes under it, where a browser's own progress sits,
// instead of across the window's controls. A drag region that does not touch the top edge is not a
// title bar, and a page without one keeps the bar at the very top.
//
// The bar is the progressbar COMPONENT (Drive draws none of its own since 10/10/2026), so this is the
// whole chain: Drive reports the move to kit.progress, the component shows, and its top getter puts
// the host under the title bar. The host is the one the engine writes (ui.ProgressbarMarkup), the
// component file is the catalogue's own. A real Chrome; the next page answers 500ms late, so the move
// is slow enough for the bar to show, and the page records where the bar landed.
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
		case "/kit/progressbar.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(progressbarComponentFile(t))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(`<!doctype html><html data-kit-hydrate="v1"><head><meta charset="utf-8"><title>start</title></head>` +
				`<body style="margin:0">` + progressbarHostForTest + chrome + `<main><a id="next" href="/next">next</a></main>` +
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
				     var host = document.querySelector('[data-kitwork-component="progressbar"]');
				     if (host && !host.hidden && window.kit.progress.active) {
				       document.documentElement.setAttribute("data-bar-top", String(Math.round(host.getBoundingClientRect().top)));
				       return;
				     }
				     setTimeout(watch, 20);
				   })();
				 });</script>` +
				pageRuntimeTags + `<script src="/kit/progressbar.js" defer></script></body></html>`))
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
