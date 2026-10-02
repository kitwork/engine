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

// A native service is delivered like a component: only where something on the page names it — in
// the markup or in a component the page uses — after the kernel, before the components that call
// it, with the assembly first. A name that is the kernel's own (kit.theme) or nothing (kit.nope)
// brings no service: the control.
func TestServicesAreDeliveredWhereTheyAreNamed(t *testing.T) {
	site := fakeSite{"workbench": `kit.component("workbench", { init: function () { kit.studioState.clearHistory(); } });`}

	page := `<html><head></head><body><div data-kit-component="workbench"></div></body></html>`
	roles, paths := deliveredFiles(t, Deliver(page, site, Options{}))
	if strings.Join(roles, ",") != "runtime,service,service,component" ||
		!strings.HasPrefix(paths[1], "/kit/services/assembly.") || !strings.HasPrefix(paths[2], "/kit/services/studioState@1.0.0.") {
		t.Fatalf("named from a component: roles %v paths %v", roles, paths)
	}
	for _, path := range paths[1:3] {
		if asset, exact := AssetAt(path, site); asset == nil || !exact {
			t.Errorf("%s does not round-trip through AssetAt", path)
		}
	}
	stale := paths[2][:strings.LastIndexByte(paths[2][:len(paths[2])-3], '.')+1] + strings.Repeat("0", hashLength) + ".js"
	if asset, exact := AssetAt(stale, nil); asset == nil || exact {
		t.Errorf("an old hash of a known service must still answer, uncached: %s", stale)
	}
	if asset, _ := AssetAt("/kit/services/studioState@9.9.9."+strings.Repeat("0", hashLength)+".js", nil); asset != nil {
		t.Error("a version this engine does not carry must not resolve")
	}

	markup := `<html><head></head><body><button data-kit-click="kit.studioSqlite.close('x')">x</button></body></html>`
	if roles, paths := deliveredFiles(t, Deliver(markup, nil, Options{})); strings.Join(roles, ",") != "runtime,service,service" ||
		!strings.HasPrefix(paths[2], "/kit/services/studioSqlite@") {
		t.Errorf("named in markup: roles %v paths %v", roles, paths)
	}

	control := `<html><head></head><body><button data-kit-click="kit.theme.toggle(); kit.nope.x()">x</button></body></html>`
	if roles, _ := deliveredFiles(t, Deliver(control, nil, Options{})); strings.Join(roles, ",") != "runtime" {
		t.Errorf("CONTROL: the kernel's own service and an unknown name bring nothing, got %v", roles)
	}
}

// The whole road in a real browser: the shell's seeded host (faked here, as native/bridge seeds it
// at document-start) → the assembly → studioState → a component calling it. Answers come back
// through the host, and the kernel adopted the same host as its bridge. The second page has no host,
// as in a plain browser: the service answers UNAVAILABLE instead of hanging — the control.
func TestBrowserNativeServiceReachesTheShellsHost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping service browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	previous := Native
	Native = true
	defer func() { Native = previous }()

	site := fakeSite{"workbench": `kit.component("workbench", {
  result: "", failure: "",
  init: function () {
    var self = this;
    kit.studioState.clearHistory().then(function (value) { self.result = String(value); }, function (error) { self.failure = error.code || String(error); });
  }
});`}
	seed := `<script>
window.__calls = [];
document[Symbol.for("kitwork:native:host:v1")] = {
  version: "1.0.0",
  call: function (action, params) { window.__calls.push(action); return Promise.resolve(action === "studioState.clearHistory" ? true : false); }
};
</script>`
	body := `<div id="bench" data-kit-component="workbench"></div><script>
document.addEventListener("DOMContentLoaded", function () { (async function () {
` + proofScriptHead + `
  await wait(200);
  var bench = window.kit.scopeFor(document.getElementById("bench"));
  RESULT
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } }); });
</script>`
	withHost := Deliver(`<!doctype html><html><head>`+seed+`</head><body>`+strings.Replace(body, "RESULT", `
  assert(bench.result === "true", "the service answered through the shell's host, got " + JSON.stringify(bench.result) + " / " + bench.failure);
  assert(window.__calls.join(",") === "studioState.clearHistory", "one call reached the host: " + window.__calls.join(","));
  assert(window.kit.bridge === document[Symbol.for("kitwork:native:host:v1")], "the kernel adopted the same host as its bridge");`, 1)+`</body></html>`, site, Options{})
	withoutHost := Deliver(`<!doctype html><html><head></head><body>`+strings.Replace(body, "RESULT", `
  assert(bench.failure === "UNAVAILABLE" && bench.result === "", "with no host the service says UNAVAILABLE, got " + JSON.stringify(bench.failure));`, 1)+`</body></html>`, site, Options{})

	for label, page := range map[string]string{"with the shell's host": withHost, "CONTROL, no host": withoutHost} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if asset, _ := AssetAt(request.URL.Path, site); asset != nil {
				response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				_, _ = response.Write(asset.Body)
				return
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
			"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
			"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
		if runtime.GOOS == "windows" {
			args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
		}
		output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
		cancel()
		server.Close()
		if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
			continue
		}
		if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
			end := i + 300
			if end > len(output) {
				end = len(output)
			}
			t.Fatalf("%s: %s", label, output[i:end])
		}
		t.Fatalf("%s did not pass (err=%v):\n%.1500s", label, err, output)
	}
}
