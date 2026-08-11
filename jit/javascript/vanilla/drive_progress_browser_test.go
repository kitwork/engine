package vanilla

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const driveProgressArtifactName = "hydrate.kit.0.8.0.9a1f9c39f86cecbc71157cb3ef2e28363f2f8989ab04d1ff3491eac0c2dde534.js"

var driveProgressHostRE = regexp.MustCompile(`(?is)<section\b[^>]*\bdata-kit-retain\s*=\s*"app-progress"[^>]*>`)

func TestDriveProgressExampleContract(t *testing.T) {
	artifact := buildDriveProgressArtifact(t)
	if artifact.Name() != driveProgressArtifactName {
		t.Fatalf("Drive progress artifact = %q, want %q", artifact.Name(), driveProgressArtifactName)
	}
	checked := readVanillaFile(t, "examples", "drive-progress", driveProgressArtifactName)
	if !bytes.Equal(checked, artifact.Bytes()) {
		t.Fatalf("checked Drive progress artifact %s is stale", driveProgressArtifactName)
	}
	routes := []string{"index.html", "next.html"}
	var retainedAttributes map[string]string
	for _, route := range routes {
		source := string(readVanillaFile(t, "examples", "drive-progress", route))
		matches := externalScriptRE.FindAllStringSubmatch(source, -1)
		if len(matches) != 1 {
			t.Fatalf("%s external script count = %d, want one placeholder artifact", route, len(matches))
		}
		script := matches[0][1]
		if script == "" {
			script = matches[0][2]
		}
		if script != "./"+driveProgressArtifactName {
			t.Fatalf("%s artifact URL = %q, want %q", route, script, "./"+driveProgressArtifactName)
		}
		for _, required := range []string{
			`data-kit-retain="app-progress"`, `data-kit-component="progress-bar"`, `data-kit-version="2.0.0"`,
			`role="progressbar"`, `aria-valuemin="0"`, `aria-valuemax="100"`,
			`aria-valuenow: value`, `max-w-8xl`,
			`focus-visible:outline`, `focus-visible:outline-2`, `focus-visible:outline-offset-2`,
			`bg-indigo-600`,
			`id="drive-progress-slow"`, `id="drive-progress-fast"`, `id="drive-progress-error"`,
		} {
			if !strings.Contains(source, required) {
				t.Fatalf("%s lost Drive progress contract %s", route, required)
			}
		}
		lower := strings.ToLower(source)
		for _, forbidden := range []string{
			"data-kit-app", "data-kit-hydrate", "data-kit-style", "<style", "<dialog", "<details",
			"bg-indigo-500", "fill-", "motion-safe:animate-pulse", "focus-visible:ring-", "focus-visible:outline-none",
			`id="drive-progress"`, `id="drive-progress-message"`,
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains forbidden demo construct %q", route, forbidden)
			}
		}
		host := driveProgressHostRE.FindString(source)
		if host == "" {
			t.Fatalf("%s has no retained progress component host", route)
		}
		attributes := make(map[string]string)
		for _, match := range shopDataKitAttrRE.FindAllStringSubmatch(host, -1) {
			attributes[strings.ToLower(match[1])] = match[2]
		}
		if retainedAttributes == nil {
			retainedAttributes = attributes
		} else if !sameStringMap(retainedAttributes, attributes) {
			t.Fatalf("%s retained progress attributes = %#v, want %#v", route, attributes, retainedAttributes)
		}
	}

	component := string(readVanillaFile(t, "component", "progress-bar", "2.0.0.js"))
	if component == "" || component[0] != ';' {
		t.Fatal("progress-bar@2.0.0 is not a sealable classic script")
	}
	for _, required := range []string{
		`kit.component("progress-bar"`, `kit.progress.subscribe(`,
		`visible: false`, `value: null`, `clearTimeout(hideTimer)`, `unsubscribe()`,
	} {
		if !strings.Contains(component, required) {
			t.Fatalf("progress-bar@2.0.0 lost %s", required)
		}
	}
	for _, forbidden := range []string{
		"kit:navigation", "document.", "kit.progress.snapshot", "WeakMap", "manualSequence",
		"start: function", "set: function", "inc: function", "done: function", "reset: function",
		"status:", "message:", "source:",
	} {
		if strings.Contains(component, forbidden) {
			t.Fatalf("progress-bar@2.0.0 contains non-presentation behavior %q", forbidden)
		}
	}
	for _, mojibake := range []string{"Â", "Ã", "�"} {
		if strings.Contains(component, mojibake) {
			t.Fatalf("progress-bar@2.0.0 contains mojibake %q", mojibake)
		}
	}
}

