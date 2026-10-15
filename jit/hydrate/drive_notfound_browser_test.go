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

// A link to a page that does not exist still moves without a reload: the not-found page is HTML in
// the same frame (a Kitwork UI site's notfound.ui inside page.ui), so Drive swaps it in like any
// other page. Drive used to refuse every status that was not 2xx and load the URL whole, so a
// navbar link to a missing page dropped everything the swap keeps.
//
// The proof is on the wire, as in TestBrowserReloadOptsOutOfDrive: Drive fetches with
// X-Kitwork-Hydrate: 1, the browser's own navigation carries no such header. A driven 404 is one
// request with the header and none without it. Control: a 500 must still go to the browser, so the
// test fails if Drive simply took every status.
func TestBrowserDriveSwapsNotFoundPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the not-found Drive browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	page := func(title, body string) string {
		return `<!doctype html><html><head><meta charset="utf-8"><title>` + title +
			`</title></head><body><main>` + body + `</main>` + pageRuntimeTags + `</body></html>`
	}

	var mu sync.Mutex
	type hit struct{ path, header string }
	var hits []hit
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		hits = append(hits, hit{path: request.URL.Path, header: request.Header.Get("X-Kitwork-Hydrate")})
		mu.Unlock()
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			_, _ = response.Write([]byte(page("start", `<a id="missing" href="/missing">missing</a>`+
				`<script>window.addEventListener("load", function () {
				   setTimeout(function () { document.getElementById("missing").click(); }, 60);
				 });</script>`)))
		case "/missing":
			// The site's not-found page; from here, follow a link to a page that fails on the server.
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(page("not found", `<a id="broken" href="/broken">broken</a>`+
				`<script>window.addEventListener("kitwork:load", function () {
				   setTimeout(function () { var a = document.getElementById("broken"); if (a) a.click(); }, 60);
				 });
				 setTimeout(function () { var a = document.getElementById("broken"); if (a) a.click(); }, 400);</script>`)))
		case "/broken":
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(page("error", `<b>failed</b>`)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=5000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	headers := func(path string) (driven, whole int) {
		for _, h := range hits {
			if h.path != path {
				continue
			}
			if h.header == "1" {
				driven++
			} else {
				whole++
			}
		}
		return
	}
	driven, whole := headers("/missing")
	if driven == 0 {
		t.Fatalf("the link to the missing page was never driven; requests=%v\n%.500s", hits, output)
	}
	if whole != 0 {
		t.Fatalf("a 404 page was loaded whole after Drive fetched it (%d driven, %d whole): the swap must take it", driven, whole)
	}
	// Control: the page reached through the 404's own link must be reachable, and a server error is
	// still the browser's to load — it is not a page in the site's frame.
	brokenDriven, brokenWhole := headers("/broken")
	if brokenDriven == 0 {
		t.Fatalf("the 404 page's own link was never followed, so the swapped page is not live; requests=%v", hits)
	}
	if brokenWhole == 0 {
		t.Fatalf("a 500 must fall back to a whole load, but the browser never requested /broken itself; requests=%v", hits)
	}
	t.Logf("/missing: %d driven, %d whole; /broken: %d driven, %d whole", driven, whole, brokenDriven, brokenWhole)
}
