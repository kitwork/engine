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
		!strings.HasPrefix(paths[1], "/_kitwork/services/assembly.") || !strings.HasPrefix(paths[2], "/_kitwork/services/studioState.") {
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
	// A service's name on the wire carries no version (11/10/2026). The name a page carried before
	// spelled the version the engine had then; the engine carries one version of each service, so
	// that name — under either prefix, whatever version it spelled — answers today's file, never as
	// its own name. CONTROL: a service the engine does not carry, or a version that is no version,
	// names nothing.
	for _, old := range []string{
		"/_kitwork/services/studioState@" + serviceVersion("studioState") + "." + strings.Repeat("0", hashLength) + ".js",
		"/kit/services/studioState@" + serviceVersion("studioState") + "." + strings.Repeat("0", hashLength) + ".js",
		"/kit/services/studioState@0.9.0." + strings.Repeat("0", hashLength) + ".js",
	} {
		if asset, exact := AssetAt(old, nil); asset == nil || exact || asset.Path != paths[2] {
			t.Errorf("%s: want today's file %s, not exact; got %v exact=%t", old, paths[2], assetPath(asset), exact)
		}
	}
	for _, nothing := range []string{
		"/_kitwork/services/nonesuch@1.0.0." + strings.Repeat("0", hashLength) + ".js",
		"/_kitwork/services/nonesuch." + strings.Repeat("0", hashLength) + ".js",
		"/_kitwork/services/studioState@latest." + strings.Repeat("0", hashLength) + ".js",
		"/_kitwork/services/assembly@1.0.0." + strings.Repeat("0", hashLength) + ".js",
	} {
		if asset, _ := AssetAt(nothing, nil); asset != nil {
			t.Errorf("%s must not resolve, got %s", nothing, asset.Path)
		}
	}

	markup := `<html><head></head><body><button data-kit-click="kit.studioSqlite.close('x')">x</button></body></html>`
	if roles, paths := deliveredFiles(t, Deliver(markup, nil, Options{})); strings.Join(roles, ",") != "runtime,service,service" ||
		!strings.HasPrefix(paths[2], "/_kitwork/services/studioSqlite.") {
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

// Every service this engine carries loads on one page, in an order that works — network before
// files (files takes the upload boundary network leaves), files before camera and media — and
// registers, the KitJS files/media/network/camera replacing the kernel's own on that page (02/10,
// agreed: no code anywhere calls the kernel-only members). Nothing reports an error while loading.
func TestBrowserEveryServiceLoadsInOrder(t *testing.T) {
	names := ServiceNames()
	if len(names) < 13 {
		t.Fatalf("expected the restored services, have %v", names)
	}
	previous := Native
	Native = true
	defer func() { Native = previous }()
	var markup, checks strings.Builder
	for _, name := range names {
		markup.WriteString(`<button data-kit-click="kit.` + name + `.x()">x</button>`)
	}
	members := map[string]string{"files": "importBlob", "media": "pickImage", "network": "request", "camera": "capture",
		"notifications": "schedule", "biometric": "authenticate", "geolocation": "getCurrentPosition", "nfc": "scan",
		"deepLinks": "snapshot", "qr": "scan", "studioState": "loadSnapshot", "studioSqlite": "open", "studioDatabase": "open"}
	for name, member := range members {
		checks.WriteString(`  assert(window.kit.` + name + ` && typeof window.kit.` + name + `.` + member + ` === "function", "kit.` + name + `.` + member + ` is missing");` + "\n")
	}
	page := Deliver(`<!doctype html><html><head><script>window.__errors = []; window.addEventListener("error", function (e) { window.__errors.push(String(e.message)); });</script></head><body>`+markup.String()+`<script>
document.addEventListener("DOMContentLoaded", function () { (async function () {
`+proofScriptHead+`
  await wait(100);
  assert(window.__errors.length === 0, "loading reported: " + window.__errors.join(" | "));
`+checks.String()+`
  assert(typeof window.kit.files.read !== "function", "on this page files is the KitJS one, not the kernel's");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } }); });
</script></body></html>`, nil, Options{})
	_, paths := deliveredFiles(t, page)
	order := strings.Join(paths, " ")
	index := func(name string) int { return strings.Index(order, "/_kitwork/services/"+name+".") }
	for _, name := range []string{"network", "files", "camera", "media"} {
		if index(name) < 0 {
			t.Fatalf("no file for %s: %s", name, order)
		}
	}
	if !(index("network") < index("files") && index("files") < index("camera") && index("files") < index("media")) {
		t.Fatalf("services out of order: %s", order)
	}

	browser := findHeadlessBrowser()
	if browser == "" || testing.Short() {
		t.Skip("no browser")
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if asset, _ := AssetAt(request.URL.Path, nil); asset != nil {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(asset.Body)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(page))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--disable-background-networking",
		"--disable-default-apps", "--disable-extensions", "--disable-sync", "--metrics-recording-only", "--no-first-run",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(t.TempDir(), "p"), "--virtual-time-budget=6000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, err := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if i := bytes.Index(output, []byte("data-kit-test-error=")); i >= 0 {
		end := i + 400
		if end > len(output) {
			end = len(output)
		}
		t.Fatalf("services did not all load: %s", output[i:end])
	}
	t.Fatalf("no verdict (err=%v):\n%.1500s", err, output)
}
