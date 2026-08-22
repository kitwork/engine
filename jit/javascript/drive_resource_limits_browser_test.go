package javascript

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	driveResponseBytesLimit = 8 << 20
	driveDocumentNodeLimit  = 100000
)

func TestBrowserStandaloneDriveBoundsDestinationWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Drive destination resource-bound browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	hydrateJS, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}
	contractJS := []byte(browserHarness + "\n" + driveResourceLimitAssertions)
	initial := driveResourceLimitDocument("initial", "")
	exact := driveResourceLimitExactDocument()
	if got := len(exact); got != driveResponseBytesLimit {
		t.Fatalf("exact byte-limit fixture length = %d, want %d", got, driveResponseBytesLimit)
	}
	nodeExact := driveResourceLimitNodeDocument(driveDocumentNodeLimit)
	nodeOver := driveResourceLimitNodeDocument(driveDocumentNodeLimit + 1)
	depthExact := driveResourceLimitDepthDocument(256)
	depthOver := driveResourceLimitDepthDocument(257)
	var earlyStreamCancelled atomic.Int64
	var encodedOver bytes.Buffer
	compressed := gzip.NewWriter(&encodedOver)
	if _, err := compressed.Write([]byte(exact + "x")); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	driveRequests := make(map[string]int)
	nativeRequests := make(map[string]int)
	count := func(name string, drive bool) {
		mu.Lock()
		defer mu.Unlock()
		if drive {
			driveRequests[name]++
		} else {
			nativeRequests[name]++
		}
	}
	counts := func(name string) (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return driveRequests[name], nativeRequests[name]
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/limits/hydrate.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
			return
		case "/limits/contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractJS)
			return
		case "/limits/start":
			writeHydrateHTML(response, initial)
			return
		}

		name := strings.TrimPrefix(request.URL.Path, "/limits/")
		drive := request.Header.Get("X-KitJS-Drive") == "1"
		count(name, drive)
		if !drive {
			response.Header().Set("Set-Cookie", "kit_drive_limit_"+strings.ReplaceAll(name, "-", "_")+"=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
			return
		}

		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		if name == "non-html-stream" {
			response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
			earlyStreamCancelled.Add(1)
			return
		}
		switch name {
		case "exact":
			response.Header().Set("Content-Length", strconv.Itoa(len(exact)))
			_, _ = response.Write([]byte(exact))
		case "declared-over":
			response.Header().Set("Content-Length", strconv.Itoa(driveResponseBytesLimit+1))
			response.WriteHeader(http.StatusOK)
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
		case "unknown-over":
			response.WriteHeader(http.StatusOK)
			flusher, _ := response.(http.Flusher)
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = response.Write([]byte(driveResourceLimitDocument("unknown-over", "")))
		case "encoded-over":
			response.Header().Set("Content-Encoding", "gzip")
			response.Header().Set("Content-Length", strconv.Itoa(encodedOver.Len()))
			_, _ = response.Write(encodedOver.Bytes())
		case "encoded-declared-over":
			response.Header().Set("Content-Encoding", "gzip")
			response.Header().Set("Content-Length", strconv.Itoa(driveResponseBytesLimit+1))
			response.WriteHeader(http.StatusOK)
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
		case "node-exact":
			_, _ = response.Write([]byte(nodeExact))
		case "node-over":
			_, _ = response.Write([]byte(nodeOver))
		case "depth-exact":
			_, _ = response.Write([]byte(depthExact))
		case "depth-over":
			_, _ = response.Write([]byte(depthOver))
		case "setup-failure", "decoder-failure":
			_, _ = response.Write([]byte(driveResourceLimitDocument(name, "")))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runDriveResourceLimitBrowser(t, browser, server.URL+"/limits/start")
	if drive, native := counts("exact"); drive != 1 || native != 0 {
		t.Fatalf("exact byte-limit requests = Drive %d, native %d; want Drive 1, native 0", drive, native)
	}
	for _, name := range []string{"node-exact", "depth-exact"} {
		if drive, native := counts(name); drive != 1 || native != 0 {
			t.Fatalf("%s requests = Drive %d, native %d; want Drive 1, native 0", name, drive, native)
		}
	}
	for _, name := range []string{"declared-over", "unknown-over", "encoded-over", "encoded-declared-over", "non-html-stream", "node-over", "depth-over", "setup-failure", "decoder-failure"} {
		if drive, native := counts(name); drive != 1 || native != 1 {
			t.Fatalf("%s requests = Drive %d, native %d; want Drive 1, native 1", name, drive, native)
		}
	}
	if got := earlyStreamCancelled.Load(); got != 1 {
		t.Fatalf("headers-first non-HTML stream cancellation count = %d, want one", got)
	}
}

func runDriveResourceLimitBrowser(t *testing.T, browser, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-first-run",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + t.TempDir(),
		"--virtual-time-budget=120000",
		"--dump-dom",
		target,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-test="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("Drive resource-limit browser proof timed out: %v\n%s", ctx.Err(), boundedVanillaOutput(output))
	}
	if runErr != nil {
		t.Fatalf("Drive resource-limit browser proof failed to run: %v\n%s", runErr, boundedVanillaOutput(output))
	}
	t.Fatalf("Drive resource-limit browser proof did not pass\n%s", boundedVanillaOutput(output))
}

