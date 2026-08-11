package vanilla

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRemovedStructuralRowsAreReleased proves the ownership edge that a DOM
// assertion cannot: after a keyed row is removed, neither its cloned elements
// nor its nested component instance may remain strongly owned by the structural
// record. The neighboring keyed row is the live control and must survive the
// same forced collections with its exact node and component instance.
func TestRemovedStructuralRowsAreReleased(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS structural retention contract in short mode")
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
		case "/structural-retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(structuralRetentionFixture))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/structural-retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("structural-row retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

const structuralRetentionFixture = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KitJS structural retention contract</title></head>
<body>
  <main id="structural-gc-host" data-kit-component="structural-gc-host">
    <button id="structural-gc-remove" type="button" data-kit-click="removeFirst()">remove first</button>
    <template data-kit-for="item of items" data-kit-key="item.id">
      <article class="structural-gc-row" data-kit-bind="data-row-id: item.id">
        <section class="structural-gc-child" data-kit-component="structural-gc-child">
          <output data-kit-text="value">server</output>
          <button class="structural-gc-hold" type="button" data-kit-click="$sink.hold(() => value)">hold callback</button>
        </section>
      </article>
    </template>
  </main>
  <section data-kit-component="structural-gc-sink" data-kit-as="$sink"></section>

  <script>
    globalThis.__structuralGCComponentRefs = [];
    globalThis.__structuralGCNever = new Promise(function () {});
  </script>
  <script src="/kit.js"></script>
  <script>
    kit.component("structural-gc-child", {
      value: "ready",
      init: function () {
        if (typeof WeakRef === "function") {
          globalThis.__structuralGCComponentRefs.push(new WeakRef(this));
        }
        return globalThis.__structuralGCNever;
      }
    });
    kit.component("structural-gc-sink", {
      callback: null,
      hold: function (callback) { this.callback = callback; }
    });
    kit.component("structural-gc-host", {
      items: [
        { id: "remove", label: "remove" },
        { id: "retain", label: "retain" }
      ],
      removeFirst: function () {
        this.items = [{ id: "retain", label: "retain refreshed" }];
      }
    });
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
    function alive(refs) {
      var count = 0;
      refs.forEach(function (ref) { if (ref.deref() !== undefined) count++; });
      return count;
    }
    function controls() {
      var refs = [];
      for (var index = 0; index < 128; index++) refs.push(new WeakRef({ index: index }));
      return refs;
    }
    function collect(removedRefs, retainedRefs, controlRefs, pass) {
      var pressure = [];
      for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
      pressure = null;
      globalThis.gc();
      globalThis.gc();
      if (pass < 7) {
        setTimeout(function () { collect(removedRefs, retainedRefs, controlRefs, pass + 1); }, 0);
        return;
      }
      var controlAlive = alive(controlRefs);
      if (controlAlive !== 0) {
        finish("unsupported", "forced GC retained " + controlAlive + " control objects");
        return;
      }
      var removedAlive = alive(removedRefs);
      if (removedAlive !== 0) {
        var retainedKinds = [];
        ["row", "host", "scope"].forEach(function (kind, index) {
          if (removedRefs[index].deref() !== undefined) retainedKinds.push(kind);
        });
        fail("runtime retained removed " + retainedKinds.join(","));
      }
      var retainedAlive = alive(retainedRefs);
      if (retainedAlive !== retainedRefs.length) {
        fail("live keyed row retained only " + retainedAlive + " of " + retainedRefs.length + " owners");
      }
      finish("passed");
    }
    function waitForInitial(deadline) {
      var rows = document.querySelectorAll(".structural-gc-row");
      var componentRefs = globalThis.__structuralGCComponentRefs;
      if (rows.length !== 2 || componentRefs.length !== 2) {
        if (performance.now() >= deadline) fail("structural rows/components did not initialize");
        setTimeout(function () { waitForInitial(deadline); }, 8);
        return;
      }

      var removedRow = document.querySelector('.structural-gc-row[data-row-id="remove"]');
      var retainedRow = document.querySelector('.structural-gc-row[data-row-id="retain"]');
      var removedChild = removedRow && removedRow.querySelector(".structural-gc-child");
      var retainedChild = retainedRow && retainedRow.querySelector(".structural-gc-child");
      if (!removedChild || !retainedChild) fail("structural component controls are missing");

      var removedRefs = [new WeakRef(removedRow), new WeakRef(removedChild), componentRefs[0]];
      var retainedRefs = [new WeakRef(retainedRow), new WeakRef(retainedChild), componentRefs[1]];
      removedChild.querySelector(".structural-gc-hold").click();
      document.getElementById("structural-gc-remove").click();

      function afterRemove(removeDeadline) {
        if (document.querySelector('.structural-gc-row[data-row-id="remove"]')) {
          if (performance.now() >= removeDeadline) fail("keyed row was not removed");
          setTimeout(function () { afterRemove(removeDeadline); }, 8);
          return;
        }
        var liveRow = document.querySelector('.structural-gc-row[data-row-id="retain"]');
        if (liveRow !== retainedRow || liveRow.querySelector(".structural-gc-child") !== retainedChild) {
          fail("removing one keyed row replaced the retained control row");
        }
        removedRow = null;
        removedChild = null;
        retainedRow = null;
        retainedChild = null;
        rows = null;
        componentRefs = null;
        setTimeout(function () { collect(removedRefs, retainedRefs, controls(), 0); }, 0);
      }
      afterRemove(performance.now() + 2000);
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
