package hydrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// <box component="progressbar" delay="…" /> says how long a move may take before it counts as slow and the bar shows;
// it reaches Drive as data-kitwork-delay on the progressbar host. Each case is paired with a CONTROL:
// the same move with Drive's own 120ms, which must answer the other way, so the test fails if Drive
// ignored the attribute. Real Chrome; the next page is answered late by a fetch on the virtual clock.
func TestBrowserDriveProgressDelay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the progress delay browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	host := func(delay string) string {
		if delay == "" {
			return `<div data-kit-component="progressbar"></div>`
		}
		return `<div data-kit-component="progressbar" data-kitwork-delay="` + delay + `"></div>`
	}
	cases := []struct {
		name    string
		host    string
		answer  int // ms until the next page arrives; -1: it is already there
		showsUp string
	}{
		{"delay 0 shows a fast move", host("0"), 40, "true"},
		{"CONTROL: the default does not show the same move", host(""), 40, "false"},
		{"delay 300 waits through a 200ms move", host("300"), 200, "false"},
		{"CONTROL: the default shows the same move", host(""), 200, "true"},
		// a page fetched ahead (the pointer rested on the link) is already there at the click
		{"delay 0 shows a move to a page fetched ahead", host("0"), -1, "true"},
		{"CONTROL: the default does not show a move to a page fetched ahead", host(""), -1, "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := watchProgressAfter(t, browser, c.host, c.answer); got != c.showsUp {
				t.Fatalf("kit.progress seen=%s, want %s", got, c.showsUp)
			}
		})
	}
}

var progressSeenRe = regexp.MustCompile(`data-progress-seen="(true|false)"`)

func watchProgressAfter(t *testing.T, browser, host string, answer int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>start</title></head>` +
				`<body>` + host + `<main><a id="next" href="/next">next</a></main>` +
				`<script>var answer = ` + strconv.Itoa(answer) + `;
				 window.fetch = function () {
				   var page = "<!doctype html><html><head><title>next</title></head><body><main>next</main></body></html>";
				   if (answer < 0) return Promise.resolve(new Response(page, { headers: { "Content-Type": "text/html; charset=utf-8" } }));
				   return new Promise(function (resolve) {
				     setTimeout(function () {
				       resolve(new Response("<!doctype html><html><head><title>next</title></head><body><main>next</main></body></html>",
				         { headers: { "Content-Type": "text/html; charset=utf-8" } }));
				     }, ` + strconv.Itoa(answer) + `);
				   });
				 };
				 window.addEventListener("load", function () {
				   var seen = false, ticks = 0;
				   var link = document.getElementById("next");
				   if (answer < 0) {
				     // rest the pointer on the link: Drive fetches the page ahead, then the click finds it there
				     setTimeout(function () { link.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); }, 20);
				     setTimeout(function () { link.click(); }, 300);
				   } else {
				     setTimeout(function () { link.click(); }, 60);
				   }
				   (function watch() {
				     if (window.kit.progress && window.kit.progress.active) seen = true;
				     if (++ticks < 90) { setTimeout(watch, 10); return; }
				     document.documentElement.setAttribute("data-progress-seen", String(seen));
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
	match := progressSeenRe.FindSubmatch(output)
	if match == nil {
		t.Fatalf("the page never reported:\n%.800s", output)
	}
	return string(match[1])
}