func driveResourceLimitDocument(marker, extra string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Drive limit %s</title>
  <script defer src="/limits/hydrate.js" data-kit-drive="stable"></script>
  <script defer src="/limits/contract.js" data-kit-drive="stable"></script>
</head>
<body>
  <main id="limit-marker">%s</main>
  <a id="limit-exact" href="/limits/exact">Exact</a>
  <a id="limit-node-exact" href="/limits/node-exact">Node exact</a>
  <a id="limit-depth-exact" href="/limits/depth-exact">Depth exact</a>
  <a id="limit-declared-over" href="/limits/declared-over">Declared over</a>
  <a id="limit-unknown-over" href="/limits/unknown-over">Unknown over</a>
  <a id="limit-encoded-over" href="/limits/encoded-over">Encoded over</a>
  <a id="limit-encoded-declared-over" href="/limits/encoded-declared-over">Encoded declared over</a>
  <a id="limit-non-html-stream" href="/limits/non-html-stream">Non-HTML stream</a>
  <a id="limit-node-over" href="/limits/node-over">Node over</a>
  <a id="limit-depth-over" href="/limits/depth-over">Depth over</a>
  <a id="limit-setup-failure" href="/limits/setup-failure">Stream setup failure</a>
  <a id="limit-decoder-failure" href="/limits/decoder-failure">Decoder failure</a>
  <div id="limit-padding" hidden>%s</div>
</body>
</html>`, marker, marker, extra)
}

func driveResourceLimitExactDocument() string {
	base := driveResourceLimitDocument("exact", "")
	remaining := driveResponseBytesLimit - len(base)
	if remaining < 0 {
		panic("Drive exact-limit fixture exceeds its target before padding")
	}
	return driveResourceLimitDocument("exact", strings.Repeat("x", remaining))
}

func driveResourceLimitCompactDocument(marker, templateContent string) string {
	return fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Drive limit %s</title><script defer src="/limits/hydrate.js" data-kit-drive="stable"></script><script defer src="/limits/contract.js" data-kit-drive="stable"></script><template id="limit-candidate-work">%s</template></head><body><main id="limit-marker">%s</main><a id="limit-exact" href="/limits/exact">Exact</a><a id="limit-node-exact" href="/limits/node-exact">Node exact</a><a id="limit-depth-exact" href="/limits/depth-exact">Depth exact</a><a id="limit-declared-over" href="/limits/declared-over">Declared over</a><a id="limit-unknown-over" href="/limits/unknown-over">Unknown over</a><a id="limit-encoded-over" href="/limits/encoded-over">Encoded over</a><a id="limit-encoded-declared-over" href="/limits/encoded-declared-over">Encoded declared over</a><a id="limit-non-html-stream" href="/limits/non-html-stream">Non-HTML stream</a><a id="limit-node-over" href="/limits/node-over">Node over</a><a id="limit-depth-over" href="/limits/depth-over">Depth over</a><a id="limit-setup-failure" href="/limits/setup-failure">Stream setup failure</a><a id="limit-decoder-failure" href="/limits/decoder-failure">Decoder failure</a></body></html>`, marker, templateContent, marker)
}

