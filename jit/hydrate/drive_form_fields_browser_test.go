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

// A form's fields do not change where Drive sends it. A form element gives its named fields as its own
// properties, so a field named action is form.action and one named method is form.method; Drive reads
// the form through HTMLFormElement's own getters, which no field shadows. A get form with a field named
// action is fetched at the page's own address with its fields in the query, as any get form is, and so
// is one with a field named method; both are Drive's (X-Kitwork-Hydrate: 1), neither goes elsewhere.
//
// CONTROL: with the submit listener reading f.action and f.method (drive.js as it was), the form with
// a field named action was fetched at "/[object HTMLInputElement]", and the one with a field named
// method threw in the listener and was loaded whole by the browser (no X-Kitwork-Hydrate); this test
// failed (run 20/10/2026 on an overlaid copy of drive.js).
func TestBrowserDriveFormFieldsNamedActionOrMethod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive form browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	// each page reached sends the next form: the first by its field named action, then by its field
	// named method, then says it is done
	const page = `<!doctype html><html><head><meta charset="utf-8"><title>forms</title><script>
  fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
  function next() {
    var search = location.search;
    if (search === "" && !window.sentAction) { window.sentAction = true; document.getElementById("a").requestSubmit(); }
    else if (search.indexOf("action=delete") >= 0 && !window.sentMethod) { window.sentMethod = true; document.getElementById("b").requestSubmit(); }
    else if (search.indexOf("method=delete") >= 0) { fetch("/done"); }
  }
  window.addEventListener("load", next);
  document.addEventListener("kitwork:load", next);
</script>` + pageRuntimeTags + `</head><body><main>
  <form id="a"><input name="q" value="x"><input name="action" value="delete"><button type="submit">Go</button></form>
  <form id="b"><input name="q" value="y"><input name="method" value="delete"><button type="submit">Go</button></form>
</main></body></html>`

	type hit struct{ path, query, header string }
	var mu sync.Mutex
	var hits []hit
	done := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
			return
		case "/hold":
			select {
			case <-done:
			case <-time.After(15 * time.Second):
			}
			return
		case "/done":
			once.Do(func() { close(done) })
			return
		case "/favicon.ico":
			http.NotFound(response, request)
			return
		}
		mu.Lock()
		hits = append(hits, hit{path: request.URL.Path, query: request.URL.RawQuery, header: request.Header.Get("X-Kitwork-Hydrate")})
		mu.Unlock()
		// every address answers the page, so a form sent to the wrong one still shows where it went
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(page))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	want := []hit{{"/", "", ""}, {"/", "q=x&action=delete", "1"}, {"/", "q=y&method=delete", "1"}}
	if len(hits) != len(want) {
		t.Fatalf("requests %+v, want %+v\n%.500s", hits, want, output)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Errorf("request %d: %+v, want %+v (all: %+v)", i, hits[i], want[i], hits)
		}
	}
}
