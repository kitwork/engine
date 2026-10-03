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
	"testing"
	"time"
)

// proofScriptHead is what every browser proof starts with: an assert that records its reason on
// <html>, a timer-based wait (not requestAnimationFrame — under --virtual-time-budget the clock runs
// ahead of the compositor and a frame callback may never arrive), and key/focus helpers. A
// background headless page never has window focus, so element.focus() moves activeElement without
// firing focusin; focusIn() does both, the way a real Tab would.
const proofScriptHead = `
var root = document.documentElement;
function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
function assert(condition, message) { if (!condition) fail(message); }
function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }
function key(target, name, extra) {
  var init = { key: name, bubbles: true, cancelable: true };
  for (var k in (extra || {})) init[k] = extra[k];
  var event = new KeyboardEvent("keydown", init);
  target.dispatchEvent(event);
  return event;
}
function focusIn(el) { el.focus(); el.dispatchEvent(new FocusEvent("focusin", { bubbles: true })); }
function active() { return document.activeElement ? (document.activeElement.id || document.activeElement.tagName) : "(none)"; }
`

// runBrowserProof serves `body` (HTML plus a <script> that awaits its assertions and finally sets
// data-kit-test="passed") with the files Deliver gives it — the kernel, each component the markup
// names, plus any named in components — drives it in a real headless browser, and fails with the
// assertion's own words.
func runBrowserProof(t *testing.T, label string, components []string, body string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping " + label + " browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	page := "<!doctype html><html><head></head><body>" + body + "</body></html>"
	page = strings.Replace(page, "<script>", "<script>"+proofScriptHead, 1)
	delivered := deliverTestPage(t, page, components, nil)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if delivered.serveFiles(response, request) {
			return
		}
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(delivered.html))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	profile := t.TempDir()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--window-size=1000,800",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=8000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 320
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("%s proof failed: %s", label, output[i:end])
	}
	t.Fatalf("%s proof did not pass (err=%v):\n%.2000s", label, err, output)
}
