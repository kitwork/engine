package js

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

// KitJS shipped a `terminal` component: a code block with a title bar, a tab per file and a copy
// control. kitwork.io mounts it three times on its home page, and all three are ONE panel with no
// tabs — the tabs were never used there.
//
// So the kernel does not need a terminal component. What the page actually wants is the copy
// control, and that is `clipboard` plus one piece of grammar: data-kit-element names the <pre>, and
// the button reads its text. This test is the evidence for that claim, because "we can do it with
// what we have" is the kind of sentence that should not be believed without a browser.
//
// It also pins the reach of data-kit-element: an expression may read a named element's textContent.
// If that ever stops being true, a terminal component becomes necessary again, and this test is
// where we would find out.
func TestBrowserACodeBlockNeedsNoTerminalComponent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping code block browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	// The shape kitwork.io's home page would use, reduced to what matters.
	page := `<!doctype html><html><body>
  <figure id="block" data-kit-component="clipboard">
    <div>
      <span>app.kitwork.js</span>
      <button id="copy" type="button" data-kit-click="copy($element.source.textContent)">
        <i id="idle" data-kit-show="!copied">copy</i>
        <i id="done" data-kit-show="copied" hidden>check</i>
        <span id="label" data-kit-text="copied ? 'Copied' : 'Copy'">Copy</span>
      </button>
    </div>
    <pre data-kit-element="source"><code>app.web({ port: 8080 })</code></pre>
    <button id="broken" type="button" data-kit-click="copy($element.nosuch.textContent)">broken</button>
  </figure>
  <script src="` + hydrate.RuntimePath + `"></script>
  <script>
  // Headless grants no clipboard permission, so the platform is stubbed: what is under test is the
  // kernel wiring — expression reads a named element, reaches the component, reaches the platform.
  window.__copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: function (text) { window.__copied.push(text); return Promise.resolve(); } } });
  </script>
  <script>
  (async function () {
    var root = document.documentElement;
    function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
    function assert(condition, message) { if (!condition) fail(message); }
    function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }

    await wait(50);
    assert(document.getElementById("label").textContent === "Copy", "resting label should be Copy, got " + document.getElementById("label").textContent);
    assert(document.getElementById("done").hidden === true, "the check mark should be hidden at rest");

    document.getElementById("copy").click();
    await wait(50);

    assert(window.__copied.length === 1, "one copy should have reached the platform, got " + window.__copied.length);
    assert(window.__copied[0] === "app.web({ port: 8080 })", "the block's own text should be copied, got " + JSON.stringify(window.__copied[0]));
    assert(document.getElementById("label").textContent === "Copied", "the label should confirm, got " + document.getElementById("label").textContent);
    assert(document.getElementById("idle").hidden === true && document.getElementById("done").hidden === false, "the icons should swap on confirmation");

    // The feedback is temporary, which is the component's only state. clipboard@v2.0.0 holds it
    // for two seconds — the version the engine actually ships, since findLatestComponentVersion
    // picks the highest, not v1.0.0.
    await wait(2500);
    assert(document.getElementById("label").textContent === "Copy", "the label should go back, got " + document.getElementById("label").textContent + " (scope.copied=" + (window.kit.components["clipboard"] && window.kit.components["clipboard"].copied) + ")");

    // An element name that does not resolve must copy NOTHING. A reader who gets the word
    // "undefined" on their clipboard has been told nothing went wrong when it did.
    window.__copied.length = 0;
    document.getElementById("broken").click();
    await wait(50);
    assert(window.__copied.length === 0, "a copy of an unresolved name must not reach the clipboard, got " + JSON.stringify(window.__copied));

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!document.documentElement.hasAttribute("data-kit-test")) { document.documentElement.setAttribute("data-kit-test", "failed"); document.documentElement.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
  </script>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(RuntimeJS([]string{"clipboard"})))
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
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
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(profile, "p"), "--virtual-time-budget=8000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 300
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("code block proof failed: %s", output[i:end])
	}
	t.Fatalf("code block proof did not pass (err=%v):\n%.2000s", err, output)
}
