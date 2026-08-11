package vanilla

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

// TestDetachedDOMIsReleased is deliberately a browser-level contract. Static
// source checks cannot distinguish a WeakMap from an accidental strong owner,
// and heap-size assertions are too noisy to serve as a regression gate.
//
// The fixture therefore proves two independent properties:
//  1. a detached binding is not visited by a later dirty render; and
//  2. after an explicit major collection, neither its boundary nor its bound
//     child remains reachable.
//
// The control WeakRefs keep the test honest: if this browser ignores forced GC,
// the test reports that the capability is unavailable instead of blaming KitJS.
func TestDetachedDOMIsReleased(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS retention contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS := readVanillaFile(t, "kit.js")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(retentionFixture))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("detached-DOM retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

const retentionFixture = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>KitJS detached DOM retention contract</title>
  <script>
    (function () {
      "use strict";
      var descriptor = Object.getOwnPropertyDescriptor(Node.prototype, "textContent");
      globalThis.__retentionProbe = { value: "before", writes: 0 };
      customElements.define("kit-retention-probe", class extends HTMLElement {
        get textContent() { return descriptor.get.call(this); }
        set textContent(value) {
          globalThis.__retentionProbe.writes++;
          descriptor.set.call(this, value);
        }
      });
    })();
  </script>
</head>
<body>
  <div id="retention-host"></div>
  <script>
    (function () {
      "use strict";
      var fragment = document.createDocumentFragment();
      for (var index = 0; index < 64; index++) {
        var boundary = document.createElement("section");
        boundary.setAttribute("data-retention-boundary", "");
        boundary.setAttribute("data-kit-component", "retention-probe");
        var output = document.createElement("kit-retention-probe");
        output.setAttribute("data-kit-text", "value");
        boundary.appendChild(output);
        fragment.appendChild(boundary);
      }
      document.getElementById("retention-host").appendChild(fragment);
    })();
  </script>

  <section data-kit-component="retention-driver">
    <button id="retention-dirty" type="button" data-kit-click="tick = tick + 1">dirty</button>
    <output data-kit-text="tick"></output>
  </section>

  <script src="/kit.js"></script>
  <script>
    kit.component("retention-probe", {
      get value() { return globalThis.__retentionProbe.value; }
    });
    kit.component("retention-driver", { tick: 0 });
  </script>
  <script>
    (function () {
      "use strict";
      var root = document.documentElement;
      var probe = globalThis.__retentionProbe;

      function finish(status, error) {
        root.setAttribute("data-kit-retention-test", status);
        if (error) root.setAttribute("data-kit-retention-error", String(error && error.message || error));
      }
      function fail(message) { throw new Error(message); }
      function makeControlRefs() {
        var refs = [];
        for (var index = 0; index < 128; index++) refs.push(new WeakRef({ index: index }));
        return refs;
      }
      function detachAll() {
        var refs = [];
        var boundaries = Array.from(document.querySelectorAll("[data-retention-boundary]"));
        boundaries.forEach(function (boundary) {
          refs.push(new WeakRef(boundary));
          refs.push(new WeakRef(boundary.firstElementChild));
          boundary.remove();
        });
        return refs;
      }
      function alive(refs) {
        var count = 0;
        refs.forEach(function (ref) { if (ref.deref() !== undefined) count++; });
        return count;
      }
      function collect(domRefs, controlRefs, pass) {
        // Allocation pressure makes embedders that expose a conservative gc()
        // perform a full collection without using unstable heap-size thresholds.
        var pressure = [];
        for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
        pressure = null;
        globalThis.gc();
        globalThis.gc();
        if (pass < 7) {
          setTimeout(function () { collect(domRefs, controlRefs, pass + 1); }, 0);
          return;
        }
        var controlAlive = alive(controlRefs);
        if (controlAlive !== 0) {
          finish("unsupported", "forced GC retained " + controlAlive + " control objects");
          return;
        }
        var domAlive = alive(domRefs);
        if (domAlive !== 0) fail("runtime retained " + domAlive + " detached DOM nodes");
        finish("passed");
      }
      function run() {
        try {
          if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
            finish("unsupported", "WeakRef or forced gc() is unavailable");
            return;
          }
          if (probe.writes !== 64) fail("initial render wrote " + probe.writes + " probe nodes instead of 64");
          var initialWrites = probe.writes;
          var domRefs = detachAll();
          var controlRefs = makeControlRefs();
          probe.value = "after";
          document.getElementById("retention-dirty").click();
          setTimeout(function () {
            try {
              if (probe.writes !== initialWrites) {
                fail("a dirty render revisited " + (probe.writes - initialWrites) + " detached bindings");
              }
              setTimeout(function () { collect(domRefs, controlRefs, 0); }, 0);
            } catch (error) { finish("failed", error); }
          }, 0);
        } catch (error) { finish("failed", error); }
      }

      window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
      window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
      if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
      else setTimeout(run, 0);
    })();
  </script>
</body>
</html>`

func runRetentionBrowser(t *testing.T, browser, target string) (string, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		"--js-flags=--expose-gc",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + t.TempDir(),
		"--virtual-time-budget=10000",
		"--dump-dom",
		target,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-kit-retention-test="passed"`)) {
		return "passed", output
	}
	if bytes.Contains(output, []byte(`data-kit-retention-test="unsupported"`)) {
		return "unsupported", output
	}
	if ctx.Err() != nil {
		t.Fatalf("headless retention proof timed out: %v\n%s", ctx.Err(), boundedVanillaOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless retention proof failed to run: %v\n%s", runErr, boundedVanillaOutput(output))
	}
	return "failed", output
}
