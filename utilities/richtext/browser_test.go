package richtext

import (
	"context"
	"encoding/json"
	"html"
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
)

// A browser reads the value the way Clean wrote it, for every corpus and table case: the HTML of Clean
// and of Display, set as the innerHTML of a div, serializes back byte for byte (nothing was reparented,
// re-escaped or dropped: a code block's white space, a newline at its start among it, the alignment
// classes, the rules, the five marks in their nesting), and every link's href is the address the link
// rule checked, going where it says: http:, https:, mailto: or tel:, and a path to the page's own host.
//
// CONTROL: with a backslash no longer read as a slash (NormalizeAddress's '\\' case removed),
// "/\evil.example" was written as a path and the browser took it to the host evil.example; this test
// failed.
//
// CONTROL: with a code block keeping U+00A0 (text() handing it its text before normalizeText), the
// browser wrote the code block of TestCodeBlocks back with &nbsp; and this test failed on it.
func TestBrowserCleanHoldsInABrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the browser round trip in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	type written struct {
		HTML    string   `json:"html"`
		Display string   `json:"display"`
		Hrefs   []string `json:"-"`
	}
	sources := append([]string(nil), tableSources...)
	for _, c := range corpus(t) {
		sources = append(sources, c.source)
	}
	for _, c := range keptLinks {
		sources = append(sources, `<a href="`+c.address+`">x</a>`)
	}
	for _, address := range refusedLinks {
		sources = append(sources, `<a href="`+address+`">x</a>`)
	}
	for _, address := range []string{"/a'b`c{d}|e^f", "/%2F%2Fevil.example", "/.//evil.example", "https://ví-dụ.vn/đường",
		"/a\u00a0b", "/?q=<b>&x=\"y\"", "mailto:a@b.c?subject=xin%20ch%C3%A0o", "tel:+84 90 123 4567"} {
		sources = append(sources, `<a href="`+html.EscapeString(address)+`">x</a>`)
	}
	var cases []written
	for _, source := range sources {
		c := written{HTML: Clean(source).HTML, Display: Display(source, "")}
		for rest := c.HTML; ; {
			i := strings.Index(rest, `href="`)
			if i < 0 {
				break
			}
			rest = rest[i+6:]
			c.Hrefs = append(c.Hrefs, html.UnescapeString(rest[:strings.IndexByte(rest, '"')]))
		}
		cases = append(cases, c)
	}
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><html><head><meta charset="utf-8"></head><body><script>
(async () => {
  fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
  const cases = await (await fetch("/cases")).json();
  const out = [];
  for (const c of cases) {
    const seen = {};
    for (const key of ["html", "display"]) {
      const div = document.createElement("div");
      div.innerHTML = c[key];
      seen[key] = div.innerHTML;
      if (key === "html") {
        seen.links = [...div.querySelectorAll("a")].map((a) => {
          const href = a.getAttribute("href");
          let protocol = "unparsed", host = "";
          try { const url = new URL(href, location.href); protocol = url.protocol; host = url.host; } catch (e) {}
          return { href, protocol, own: host === location.host };
        });
      }
    }
    out.push(seen);
  }
  await fetch("/report", { method: "POST", body: JSON.stringify(out) });
})();
</script></body></html>`

	var mu sync.Mutex
	var once sync.Once
	var report []byte
	reported := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, page)
		case "/cases":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(payload)
		case "/hold":
			select {
			case <-reported:
			case <-time.After(30 * time.Second):
			}
		case "/report":
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			report = body
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--window-size=800,600",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--virtual-time-budget=20000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == nil {
		t.Fatalf("the page never reported\n%.700s", output)
	}
	var seen []struct {
		HTML    string `json:"html"`
		Display string `json:"display"`
		Links   []struct {
			Href     string `json:"href"`
			Protocol string `json:"protocol"`
			Own      bool   `json:"own"`
		} `json:"links"`
	}
	if err := json.Unmarshal(report, &seen); err != nil {
		t.Fatalf("report %.300s: %v", report, err)
	}
	if len(seen) != len(cases) {
		t.Fatalf("the browser read %d cases, want %d", len(seen), len(cases))
	}
	links := 0
	for i, c := range cases {
		got := seen[i]
		if got.HTML != c.HTML {
			t.Errorf("Clean(%q): the browser writes back\n %q\nnot\n %q", sources[i], got.HTML, c.HTML)
		}
		if got.Display != c.Display {
			t.Errorf("Display(%q): the browser writes back\n %q\nnot\n %q", sources[i], got.Display, c.Display)
		}
		if len(got.Links) != len(c.Hrefs) {
			t.Errorf("Clean(%q): the browser sees %d links, the output has %d", sources[i], len(got.Links), len(c.Hrefs))
			continue
		}
		for k, link := range got.Links {
			links++
			want := c.Hrefs[k]
			switch {
			case link.Href != want:
				t.Errorf("Clean(%q): the browser reads href %q, the link rule checked %q", sources[i], link.Href, want)
			case strings.HasPrefix(want, "/") && (link.Protocol != "http:" || !link.Own):
				t.Errorf("Clean(%q): the path %q goes to %s on another host", sources[i], want, link.Protocol)
			case !strings.HasPrefix(want, "/") && link.Protocol != "http:" && link.Protocol != "https:" &&
				link.Protocol != "mailto:" && link.Protocol != "tel:" && link.Protocol != "unparsed":
				t.Errorf("Clean(%q): the link %q goes to %s", sources[i], want, link.Protocol)
			}
		}
	}
	if links < 50 {
		t.Fatalf("only %d links were read in the browser: the cases no longer exercise the link rule", links)
	}
	t.Logf("%d cases and %d links read back in %s", len(cases), links, filepath.Base(browser))
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
