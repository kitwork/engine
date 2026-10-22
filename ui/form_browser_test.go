package ui

import (
	"context"
	"encoding/json"
	"io"
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

	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/ui/stylesheet"
)

// A page of .ui in a real browser, with the kernel and Drive on it as a site that moves without a
// reload has them (router.reload(false)):
//   - <form> (get) is Drive's: its fields go in the address, fetched with X-Kitwork-Hydrate and swapped
//     in, the page staying;
//   - <form method="post"> is the browser's: Drive steps aside (jit/hydrate/drive.js, the submit
//     listener) and the browser sends it, urlencoded, to the page's own address (the one on screen,
//     where Drive's move left it), as router.post there receives it;
//   - the page's stylesheets written inline (Page.Document with no head) carry the richtext's scope: its
//     link is kitwork-link and underlined, its list has markers, its quote a line on its left.
//
// CONTROL: with formWords writing no method, the post form was a get one, Drive fetched it, and this
// test failed (run 20/10/2026 on an overlaid copy of form.go).
func TestBrowserFormPostsToItsOwnPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the form browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	s := memorySite("", map[string]string{"/site/content.ui": `<content>
  <form id="search"><input name="q" value="cà phê" alt="Tìm" /><button type="submit">Tìm</button></form>
  <form id="note" method="post"><input name="note" value="Xin chào & cảm ơn" alt="Lời nhắn" /><button type="submit">Gửi</button></form>
  <richtext value="{{ post.body }}" />
</content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	// the cleaner keeps no id: the script finds each part by its place in the richtext
	const body = `<p>Đọc <a id="link" href="/menu">thực đơn</a>.</p><ul><li>Bạc xỉu</li></ul><blockquote>Ngon.</blockquote>`
	script := `<script>
	fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	window.addEventListener("load", function () {
	  if (location.search) return; // arrived by the browser itself: Drive did not take the get form
	  var style = function (selector, property) { return getComputedStyle(document.querySelector(selector)).getPropertyValue(property).trim(); };
	  var r = {
	    link: style(".richtext a", "color") + " " + style(".richtext a", "text-decoration-line") + " " + document.querySelectorAll("[id=link]").length,
	    linkColour: getComputedStyle(document.documentElement).getPropertyValue("--kitwork-link").trim(),
	    item: style(".richtext li", "display") + " " + style(".richtext li", "list-style-type"),
	    quote: style(".richtext blockquote", "border-left-style") + " " + style(".richtext blockquote", "background-color")
	  };
	  fetch("/report", { method: "POST", body: JSON.stringify(r) }).then(function () {
	    document.getElementById("search").requestSubmit();
	  });
	});
	document.addEventListener("kitwork:load", function () {
	  var form = document.getElementById("note");
	  // no timer: the held request keeps the browser's virtual clock from running one
	  if (form && !window.sent) { window.sent = true; form.requestSubmit(); }
	});
	</script>`
	tags := `<script src="/kernel.js" defer></script><script src="/drive.js" defer></script>`
	document := func(r *http.Request) string {
		html := page.Document(Request{Path: r.URL.Path, Origin: "http://" + r.Host, Scope: map[string]any{"post": map[string]any{"body": body}}})
		html = strings.Replace(html, "</head>", tags+"</head>", 1)
		return strings.Replace(html, "</body>", script+"</body>", 1)
	}

	type hit struct{ method, path, query, header, kind, body string }
	var mu sync.Mutex
	var hits []hit
	var report []byte
	posted := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kernel.js", "/drive.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			if request.URL.Path == "/drive.js" {
				_, _ = io.WriteString(response, hydrate.DriveFile())
			} else {
				_, _ = io.WriteString(response, hydrate.KernelFile(false))
			}
			return
		case "/hold":
			select {
			case <-posted:
			case <-time.After(20 * time.Second):
			}
			return
		case "/report":
			data, _ := io.ReadAll(request.Body)
			mu.Lock()
			report = data
			mu.Unlock()
			return
		case "/":
			data, _ := io.ReadAll(request.Body)
			mu.Lock()
			hits = append(hits, hit{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery,
				header: request.Header.Get("X-Kitwork-Hydrate"), kind: request.Header.Get("Content-Type"), body: string(data)})
			mu.Unlock()
			if request.Method == http.MethodPost {
				once.Do(func() { close(posted) })
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, document(request))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--window-size=1000,800",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--virtual-time-budget=15000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	var driven, sentPost *hit
	for i := range hits {
		switch h := &hits[i]; {
		case h.method == http.MethodGet && h.query != "":
			driven = h
		case h.method == http.MethodPost:
			sentPost = h
		}
	}
	if driven == nil || driven.header != "1" || driven.query != "q=c%C3%A0+ph%C3%AA" {
		t.Fatalf("the get form is Drive's, its fields in the address: %+v\n%.700s", hits, output)
	}
	if sentPost == nil {
		t.Fatalf("the post form was never sent: %+v\n%.700s", hits, output)
	}
	// the page's own address is the one on screen, where Drive's move left it
	if sentPost.header != "" || sentPost.path != "/" || sentPost.query != driven.query || sentPost.kind != "application/x-www-form-urlencoded" ||
		sentPost.body != "note=Xin+ch%C3%A0o+%26+c%E1%BA%A3m+%C6%A1n" {
		t.Errorf("the post form is the browser's, sent urlencoded to its own page: %+v", *sentPost)
	}
	got := map[string]string{}
	if err := json.Unmarshal(report, &got); err != nil {
		t.Fatalf("report %q: %v", report, err)
	}
	palette, _ := stylesheet.NewPalette(false)
	link, _ := palette.Paint(stylesheet.LinkColour, stylesheet.LightMode)
	for name, want := range map[string]string{
		"link":       hexRGB(link.Hex()) + " underline 0", // and the id written in the value is gone
		"linkColour": link.Hex(),
		"item":       "list-item disc",
		"quote":      "solid rgba(0, 0, 0, 0)",
	} {
		if got[name] != want {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], want)
		}
	}
}

// hexRGB is #rrggbb as a browser serializes a computed colour: rgb(r, g, b).
func hexRGB(hex string) string {
	var n [3]int
	for i := range n {
		for _, c := range hex[1+2*i : 3+2*i] {
			n[i] = n[i]*16 + strings.IndexRune("0123456789abcdef", c)
		}
	}
	return "rgb(" + itoa(n[0]) + ", " + itoa(n[1]) + ", " + itoa(n[2]) + ")"
}

func findHeadlessBrowser() string {
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