func driveResourceLimitNodeDocument(nodes int) string {
	const compactDocumentNodes = 38
	if nodes < compactDocumentNodes {
		panic("Drive node-limit fixture is smaller than its document shell")
	}
	return driveResourceLimitCompactDocument("node-"+strconv.Itoa(nodes),
		strings.Repeat("<i></i>", nodes-compactDocumentNodes))
}

func driveResourceLimitDepthDocument(depth int) string {
	const templateContentDepth = 4
	if depth < templateContentDepth {
		panic("Drive depth-limit fixture is shallower than its template content root")
	}
	nested := depth - templateContentDepth
	return driveResourceLimitCompactDocument("depth-"+strconv.Itoa(depth),
		strings.Repeat("<div>", nested)+strings.Repeat("</div>", nested))
}

const driveResourceLimitAssertions = `
(function () {
  "use strict";
  var events = [];
  document.addEventListener("kit:navigation", function (event) { events.push(event.detail); });
  var realFetch = globalThis.fetch.bind(globalThis);
  var unboundedTextCalls = 0;
  var setupCancelCalls = 0;
  var decoderCancelCalls = 0;
  var earlyCancelCalls = 0;
  globalThis.fetch = function (source, options) {
    var url = new URL(source, location.href);
    return realFetch(source, options).then(function (response) {
      if (!options || !options.headers || options.headers["X-KitJS-Drive"] !== "1") return response;
      if (url.pathname === "/limits/unknown-over") {
        var headers = new Headers(response.headers);
        headers.delete("content-length");
        var body = new ReadableStream({
          start: function (controller) {
            controller.enqueue(new Uint8Array(8 * 1024 * 1024));
            controller.enqueue(new Uint8Array(1));
            controller.close();
          }
        });
        return { ok: response.ok, url: response.url, headers: headers, body: body };
      }
      if (url.pathname === "/limits/decoder-failure") {
        var RealTextDecoder = globalThis.TextDecoder;
        globalThis.TextDecoder = function () { throw new Error("synthetic decoder setup failure"); };
        setTimeout(function () { globalThis.TextDecoder = RealTextDecoder; }, 0);
        return {
          ok: response.ok,
          url: response.url,
          headers: response.headers,
          body: {
            getReader: function () { return response.body.getReader(); },
            cancel: function () { decoderCancelCalls++; return response.body.cancel(); }
          },
          text: function () {
            unboundedTextCalls++;
            return response.text();
          }
        };
      }
      if (url.pathname === "/limits/non-html-stream") {
        return {
          ok: response.ok,
          url: response.url,
          headers: response.headers,
          body: {
            getReader: function () { return response.body.getReader(); },
            cancel: function () { earlyCancelCalls++; return response.body.cancel(); }
          }
        };
      }
      if (url.pathname !== "/limits/setup-failure") return response;
      return {
        ok: response.ok,
        url: response.url,
        headers: response.headers,
        body: {
          getReader: function () { throw new Error("synthetic reader setup failure"); },
          cancel: function () { setupCancelCalls++; return Promise.resolve(); }
        },
        text: function () {
          unboundedTextCalls++;
          return response.text();
        }
      };
    });
  };

  __runStandaloneKitTest(async function () {
    var assert = __kitTestAssert;
    function waitFor(predicate, message) {
      return new Promise(function (resolve, reject) {
        var deadline = performance.now() + 60000;
        function poll() {
          try {
            if (predicate()) { resolve(); return; }
            if (performance.now() >= deadline) { reject(new Error(message)); return; }
            setTimeout(poll, 8);
          } catch (error) { reject(error); }
        }
        poll();
      });
    }

    var exactStart = events.length;
    document.getElementById("limit-exact").click();
    await waitFor(function () {
      return events.slice(exactStart).some(function (detail) { return detail.phase === "finish"; });
    }, "the exact response-byte limit produced no terminal outcome");
    var exactFinishes = events.slice(exactStart).filter(function (detail) { return detail.phase === "finish"; });
    assert(exactFinishes.length === 1 && exactFinishes[0].outcome === "loaded",
      "the exact response-byte limit outcomes were " + exactFinishes.map(function (detail) { return detail.outcome; }).join(","));
    assert(location.pathname === "/limits/exact" &&
      document.getElementById("limit-marker").textContent.trim() === "exact",
      "the exact response-byte limit did not Morph");
    document.getElementById("limit-padding").textContent = "";

    async function expectLoaded(name, markerText, label) {
      var start = events.length;
      document.getElementById("limit-" + name).click();
      await waitFor(function () {
        return events.slice(start).some(function (detail) { return detail.phase === "finish"; });
      }, label + " produced no terminal outcome");
      var finishes = events.slice(start).filter(function (detail) { return detail.phase === "finish"; });
      assert(finishes.length === 1 && finishes[0].outcome === "loaded",
        label + " outcomes were " + finishes.map(function (detail) { return detail.outcome; }).join(","));
      assert(location.pathname === "/limits/" + name &&
        document.getElementById("limit-marker").textContent.trim() === markerText,
        label + " did not Morph");
    }

    await expectLoaded("node-exact", "node-100000", "the exact 100000-node document");
    await expectLoaded("depth-exact", "depth-256", "the exact depth-256 document");

    async function expectFallback(name, label) {
      var cookie = "kit_drive_limit_" + name.replace(/-/g, "_") + "=1";
      var start = events.length;
      var path = location.pathname + location.search + location.hash;
      var historyLength = history.length;
      var historyState = JSON.stringify(history.state);
      var root = document.documentElement;
      var body = document.body;
      var title = document.title;
      var marker = document.getElementById("limit-marker");
      var markerText = marker.textContent;
      document.getElementById("limit-" + name).click();
      await waitFor(function () { return document.cookie.indexOf(cookie) >= 0; }, label + " did not navigate natively");
      var finishes = events.slice(start).filter(function (detail) { return detail.phase === "finish"; });
      assert(finishes.length === 1 && finishes[0].outcome === "fallback",
        label + " terminal outcomes were " + finishes.map(function (detail) { return detail.outcome; }).join(","));
      assert(location.pathname + location.search + location.hash === path && history.length === historyLength,
        label + " mutated URL or history before native fallback");
      assert(JSON.stringify(history.state) === historyState,
        label + " mutated history state before native fallback");
      assert(document.documentElement === root && document.body === body && document.title === title,
        label + " replaced the document root, body, or title before native fallback");
      assert(document.getElementById("limit-marker") === marker && marker.textContent === markerText,
        label + " mutated the live document before native fallback");
    }

    await expectFallback("declared-over", "over-limit Content-Length");
    await expectFallback("unknown-over", "over-limit unknown-length stream");
    await expectFallback("encoded-over", "over-limit gzip-decoded stream");
    await expectFallback("encoded-declared-over", "over-limit gzip Content-Length");
    await expectFallback("non-html-stream", "headers-first non-HTML stream");
    await expectFallback("node-over", "over-limit DOM node count");
    await expectFallback("depth-over", "over-limit DOM depth");
    await expectFallback("setup-failure", "response stream setup failure");
    await expectFallback("decoder-failure", "TextDecoder setup failure");
    assert(unboundedTextCalls === 0, "bounded response failures escaped to unbounded response.text()");
    assert(setupCancelCalls === 1 && decoderCancelCalls === 1,
      "bounded response setup did not cancel both failed bodies");
    assert(earlyCancelCalls === 1, "early response rejection did not cancel its live body exactly once");
  });
})();`
