package hydrate

import (
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

	jittheme "github.com/kitwork/engine/jit/theme"
)

// router.appearance reaches the browser as <html data-kitwork-appearance> on the page arriving — the
// mode a page shows before the visitor chooses, and a branch of a site may say another than its root.
// A move without a reload must look like a whole load of the page arriving: the attribute is carried
// over, and with no choice stored the page shows the new default; a stored choice (light, or system)
// still beats it. The binding of a component reading kit.appearance.dark shows the same thing after
// the move, so the mode is applied before the kernel renders.
//
// One controller script outside <main> (Drive swaps only <main>, so it lives through every move) walks
// the steps, records the state after each move, and reports the log to the server. The operating
// system is a light one throughout the runtime (matchMedia is replaced before the kernel starts), so
// "system" shows light and the default is what makes a page dark. Control: a move between two pages
// that never speak of appearance repaints nothing, even with the device turned dark in between.
func TestBrowserDriveCarriesTheSiteAppearanceDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the appearance browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	links := `<a id="home" href="/">home</a><a id="dark" href="/dark">dark</a><a id="other" href="/other">other</a>`
	probe := `<p data-kitwork-component="probe"><b id="shown" data-kitwork-bind="text: dark ? 'dark' : 'light'"></b></p>`
	page := func(title, appearance, extra string, prepaint bool) string {
		html := `<html>`
		if appearance != "" {
			html = `<html data-kitwork-appearance="` + appearance + `">`
		}
		out := `<!doctype html>` + html + `<head><meta charset="utf-8"><title>` + title + `</title></head><body>` +
			probe + `<main><h1>` + title + `</h1>` + links + `</main>` + extra + pageRuntimeTags +
			`<script src="/probe.js" defer></script></body></html>`
		if prepaint {
			out = jittheme.Force(out) // the engine's pre-paint, as a page of a site with dark gets it
		}
		return out
	}
	controller := `<script>
	  var root = document.documentElement, osDark = false, log = [];
	  window.matchMedia = function (query) {
	    return { matches: osDark && query === "(prefers-color-scheme: dark)", media: query, addListener: function () {}, addEventListener: function () {} };
	  };
	  function state() {
	    var shown = document.getElementById("shown").textContent;
	    var stored = null; try { stored = localStorage.getItem("appearance"); } catch (e) {}
	    return location.pathname + "=" + (root.classList.contains("dark") ? "dark" : "light") + "/" + root.getAttribute("data-theme") + "/" +
	      (root.getAttribute("data-kitwork-appearance") || "-") + "/" + shown + "/" + (stored || "-");
	  }
	  function go(id) { document.getElementById(id).click(); }
	  var steps = [
	    function () { try { localStorage.clear(); } catch (e) {} go("dark"); },
	    function () { log.push(state()); go("home"); },
	    function () { log.push(state()); window.kit.appearance.set("light"); go("dark"); },
	    function () { log.push(state()); window.kit.appearance.system(); go("home"); },
	    function () { log.push(state()); go("dark"); },
	    function () { log.push(state()); localStorage.clear(); go("home"); },
	    function () { log.push(state()); go("dark"); },
	    function () { log.push(state()); go("home"); },
	    function () { log.push(state()); osDark = true; root.removeAttribute("data-theme"); go("other"); },
	    function () {
	      log.push(state());
	      fetch("/report?log=" + encodeURIComponent(log.join(",")) + "&same=" + (window.kit.theme === window.kit.appearance ? "1" : "0"));
	    }
	  ];
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
		case "/probe.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(`window.kit.component("probe", { get dark() { return window.kit.appearance.dark; } });`))
		case "/":
			if request.Header.Get("X-Kitwork-Hydrate") == "" {
				_, _ = response.Write([]byte(page("home", "", controller, true)))
			} else {
				_, _ = response.Write([]byte(page("home", "", "", true)))
			}
		case "/dark":
			_, _ = response.Write([]byte(page("dark", "dark", "", true)))
		case "/other":
			_, _ = response.Write([]byte(page("other", "", "", false)))
		case "/hold":
			select {
			case <-reported:
			case <-time.After(12 * time.Second):
			}
		case "/report":
			q := request.URL.Query()
			mu.Lock()
			report = map[string]string{"log": q.Get("log"), "same": q.Get("same")}
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == nil {
		t.Fatalf("the controller never reported; requests=%v\n%.600s", hits, output)
	}
	// path = class / data-theme / data-kitwork-appearance / the probe's binding / stored choice
	want := []string{
		"/dark=dark/dark/dark/dark/-",         // nothing stored: the branch's dark default
		"/=light/light/-/light/-",             // back at the root (system, a light device)
		"/dark=light/light/dark/light/light",  // a stored light beats the dark default
		"/=light/light/-/light/system",        // system() stores "system"…
		"/dark=light/light/dark/light/system", // …and it beats the dark default too: the device decides
		"/=light/light/-/light/-",             // the store emptied
		"/dark=dark/dark/dark/dark/-",         // the default is back
		"/=light/light/-/light/-",
		"/other=light/null/-/light/-", // control: neither page speaks of appearance — nothing repainted
	}
	if got := strings.Split(report["log"], ","); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("states after each move:\n got  %s\n want %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
	if report["same"] != "1" {
		t.Error("kit.theme must be the same object as kit.appearance")
	}
	for _, h := range hits {
		if h == "/dark " || h == "/other " {
			t.Errorf("a page was loaded whole instead of moved to (%s); requests=%v", h, hits)
		}
	}
}
