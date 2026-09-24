package hydrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// `data-kit-reload="true"` is the author saying "load this one the ordinary way". It is the only
// spelling: the old opt-out rode on the root marker (data-kit-hydrate="false"), which forced the
// author to type the engine's own word and gave one attribute two unrelated meanings.
//
// The proof is on the wire, because that is where the difference actually is. Drive fetches a page
// with X-Kitwork-Hydrate: 1 and swaps it in; the browser's own navigation carries no such header.
// So the server, not the DOM, says which link went which way. The page carries the engine's own
// runtime tag with its drive flag, which is exactly what router.drive() produces.
func TestBrowserReloadOptsOutOfDrive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the reload browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	page := func(title, body string) string {
		return `<!doctype html><html data-kit-hydrate="v1"><head><meta charset="utf-8"><title>` + title +
			`</title></head><body data-page="` + title + `">` + body +
			`<script data-kitwork-jit="runtime" data-kitwork-drive src="/kit.js"></script></body></html>`
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
		case RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(Runtime()))
		case "/":
			// Click the ordinary link; Drive should take it without leaving the page.
			_, _ = response.Write([]byte(page("start", `<a id="soft" href="/soft">soft</a>`+
				`<script>window.addEventListener("load", function () {
				   setTimeout(function () { document.getElementById("soft").click(); }, 60);
				 });</script>`)))
		case "/soft":
			// Drive morphed us here. Now click the opt-out link, which must be a real navigation.
			_, _ = response.Write([]byte(page("soft", `<a id="hard" href="/hard" data-kit-reload="true">hard</a>`+
				`<script>window.addEventListener("kitwork:load", function () {
				   setTimeout(function () { var a = document.getElementById("hard"); if (a) a.click(); }, 60);
				 });
				 setTimeout(function () { var a = document.getElementById("hard"); if (a) a.click(); }, 400);</script>`)))
		case "/hard":
			_, _ = response.Write([]byte(page("hard", `<b id="done">arrived</b>`)))
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
	var soft, hard *hit
	for i := range hits {
		switch hits[i].path {
		case "/soft":
			soft = &hits[i]
		case "/hard":
			hard = &hits[i]
		}
	}
	if soft == nil {
		t.Fatalf("the ordinary link was never followed; requests=%v\n%.500s", hits, output)
	}
	if soft.header != "1" {
		t.Fatalf("an ordinary link must still be driven (X-Kitwork-Hydrate=%q)", soft.header)
	}
	if hard == nil {
		t.Fatalf("the data-kit-reload link was never followed; requests=%v\n%.700s", hits, output)
	}
	if hard.header != "" {
		t.Fatalf(`data-kit-reload="true" must leave the link to the browser, but Drive fetched it (X-Kitwork-Hydrate=%q)`, hard.header)
	}
	// The final DOM is deliberately NOT asserted: --dump-dom does not wait for a real navigation to
	// finish, and the evidence that matters is the request itself, which the server already recorded.
	t.Logf("soft link X-Kitwork-Hydrate=%q, reload link X-Kitwork-Hydrate=%q", soft.header, hard.header)
}

func findHeadlessBrowserForDrive() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off", "no":
		return ""
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
