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
)

// After Drive swaps a page, kit.components and the $aliases describe the NEW page: a component that
// left is gone from both, and one that arrived is in both. The toast arrives on the very <div> the
// tabs left — same tag, same place — which morph used to patch in place, keeping the tabs' scope
// under the toast's name (02/10). The arriving toast is also the control: a registry that simply
// emptied itself would fail it.
func TestBrowserDriveLeavesNoStaleComponents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Drive browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	first := Deliver(`<!doctype html><html><head></head><body><main>
<div data-kit-component="tabs" data-kit-alias="$docs"><div role="tablist"><button data-kit-element="tab" data-tab="a">a</button></div></div>
<ul role="tree" data-kit-component="tree" data-kit-alias="$files"><li data-kit-element="item" data-id="one">one</li></ul>
<a id="go" href="/next">next</a>
</main><script>
document.addEventListener("DOMContentLoaded", function () { (async function () {
`+proofScriptHead+`
  await wait(100);
  var before = Object.keys(window.kit.components).sort().join(",");
  assert(before === "tabs,tree", "page A registers its components, got " + before);
  document.getElementById("go").click();
  for (var i = 0; i < 40 && !document.getElementById("arrived"); i++) await wait(100);
  await wait(200);
  assert(document.getElementById("arrived"), "Drive swapped in page B");
  var after = Object.keys(window.kit.components).sort().join(",");
  assert(after === "toast", "kit.components describes page B only, got " + after);
  // Aliases resolve on the page scope, where markup reads them.
  var page = window.kit.scope;
  assert(!page.$docs, "the alias of a component that left is gone, got " + typeof page.$docs);
  assert(page.$note && typeof page.$note.show === "function", "the alias of the component that arrived resolves, got " + typeof page.$note);
  assert(typeof window.kit.components.toast.show === "function", "kit.components.toast is the toast, not the tabs a morph patched into its place");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } }); });
</script></body></html>`, nil, Options{Drive: true})
	second := Deliver(`<!doctype html><html><head></head><body><main>
<div id="arrived" data-kit-component="toast" data-kit-alias="$note"></div>
</main></body></html>`, nil, Options{Drive: true})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if asset, _ := AssetAt(request.URL.Path, nil); asset != nil {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(asset.Body)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		if request.URL.Path == "/next" {
			_, _ = response.Write([]byte(second))
			return
		}
		_, _ = response.Write([]byte(first))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
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
		t.Fatalf("Drive proof failed: %s", output[i:end])
	}
	t.Fatalf("Drive proof did not pass (err=%v):\n%.1500s", err, output)
}