func TestBrowserDriveProgressExample(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping actual Drive progress browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildDriveProgressArtifact(t)
	assetPath := "/examples/drive-progress/" + artifact.Name()
	stylePath := "/examples/" + exampleStylesheetName
	stylesheet := readVanillaFile(t, "examples", exampleStylesheetName)
	pages := map[string][]byte{
		"/examples/drive-progress/index.html": driveProgressPage(t, "index.html", artifact.Name()),
		"/examples/drive-progress/next.html":  driveProgressPage(t, "next.html", artifact.Name()),
	}
	initial := injectDriveProgressSpy(t,
		injectBrowserAssertions(t, pages["/examples/drive-progress/index.html"], driveProgressAssertions))
	directPages := map[string][]byte{
		"/examples/drive-progress/index.html": injectBrowserAssertions(t, pages["/examples/drive-progress/index.html"], driveProgressDirectAssertions),
		"/examples/drive-progress/next.html":  injectBrowserAssertions(t, pages["/examples/drive-progress/next.html"], driveProgressDirectAssertions),
	}
	var artifactRequests atomic.Int64
	var styleRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == assetPath {
			artifactRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
			return
		}
		if request.URL.Path == stylePath {
			styleRequests.Add(1)
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = response.Write(stylesheet)
			return
		}
		page, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}

		isDrive := request.Header.Get("X-KitJS-Drive") == "1"
		variant := request.URL.Query().Get("visit")
		if !isDrive && variant == "error" {
			response.Header().Set("Set-Cookie", "drive_progress_error_fallback=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if isDrive && variant == "error" {
			hijacker, ok := response.(http.Hijacker)
			if !ok {
				http.Error(response, "connection failure", http.StatusInternalServerError)
				return
			}
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		if request.URL.Query().Get("direct") == "1" {
			writeDriveProgressResponse(response, directPages[request.URL.Path])
			return
		}
		if !isDrive && request.URL.Path == "/examples/drive-progress/index.html" && variant == "" {
			writeDriveProgressResponse(response, initial)
			return
		}
		if isDrive && variant == "slow" {
			writeSlowDriveProgressResponse(response, request, page)
			return
		}
		writeDriveProgressResponse(response, page)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/examples/drive-progress/index.html")
	if got := artifactRequests.Load(); got != 1 {
		t.Fatalf("Drive navigation requested its sealed progress artifact %d times, want 1", got)
	}

	for _, route := range []string{"index.html", "next.html"} {
		route := route
		t.Run("direct-"+strings.TrimSuffix(route, ".html"), func(t *testing.T) {
			runVanillaBrowser(t, browser, server.URL+"/examples/drive-progress/"+route+"?direct=1")
		})
	}
	if got := artifactRequests.Load(); got != 3 {
		t.Fatalf("progress artifact requests after direct loads = %d, want 3", got)
	}
	if got := styleRequests.Load(); got != 3 {
		t.Fatalf("progress stylesheet requests = %d, want 3", got)
	}
}

func buildDriveProgressArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{progressServicePackage(t)},
		Components: []ComponentVersion{{
			Name: "progress-bar", Version: "2.0.0",
		}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "progress-bar",
			Service:   ServiceVersion{Name: "progress", Version: "1.0.0"},
		}},
		Scripts: []Script{{
			Name: "progress-bar", Source: readVanillaFile(t, "component", "progress-bar", "2.0.0.js"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func driveProgressPage(t *testing.T, name, artifactName string) []byte {
	t.Helper()
	source := readVanillaFile(t, "examples", "drive-progress", name)
	if got := bytes.Count(source, []byte("./"+artifactName)); got != 1 {
		t.Fatalf("%s artifact URL count = %d, want 1", name, got)
	}
	return source
}

func injectDriveProgressSpy(t *testing.T, source []byte) []byte {
	t.Helper()
	needle := []byte("  <script defer src=\"")
	index := bytes.Index(source, needle)
	if index < 0 {
		t.Fatal("progress page has no external artifact seam")
	}
	spy := []byte(`<script>
    globalThis.__progressTimerIDs = [];
    globalThis.__progressOwnedClears = 0;
    globalThis.__progressNavigationAdds = 0;
    globalThis.__progressNavigationRemoves = 0;
    var __progressSetTimeout = globalThis.setTimeout.bind(globalThis);
    var __progressClearTimeout = globalThis.clearTimeout.bind(globalThis);
    globalThis.setTimeout = function (callback, delay) {
      var id = __progressSetTimeout(callback, delay);
      if (delay === 300) globalThis.__progressTimerIDs.push(id);
      return id;
    };
    globalThis.clearTimeout = function (id) {
      if (globalThis.__progressTimerIDs.indexOf(id) >= 0) globalThis.__progressOwnedClears++;
      return __progressClearTimeout(id);
    };
    var __progressAddEventListener = document.addEventListener.bind(document);
    var __progressRemoveEventListener = document.removeEventListener.bind(document);
    document.addEventListener = function (type, listener, options) {
      if (type === "kit:navigation") globalThis.__progressNavigationAdds++;
      return __progressAddEventListener(type, listener, options);
    };
    document.removeEventListener = function (type, listener, options) {
      if (type === "kit:navigation") globalThis.__progressNavigationRemoves++;
      return __progressRemoveEventListener(type, listener, options);
    };
  </script>
`)
	return append(append(append([]byte(nil), source[:index]...), spy...), source[index:]...)
}

func writeDriveProgressResponse(response http.ResponseWriter, source []byte) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Encoding", "identity")
	response.Header().Set("Content-Length", strconv.Itoa(len(source)))
	_, _ = response.Write(source)
}

func writeSlowDriveProgressResponse(response http.ResponseWriter, request *http.Request, source []byte) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Encoding", "identity")
	response.Header().Set("Content-Length", strconv.Itoa(len(source)))
	response.WriteHeader(http.StatusOK)

	first := len(source) / 3
	select {
	case <-request.Context().Done():
		return
	case <-time.After(300 * time.Millisecond):
	}
	_, _ = response.Write(source[:first])
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
	select {
	case <-request.Context().Done():
		return
	case <-time.After(700 * time.Millisecond):
	}
	_, _ = response.Write(source[first:])
}

const driveProgressDirectAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    var host = document.querySelector('[data-kit-retain="app-progress"]');
    return globalThis.kit && host && host.hidden === true;
  }, "direct-loaded progress component did not boot idle");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress",
    "direct progress route expanded public API");
  assert(document.querySelectorAll("script[src]").length === 1,
    "direct progress route did not use one sealed artifact");
  var bar = document.querySelector('[data-kit-retain="app-progress"] [role="progressbar"]');
  assert(bar && !bar.hasAttribute("aria-valuenow"), "idle direct progress exposed a false value");
});`

const driveProgressAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var root = document.documentElement;
  await waitFor(function () {
    var current = document.querySelector('[data-kit-retain="app-progress"]');
    return globalThis.kit && current && current.hidden === true;
  }, "Drive progress did not boot");

  var host = document.querySelector('[data-kit-retain="app-progress"]');
  var bar = host.querySelector("[role='progressbar']");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress", "progress service API was incomplete");
  assert(globalThis.__progressNavigationAdds === 1, "progress service installed duplicate navigation listeners");
  assert(!bar.hasAttribute("aria-valuenow"), "idle progress exposed aria-valuenow");
  var pulse = host.querySelector("rect[data-kit-show='value === null']");
  var pulseStyle = getComputedStyle(pulse);
  assert(pulseStyle.fill === "rgb(129, 140, 248)" && pulseStyle.animationName === "animate--pulse",
    "checked JIT stylesheet did not style the indeterminate progress visual");
  var slowControl = document.getElementById("drive-progress-slow");
  slowControl.focus();
  var focusStyle = getComputedStyle(slowControl);
  assert(focusStyle.outlineStyle !== "none" && parseFloat(focusStyle.outlineWidth) >= 2,
    "checked JIT stylesheet did not provide a visible focus outline");

  slowControl.click();
  await waitFor(function () {
    return host.hidden === false && bar.getAttribute("aria-busy") === "true" &&
      !bar.hasAttribute("aria-valuenow");
  }, "real Drive start was not indeterminate");
  await waitFor(function () {
    var value = Number(bar.getAttribute("aria-valuenow"));
    return value > 0 && value < 100;
  }, "real streamed bytes did not make progress determinate");

  document.getElementById("drive-progress-fast").click();
  await waitFor(function () {
    return location.pathname === "/examples/drive-progress/next.html" &&
      location.search === "?visit=fast" && bar.getAttribute("aria-valuenow") === "100";
  }, "latest real Drive visit did not win");
  assert(document.documentElement === root && document.querySelector('[data-kit-retain="app-progress"]') === host,
    "successful Drive progress replaced its retained host");
  assert(bar.getAttribute("aria-valuenow") === "100" && bar.getAttribute("aria-valuetext") === "100%",
    "loaded finish did not become determinate 100");

  document.getElementById("drive-progress-error").click();
  await waitFor(function () {
    return document.cookie.indexOf("drive_progress_error_fallback=1") >= 0 &&
      host.hidden === true && bar.getAttribute("aria-busy") === "false";
  }, "real Drive fetch failure did not reach progress");
  assert(globalThis.__progressOwnedClears >= 1, "new navigation did not clear the loaded hide timer");
  await new Promise(function (resolve) { setTimeout(resolve, 350); });
  assert(host.hidden === true, "stale loaded timer made a failed visit visible");

  document.getElementById("drive-progress-slow").click();
  await waitFor(function () { return host.hidden === false; },
    "progress did not recover from failure");
  document.getElementById("drive-progress-fast").click();
  await waitFor(function () {
    return location.pathname === "/examples/drive-progress/index.html" &&
      location.search === "?visit=fast" && bar.getAttribute("aria-valuenow") === "100";
  }, "second latest Drive visit did not commit");

  var clearsBeforeRemoval = globalThis.__progressOwnedClears;
  host.remove();
  await waitFor(function () {
    return globalThis.__progressOwnedClears === clearsBeforeRemoval + 1;
  }, "component disposal did not clear its loaded timer");
  assert(globalThis.__progressNavigationRemoves === 0,
    "component disposal removed the artifact-lifetime progress service listener");
  var retainedValue = bar.getAttribute("aria-valuenow");
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: 999, phase: "start", url: location.href })
  }));
  await new Promise(function (resolve) { setTimeout(resolve, 0); });
  assert(bar.getAttribute("aria-valuenow") === retainedValue,
    "disposed progress component still changed its detached presentation");
});`

func TestDriveProgressHelpersDoNotLeakGoroutines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/slow", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		writeSlowDriveProgressResponse(recorder, request, []byte("complete response"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled slow response retained its handler")
	}
}

func TestDriveProgressArtifactNameIsVersioned(t *testing.T) {
	digest := strings.TrimSuffix(strings.TrimPrefix(driveProgressArtifactName, "hydrate.kit.0.8.0."), ".js")
	if !strings.HasPrefix(driveProgressArtifactName, "hydrate.kit.0.8.0.") ||
		!strings.HasSuffix(driveProgressArtifactName, ".js") || len(digest) != 64 {
		t.Fatalf("invalid Drive progress artifact name %q", driveProgressArtifactName)
	}
}
