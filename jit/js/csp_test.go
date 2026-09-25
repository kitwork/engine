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
	jittheme "github.com/kitwork/engine/jit/theme"
)

// What a strict Content-Security-Policy does to a Kitwork page, measured in a browser that is
// really enforcing one rather than argued from the source.
//
// CSP is not one lock. `script-src 'unsafe-eval'` is what a runtime needs when it calls eval or
// new Function to turn an attribute into code — the kernel never does, so it needs no such
// permission, and that is the hard half. But `style-src` is a separate directive, and the engine
// injects its CSS and its icon masks as inline <style>. Under `default-src 'self'` those blocks
// are refused, and the page renders unstyled while every expression on it keeps working.
//
// The policy below is the one a careful site actually writes, and the test asserts BOTH halves of
// the answer: the runtime survives it, the stylesheets do not.
func TestBrowserUnderAStrictContentSecurityPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CSP browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	page := `<!doctype html><html><head>
  <style data-kitwork-jit="css">.probe { color: rgb(1, 2, 3); }</style>
</head><body>
  <div id="box" data-kit-scope="{ count: 1 }" class="probe">
    <b id="out" data-kit-text="count * 21"></b>
    <button id="go" data-kit-click="count = count + 1">+</button>
  </div>
  <script src="` + hydrate.RuntimePath + `"></script>
  <script src="/proof.js"></script>
</body></html>`

	// The assertions live in a FILE, not inline: an inline <script> would be refused by the policy
	// under test, and the test would prove only that CSP works.
	proof := `
  (async function () {
    var root = document.documentElement;
    function fail(message) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", message); throw new Error(message); }
    function assert(condition, message) { if (!condition) fail(message); }
    function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }

    await wait(80);

    // THE RUNTIME SURVIVES. No eval, no new Function, so an expression compiles and runs under a
    // policy that grants neither.
    assert(window.kit, "the kernel did not load under the policy");
    assert(document.getElementById("out").textContent === "21", "an expression did not evaluate: got " + JSON.stringify(document.getElementById("out").textContent));
    document.getElementById("go").click();
    await wait(50);
    assert(document.getElementById("out").textContent === "42", "a write did not repaint: got " + document.getElementById("out").textContent);

    // THE STYLESHEET DOES NOT. The engine's own <style data-kitwork-jit="css"> is inline, and
    // style-src in this policy allows neither 'unsafe-inline' nor a hash for it.
    var colour = getComputedStyle(document.getElementById("box")).color;
    root.setAttribute("data-kit-css-colour", colour);

    root.setAttribute("data-kit-test", "passed");
  })().catch(function (error) { if (!document.documentElement.hasAttribute("data-kit-test")) { document.documentElement.setAttribute("data-kit-test", "failed"); document.documentElement.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
`

	// What a site that takes CSP seriously writes. Note what is NOT here: 'unsafe-eval'.
	const policy = "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'none'"

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case hydrate.RuntimePath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(hydrate.Runtime()))
		case "/proof.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(proof))
		case "/":
			response.Header().Set("Content-Security-Policy", policy)
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
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 300
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("the runtime did not survive a strict policy: %s", output[i:end])
	}
	if !bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		t.Fatalf("CSP proof did not run:\n%.2000s", output)
	}

	// The measured half. rgb(1, 2, 3) would mean the inline <style> was applied.
	if i := bytes.Index(output, []byte(`data-kit-css-colour="`)); i >= 0 {
		rest := output[i+len(`data-kit-css-colour="`):]
		if j := bytes.IndexByte(rest, '"'); j >= 0 {
			t.Logf("under %q the engine's inline <style> produced colour %s (the block declares rgb(1, 2, 3))", policy, rest[:j])
		}
	}
	if bytes.Contains(output, []byte(`data-kit-css-colour="rgb(1, 2, 3)"`)) {
		t.Error("the engine's inline <style> was APPLIED under style-src 'self' — if browsers now allow this, the finding below is stale and jiticons/jitcss need no hash")
	}

	// And the fix the engine already knows how to write, for script, and has not yet written for
	// style: a hash is permission for one known block, nothing wider.
	if jittheme.PrepaintHash() == "" {
		t.Error("the theme pre-paint has no CSP hash; the same mechanism is what style-src needs")
	}
}
