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
)

// A move without a reload shows what a whole load of the next page would: its tab icon (a Kitwork UI
// branch may name its own with router.favicon), and, when the next page is built from another
// page.ui, the whole page rather than the old frame around its content.
//
// /  (page.ui, icon a)  →  /b  (page.ui, icon b): driven; the head links b, not a.
// /b                    →  /c  (page.ui, no icon): driven; b stays, a shell that names no icon keeps it.
// /c                    →  /admin (admin/page.ui): another frame; the browser loads it whole.
//
// The wire tells driven from whole loads, as in TestBrowserDriveSwapsNotFoundPage: Drive fetches with
// X-Kitwork-Hydrate: 1, the browser's own navigation carries no such header.
func TestBrowserDriveIconAndLayout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the icon and layout Drive browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	page := func(title, icon, layout, body string) string {
		head := `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>`
		if icon != "" {
			head += `<link rel="icon" href="` + icon + `" type="image/svg+xml">`
		}
		return head + `</head><body data-kitwork-layout="` + layout + `"><header>` + layout + `</header><main>` + body +
			`</main>` + pageRuntimeTags + `</body></html>`
	}
	follow := func(id string) string {
		return `<a id="` + id + `" href="/` + id + `">` + id + `</a><script>
		  (function () {
		    function go() { var a = document.getElementById("` + id + `"); if (a && !a.dataset.went) { a.dataset.went = 1; a.click(); } }
		    window.addEventListener("load", function () { setTimeout(go, 60); });
		    window.addEventListener("kitwork:load", function () { setTimeout(go, 60); });
		    setTimeout(go, 400);
		  })();</script>`
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
			_, _ = response.Write([]byte(page("start", "/a.svg", "page.ui", follow("b"))))
		case "/b":
			_, _ = response.Write([]byte(page("b", "/b.svg", "page.ui", follow("c"))))
		case "/c":
			_, _ = response.Write([]byte(page("c", "", "page.ui", follow("admin"))))
		case "/admin":
			_, _ = response.Write([]byte(page("admin", "/admin.svg", "admin/page.ui", `<p>quản lý</p>`)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	// The move to another frame is a whole load, after which the browser runs on to the timeout: the
	// proof is on the wire, as in TestBrowserDriveSwapsNotFoundPage.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	count := func(path string) (driven, whole int) {
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
	for _, path := range []string{"/b", "/c"} {
		if driven, whole := count(path); driven == 0 || whole != 0 {
			t.Fatalf("%s is the same page.ui: it must be driven, not loaded whole (%d driven, %d whole); requests=%v", path, driven, whole, hits)
		}
	}
	if _, whole := count("/admin"); whole == 0 {
		t.Fatalf("/admin is built from another page.ui: the browser must load it whole; requests=%v\n%.800s", hits, output)
	}
}

// The icon half is read off the page at /c, before the move to another frame replaces the document.
func TestBrowserDriveSwapsTheTabIcon(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the tab icon Drive browser proof in short mode")
	}
	browser := findHeadlessBrowserForDrive()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := func(title, icon, body string) string {
		head := `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>`
		if icon != "" {
			head += `<link rel="icon" href="` + icon + `" type="image/svg+xml">`
		}
		return head + `</head><body data-kitwork-layout="page.ui"><main>` + body + `</main>` + pageRuntimeTags + `</body></html>`
	}
	follow := func(id string) string {
		return `<a id="` + id + `" href="/` + id + `">` + id + `</a><script>
		  (function () {
		    function go() { var a = document.getElementById("` + id + `"); if (a && !a.dataset.went) { a.dataset.went = 1; a.click(); } }
		    window.addEventListener("load", function () { setTimeout(go, 60); });
		    window.addEventListener("kitwork:load", function () { setTimeout(go, 60); });
		    setTimeout(go, 400);
		  })();</script>`
	}
	var mu sync.Mutex
	var driven []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Kitwork-Hydrate") == "1" {
			mu.Lock()
			driven = append(driven, request.URL.Path)
			mu.Unlock()
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch request.URL.Path {
		case "/kit/kernel.js", "/kit/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(pageRuntimeFile(request.URL.Path)))
		case "/":
			_, _ = response.Write([]byte(page("start", "/a.svg", follow("b"))))
		case "/b":
			_, _ = response.Write([]byte(page("b", "/b.svg", follow("c"))))
		case "/c":
			_, _ = response.Write([]byte(page("c", "", `<p id="at-c">c</p>`)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	dom := string(output)

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(driven, ",") != "/b,/c" || !strings.Contains(dom, `id="at-c"`) {
		t.Fatalf("want two driven moves ending at /c, got %v:\n%.1200s", driven, dom)
	}
	head := dom[:strings.Index(dom, "</head>")]
	if !strings.Contains(head, `href="/b.svg"`) {
		t.Errorf("the head does not link the icon of the page that named one last (/b.svg):\n%s", head)
	}
	if strings.Contains(head, `href="/a.svg"`) {
		t.Errorf("the first page's icon is still linked after moving on:\n%s", head)
	}
}
