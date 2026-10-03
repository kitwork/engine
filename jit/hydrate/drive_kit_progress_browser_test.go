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

// A page change is work in flight like any other: Drive reports it to kit.progress. A page that
// shows kit.progress itself — it has a progressbar host — gets no bar of Drive's own, so it never
// shows two; a page without one keeps Drive's bar. Real Chrome, a navigation answered 500ms late.
func TestBrowserDriveReportsToKitProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive progress browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	cases := []struct {
		name, host           string
		driveBar, kitProgress string
	}{
		{"a progressbar host", `<div data-kit-component="progressbar"></div>`, "false", "true"},
		{"a pinned progressbar host", `<div data-kit-component="progressbar@1.0.0"></div>`, "false", "true"},
		{"CONTROL: no progressbar host", ``, "true", "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			driveBar, kitProgress := watchNavigationProgress(t, browser, c.host)
			if driveBar != c.driveBar || kitProgress != c.kitProgress {
				t.Fatalf("Drive's bar seen=%s (want %s), kit.progress active seen=%s (want %s)", driveBar, c.driveBar, kitProgress, c.kitProgress)
			}
		})
	}
}

var navigationProgressRe = regexp.MustCompile(`data-drive-bar="(true|false)"[^>]*data-kit-progress-seen="(true|false)"`)

func watchNavigationProgress(t *testing.T, browser, host string) (driveBar, kitProgress string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(`<!doctype html><html data-kit-hydrate="v1"><head><meta charset="utf-8"><title>start</title></head>` +
				`<body>` + host + `<main><a id="next" href="/next">next</a></main>` +
				`<script>window.fetch = function () {
				   return new Promise(function (resolve) {
				     setTimeout(function () {
				       resolve(new Response("<!doctype html><html><head><title>next</title></head><body><main>next</main></body></html>",
				         { headers: { "Content-Type": "text/html; charset=utf-8" } }));
				     }, 500);
				   });
				 };
				 window.addEventListener("load", function () {
				   var driveBar = false, kitProgress = false, ticks = 0;
				   setTimeout(function () { document.getElementById("next").click(); }, 60);
				   (function watch() {
				     var bar = document.querySelector('[data-kitwork-ui="progress"]');
				     if (bar && bar.style.opacity === "1") driveBar = true;
				     if (window.kit.progress && window.kit.progress.active) kitProgress = true;
				     if (++ticks < 60) { setTimeout(watch, 20); return; }
				     document.documentElement.setAttribute("data-drive-bar", String(driveBar));
				     document.documentElement.setAttribute("data-kit-progress-seen", String(kitProgress));
				   })();
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
	match := navigationProgressRe.FindSubmatch(output)
	if match == nil {
		t.Fatalf("the page never reported:\n%.800s", output)
	}
	return string(match[1]), string(match[2])
}
