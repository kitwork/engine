package js

import (
	"bytes"
	"context"
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

// A page loads the kernel's file, then one file per component, all deferred. boot.js is what starts
// the kernel, and the kernel's first pass is a real paint — so whatever has not registered by then
// misses it. Boot waits for DOMContentLoaded, which every deferred file precedes.
// A component that registers afterwards leaves its boundary without a scope for that pass, and a
// name nothing declares reads as 0. The visible cost is not a warning: `data-kit-text` paints "0"
// for a frame, and `data-kit-bind:src="photo"` makes the browser FETCH a url called "0" — a real
// request, a real 404, on every page load of every site that uses a component.
//
// This test watches the server, not the DOM, because the request is the part a person would see in
// a log and never be able to explain. The second half starts the kernel by hand between its file and
// the component's, so the proof cannot pass by there being nothing to catch.
func TestBrowserComponentsRegisterBeforeTheFirstPaint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping boot-order browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	const module = `kit.component("probe-photo", { photo: "/ok.png" });`
	const page = `<!doctype html>
<html lang="en" data-kit-app="boot-order"><head><meta charset="utf-8"><title>boot order</title></head><body>
  <div data-kit-component="probe-photo">
    <img id="photo" data-kit-bind:src="photo" alt="">
    <b id="label" data-kit-text="photo"></b>
  </div>
  <script>
  window.addEventListener("load", function () {
    var root = document.documentElement;
    var photo = document.getElementById("photo");
    root.setAttribute("data-kit-test", photo.getAttribute("src") === "/ok.png" ? "passed" : "failed");
    root.setAttribute("data-kit-test-src", String(photo.getAttribute("src")));
  });
  </script>
</body></html>`

	site := fakeSite{"probe-photo": module}
	run := func(t *testing.T, startEarly bool) []string {
		t.Helper()
		delivered := deliverTestPage(t, page, nil, site)
		if !strings.Contains(delivered.html, "/_kitwork/components/probe-photo.") {
			t.Fatalf("the page should get the component as a file of its own:\n%s", delivered.html)
		}
		if startEarly {
			// Between the kernel's file and the component's: what the old composition did when it put
			// boot before the modules.
			at := strings.LastIndex(delivered.html[:strings.Index(delivered.html, `src="/_kitwork/components/probe-photo.`)], "<script")
			delivered.html = delivered.html[:at] + `<script src="/test-scripts/start.js" defer></script>` + delivered.html[at:]
			delivered.files["/test-scripts/start.js"] = "window.kit.start();"
		}
		var mu sync.Mutex
		var seen []string
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			mu.Lock()
			seen = append(seen, request.URL.Path)
			mu.Unlock()
			if delivered.serveFiles(response, request) {
				return
			}
			switch request.URL.Path {
			case "/":
				response.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = response.Write([]byte(delivered.html))
			case "/ok.png":
				response.Header().Set("Content-Type", "image/gif")
				_, _ = response.Write([]byte("GIF89a"))
			default:
				http.NotFound(response, request)
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		profile := t.TempDir()
		args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
			"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
			"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
		if runtime.GOOS == "windows" {
			args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
		}
		output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
		if !bytes.Contains(output, []byte(`data-kit-test=`)) {
			t.Fatalf("the page never reported (browser did not run it):\n%.800s", output)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string{string(output)}, seen...)
	}

	t.Run("modules before boot", func(t *testing.T) {
		result := run(t, false)
		dom, requests := result[0], result[1:]
		for _, path := range requests {
			if path == "/0" {
				t.Fatalf("the browser fetched %q: a binding was evaluated before its component registered", path)
			}
		}
		if !strings.Contains(dom, `data-kit-test="passed"`) {
			t.Fatalf("the bound src never reached its value; requests=%v\n%.400s", requests, dom)
		}
	})

	// The control: a kernel started BEFORE its component's file, which is what the old boot did on a
	// deferred script. It must show the request, or the test above proves nothing.
	t.Run("control: kernel started before modules", func(t *testing.T) {
		result := run(t, true)
		requests := result[1:]
		for _, path := range requests {
			if path == "/0" {
				return
			}
		}
		t.Fatalf("expected the old order to fetch %q, got requests=%v", "/0", requests)
	})
}
