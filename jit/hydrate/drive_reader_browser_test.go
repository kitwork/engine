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

// readerHostForTest is the host ui.ReaderMarkup writes (this package cannot import ui: ui imports it).
const readerHostForTest = `<div data-kitwork-component="reader" data-kitwork-bind="text: text" aria-live="polite" aria-atomic="true"` +
	` style="position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap"></div>`

func readerComponentFile(t *testing.T) []byte {
	source, err := os.ReadFile(filepath.Join("..", "js", "components", "reader", "v1.0.0.js"))
	if err != nil {
		t.Fatalf("the reader component: %v", err)
	}
	return []byte(";(function () {\n" + string(source) + "\n})();")
}

var spokenAttribute = regexp.MustCompile(`data-spoken="([^"]*)"`)

// A move without a reload does not tell a screen reader that the page changed. Drive keeps no live
// region of its own (11/10/2026): it says the new title through kit.reader, and the reader component —
// the invisible live region the engine puts on every page that moves this way — speaks it. The walk
// is / → /b → /c → /, where /b and /c share a title: each arrival is said, the second of two equal
// titles too (the text is emptied first, so the live region sees a change). Drive must not make a
// region of its own any more.
func TestBrowserDriveSaysTheNewTitleThroughTheReader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive reader browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := func(title, next string) string {
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title></head><body>` + readerHostForTest +
			`<main><a id="next" href="` + next + `">next</a></main>` +
			`<script>
			  (function () {
			    if (window.__walk) return;
			    window.__walk = true;
			    var spoken = [], moves = 0;
			    var host = function () { return document.querySelector('[data-kitwork-component="reader"]'); };
			    new MutationObserver(function () {
			      var text = host() ? host().textContent : "?";
			      if (text) spoken.push(text);
			      document.documentElement.setAttribute("data-spoken", spoken.join(",") + "|regions=" +
			        document.querySelectorAll('[aria-live]').length + "|generated=" +
			        document.querySelectorAll('[data-kitwork-generated="screen-reader"],[data-kitwork-generated="reader"]').length);
			    }).observe(host(), { subtree: true, childList: true, characterData: true });
			    // the page repaints all the time (a progress bar ticking does): the same words must not be
			    // written again, or the live region would read them out again
			    setInterval(function () { if (window.kit && window.kit.render) window.kit.render(); }, 40);
			    function go() { if (moves++ < 3) setTimeout(function () { document.getElementById("next").click(); }, 60); }
			    window.addEventListener("load", go);
			    document.addEventListener("kitwork:load", function () { setTimeout(go, 200); });
			  })();
			</script>` + pageRuntimeTags + `<script src="/kit/reader.js" defer></script></body></html>`
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/kit/reader.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(readerComponentFile(t))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page("Thực đơn", "/b")))
		case "/b":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page("Về chúng tôi", "/c")))
		case "/c":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page("Về chúng tôi", "/")))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	match := spokenAttribute.FindSubmatch(output)
	if match == nil {
		t.Fatalf("the reader never spoke:\n%.1200s", output)
	}
	if got, want := string(match[1]), "Về chúng tôi,Về chúng tôi,Thực đơn|regions=1|generated=0"; got != want {
		t.Fatalf("spoken %q, want %q (each arrival said once, one live region, none made by Drive)", got, want)
	}
}
