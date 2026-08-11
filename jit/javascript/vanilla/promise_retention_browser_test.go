package vanilla

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDetachedPromiseOwnersAreReleased guards the non-structural lifecycle:
// an externally rooted, never-settling Promise must not retain the component
// record that returned it. Hosts are removed with element.remove(), so no
// structural disposer can make this test pass accidentally.
func TestDetachedPromiseOwnersAreReleased(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS Promise retention contract in short mode")
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
		case "/promise-retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(promiseRetentionFixture))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/promise-retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("detached-Promise retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

const promiseRetentionFixture = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS detached Promise retention contract</title></head>
<body>
  <section id="promise-init-host" data-kit-component="promise-retention-init">
    <output id="promise-init-output" data-kit-text="value">server-init</output>
  </section>
  <section id="promise-action-host" data-kit-component="promise-retention-action">
    <button id="promise-action-start" type="button" data-kit-click="waitForever()">wait forever</button>
    <output id="promise-action-output" data-kit-text="value">server-action</output>
  </section>
  <section data-kit-component="promise-retention-driver">
    <button id="promise-retention-dirty" type="button" data-kit-click="tick = tick + 1">dirty survivor</button>
    <output id="promise-retention-driver-output" data-kit-text="tick">server-driver</output>
  </section>

  <script>
    globalThis.__promiseRetentionInitNever = new Promise(function () {});
    globalThis.__promiseRetentionActionNever = new Promise(function () {});
    globalThis.__promiseRetentionScopeRefs = Object.create(null);
    globalThis.__promiseRetentionActionCalls = 0;
  </script>
  <script src="/kit.js"></script>
  <script>
    kit.component("promise-retention-init", {
      value: "init-ready",
      init: function () {
        if (typeof WeakRef === "function") {
          globalThis.__promiseRetentionScopeRefs.init = new WeakRef(this);
        }
        return globalThis.__promiseRetentionInitNever;
      }
    });
    kit.component("promise-retention-action", {
      value: "action-ready",
      init: function () {
        if (typeof WeakRef === "function") {
          globalThis.__promiseRetentionScopeRefs.action = new WeakRef(this);
        }
      },
      waitForever: function () {
        globalThis.__promiseRetentionActionCalls++;
        return globalThis.__promiseRetentionActionNever;
      }
    });
    kit.component("promise-retention-driver", { tick: 0 });
  </script>
  <script>
  (function () {
    "use strict";
    var root = document.documentElement;

    function finish(status, error) {
      root.setAttribute("data-kit-retention-test", status);
      if (error) root.setAttribute("data-kit-retention-error", String(error && error.message || error));
    }
    function fail(message) { throw new Error(message); }
    function makeControls() {
      var refs = [];
      for (var index = 0; index < 128; index++) refs.push(new WeakRef({ index: index }));
      return refs;
    }
    function alive(refs) {
      var count = 0;
      refs.forEach(function (ref) { if (ref.deref() !== undefined) count++; });
      return count;
    }
    function collect(ownerRefs, controlRefs, pass) {
      var pressure = [];
      for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
      pressure = null;
      globalThis.gc();
      globalThis.gc();
      if (pass < 7) {
        setTimeout(function () { collect(ownerRefs, controlRefs, pass + 1); }, 0);
        return;
      }
      var controlAlive = alive(controlRefs);
      if (controlAlive !== 0) {
        finish("unsupported", "forced GC retained " + controlAlive + " control objects");
        return;
      }
      var retained = [];
      ownerRefs.forEach(function (entry) {
        if (entry.ref.deref() !== undefined) retained.push(entry.kind);
      });
      if (retained.length) fail("never-settling Promise retained detached " + retained.join(","));
      if (!globalThis.__promiseRetentionInitNever || !globalThis.__promiseRetentionActionNever) {
        fail("never-settling Promise roots disappeared during the proof");
      }
      finish("passed");
    }
    function waitForInitial(deadline) {
      var scopeRefs = globalThis.__promiseRetentionScopeRefs;
      if (document.getElementById("promise-init-output").textContent !== "init-ready" ||
          document.getElementById("promise-action-output").textContent !== "action-ready" ||
          document.getElementById("promise-retention-driver-output").textContent !== "0" ||
          !scopeRefs.init || !scopeRefs.action) {
        if (performance.now() >= deadline) fail("Promise retention components did not initialize");
        setTimeout(function () { waitForInitial(deadline); }, 8);
        return;
      }

      document.getElementById("promise-action-start").click();
      if (globalThis.__promiseRetentionActionCalls !== 1) {
        fail("action Promise was not observed before direct removal");
      }

      var initHost = document.getElementById("promise-init-host");
      var actionHost = document.getElementById("promise-action-host");
      var ownerRefs = [
        { kind: "init-host", ref: new WeakRef(initHost) },
        { kind: "init-scope", ref: scopeRefs.init },
        { kind: "action-host", ref: new WeakRef(actionHost) },
        { kind: "action-scope", ref: scopeRefs.action }
      ];
      initHost.remove();
      actionHost.remove();
      document.getElementById("promise-retention-dirty").click();
      initHost = null;
      actionHost = null;
      scopeRefs = null;
      setTimeout(function () { collect(ownerRefs, makeControls(), 0); }, 0);
    }
    function run() {
      try {
        if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
          finish("unsupported", "WeakRef or forced gc() is unavailable");
          return;
        }
        waitForInitial(performance.now() + 2000);
      } catch (error) { finish("failed", error); }
    }

    window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
    window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
    } else setTimeout(run, 0);
  })();
  </script>
</body>
</html>`
