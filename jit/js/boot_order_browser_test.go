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

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// /kit.js is ONE file: the kernel, then the page's components. boot.js is what starts the kernel,
// and the kernel's first pass is a real paint — so whatever has not registered by then misses it.
// A component that registers afterwards leaves its boundary without a scope for that pass, and a
// name nothing declares reads as 0. The visible cost is not a warning: `data-kit-text` paints "0"
// for a frame, and `data-kit-bind:src="photo"` makes the browser FETCH a url called "0" — a real
// request, a real 404, on every page load of every site that uses a component.
//
// This test watches the server, not the DOM, because the request is the part a person would see in
// a log and never be able to explain. The second half runs the same page with the old composition
// (boot first, modules after) so the proof cannot pass by there being nothing to catch.
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
  <script src="/kit.js" defer></script>
  <script>
  window.addEventListener("load", function () {
    var root = document.documentElement;
    var photo = document.getElementById("photo");
    root.setAttribute("data-kit-test", photo.getAttribute("src") === "/ok.png" ? "passed" : "failed");
    root.setAttribute("data-kit-test-src", String(photo.getAttribute("src")));
  });
  </script>
</body></html>`

	run := func(t *testing.T, runtimeJS string) []string {
		t.Helper()
		var mu sync.Mutex
		var seen []string
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			mu.Lock()
			seen = append(seen, request.URL.Path)
			mu.Unlock()
			switch request.URL.Path {
			case hydrate.RuntimePath:
				response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				_, _ = response.Write([]byte(runtimeJS))
			case "/":
				response.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = response.Write([]byte(page))
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
		result := run(t, hydrate.RuntimeFor(hydrate.RuntimeParts{Modules: module, Drive: true, Native: true}))
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

	// The control: the composition this fix replaced. It must show the request, or the test above
	// proves nothing.
	t.Run("control: boot before modules", func(t *testing.T) {
		result := run(t, hydrate.Runtime()+"\n"+module)
		requests := result[1:]
		for _, path := range requests {
			if path == "/0" {
				return
			}
		}
		t.Fatalf("expected the old order to fetch %q, got requests=%v", "/0", requests)
	})
}

// The same rule, without a browser: whatever a page's modules are, they are in the file before the
// line that starts the kernel.
func TestRuntimeWithPutsModulesBeforeBoot(t *testing.T) {
	const module = `kit.component("probe-order", {});`
	source := hydrate.RuntimeFor(hydrate.RuntimeParts{Modules: module, Drive: true, Native: true})
	moduleAt := strings.Index(source, module)
	startAt := strings.LastIndex(source, "kit.start()")
	if moduleAt < 0 || startAt < 0 {
		t.Fatalf("composition lost a part: module=%d start=%d", moduleAt, startAt)
	}
	if moduleAt > startAt {
		t.Fatalf("the module is served AFTER the kernel starts (module=%d start=%d)", moduleAt, startAt)
	}
	// Control: the plain runtime still ends with boot, so the index above means what it says.
	if plain := hydrate.Runtime(); strings.Index(plain, "kit.start()") < len(plain)-400 {
		t.Fatalf("boot is no longer last in the plain runtime — this test's landmark moved")
	}
	// An empty module list must not change the runtime at all.
	if hydrate.RuntimeFor(hydrate.RuntimeParts{Drive: true, Native: true}) != hydrate.Runtime() || hydrate.RuntimeFor(hydrate.RuntimeParts{Modules: "   ", Drive: true, Native: true}) != hydrate.Runtime() {
		t.Fatal("RuntimeWith with no modules should be exactly the runtime")
	}
}
