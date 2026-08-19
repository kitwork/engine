package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserSyncInitCleanupAcrossStructureAndMorph(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping synchronous cleanup browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "cleanup-after", Version: "1.0.0"},
			{Name: "cleanup-for-child", Version: "1.0.0"},
			{Name: "cleanup-for-owner", Version: "1.0.0"},
			{Name: "cleanup-if-child", Version: "1.0.0"},
			{Name: "cleanup-if-owner", Version: "1.0.0"},
			{Name: "cleanup-retained", Version: "1.0.0"},
			{Name: "cleanup-throw", Version: "1.0.0"},
		},
		Scripts: []Script{{Name: "cleanup-contract", Source: []byte(cleanupLifecycleComponentSource)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	preludeSource := []byte(cleanupLifecycleStatePreludeSource)
	contractSource := []byte(cleanupLifecycleBrowserContractSource)
	scriptTags := cleanupLifecycleScriptTags(
		assetPath,
		driveScriptIntegrity(preludeSource),
		driveScriptIntegrity(artifact.Bytes()),
		driveScriptIntegrity(contractSource),
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/assets/cleanup-lifecycle-prelude.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(preludeSource)
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/assets/cleanup-lifecycle-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
		case "/cleanup-contract.html":
			writeCleanupHTML(response, cleanupLifecycleInitialDocument(scriptTags))
		case "/cleanup-next":
			writeCleanupHTML(response, cleanupLifecycleNextDocument(scriptTags))
		case "/cleanup-again":
			writeCleanupHTML(response, cleanupLifecycleAgainDocument(scriptTags))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/cleanup-contract.html")
}

func writeCleanupHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

const cleanupLifecycleComponentSource = `; (function (kit) {
  "use strict";

  function tracked(name, shouldThrow) {
    return {
      init: function () {
        var scope = this;
        globalThis.__cleanupState.init[name]++;
        return function () {
          globalThis.__cleanupState.cleanup[name]++;
          if (this !== scope) globalThis.__cleanupState.wrongThis++;
          if (shouldThrow) throw new Error("cleanup boom");
        };
      }
    };
  }

  kit.component("cleanup-if-owner", {
    visible: true,
    hide: function () { this.visible = false; }
  });
  kit.component("cleanup-for-owner", {
    items: [1, 2],
    removeFirst: function () { this.items = [2]; }
  });
  kit.component("cleanup-if-child", tracked("ifChild", false));
  kit.component("cleanup-for-child", tracked("forChild", false));
  kit.component("cleanup-retained", tracked("retained", false));
  kit.component("cleanup-throw", tracked("throwing", true));
  kit.component("cleanup-after", tracked("after", false));
})(kit);
`

const cleanupLifecycleStatePreludeSource = `
  globalThis.__cleanupState = {
    init: { ifChild: 0, forChild: 0, retained: 0, throwing: 0, after: 0 },
    cleanup: { ifChild: 0, forChild: 0, retained: 0, throwing: 0, after: 0 },
    errors: 0,
    wrongThis: 0
  };
  (function () {
    var original = console.error;
    console.error = function (error) {
      if (String(error && error.message || error).indexOf("cleanup boom") >= 0) {
        globalThis.__cleanupState.errors++;
      }
      return original.apply(this, arguments);
    };
  })();
`

const cleanupLifecycleBrowserContractSource = browserHarness + "\n" + cleanupLifecycleAssertions

func cleanupLifecycleScriptTags(assetPath, preludeIntegrity, artifactIntegrity, contractIntegrity string) string {
	return fmt.Sprintf(`<script defer src="/assets/cleanup-lifecycle-prelude.js" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="%s" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/assets/cleanup-lifecycle-contract.js" integrity="%s" crossorigin="anonymous"></script>`,
		preludeIntegrity, assetPath, artifactIntegrity, contractIntegrity)
}

func cleanupLifecycleRetainedHost() string {
	return `<section id="cleanup-retained" data-kit-component="cleanup-retained" data-kit-version="1.0.0">
    <span>retained host</span>
  </section>`
}

func cleanupLifecycleDisposableHosts() string {
	return `<section id="cleanup-throw" data-kit-component="cleanup-throw" data-kit-version="1.0.0">throw cleanup</section>
  <section id="cleanup-after" data-kit-component="cleanup-after" data-kit-version="1.0.0">cleanup after throw</section>`
}

func cleanupLifecycleInitialDocument(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Cleanup initial</title>
  %s
</head>
<body>
  <main id="route-main">
    <section data-kit-component="cleanup-if-owner" data-kit-version="1.0.0">
      <button id="cleanup-remove-if" type="button" data-kit-click="hide()">remove if</button>
      <template data-kit-if="visible">
        <section id="cleanup-if-child" data-kit-component="cleanup-if-child" data-kit-version="1.0.0">if child</section>
      </template>
    </section>
    <section data-kit-component="cleanup-for-owner" data-kit-version="1.0.0">
      <button id="cleanup-remove-for" type="button" data-kit-click="removeFirst()">remove row</button>
      <template data-kit-for="item of items" data-kit-key="item">
        <section class="cleanup-for-child" data-kit-component="cleanup-for-child" data-kit-version="1.0.0">for child</section>
      </template>
    </section>
    %s
    %s
    <a id="cleanup-next" href="/cleanup-next">next</a>
  </main>
</body>
</html>`, scriptTags, cleanupLifecycleRetainedHost(), cleanupLifecycleDisposableHosts())
}

func cleanupLifecycleNextDocument(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Cleanup next</title>%s</head>
<body><main id="route-main">%s<a id="cleanup-again" href="/cleanup-again">again</a></main></body></html>`,
		scriptTags, cleanupLifecycleRetainedHost())
}

func cleanupLifecycleAgainDocument(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Cleanup again</title>%s</head>
<body><main id="route-main">%s%s<a id="cleanup-next-again" href="/cleanup-next">next again</a></main></body></html>`,
		scriptTags, cleanupLifecycleRetainedHost(), cleanupLifecycleDisposableHosts())
}

const cleanupLifecycleAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var state = globalThis.__cleanupState;

  await waitFor(function () {
    return state.init.ifChild === 1 && state.init.forChild === 2 &&
      state.init.retained === 1 && state.init.throwing === 1 && state.init.after === 1;
  }, "cleanup fixture components did not initialize exactly once");

  var retainedHost = document.getElementById("cleanup-retained");
  var retainedRow = document.querySelectorAll(".cleanup-for-child")[1];
  document.getElementById("cleanup-remove-if").click();
  await waitFor(function () {
    return !document.getElementById("cleanup-if-child") && state.cleanup.ifChild === 1;
  }, "data-kit-if did not synchronously own cleanup");
  assert(state.cleanup.ifChild === 1, "data-kit-if cleanup ran more than once");

  document.getElementById("cleanup-remove-for").click();
  await waitFor(function () {
    return document.querySelectorAll(".cleanup-for-child").length === 1 && state.cleanup.forChild === 1;
  }, "data-kit-for did not clean the removed keyed component");
  assert(document.querySelector(".cleanup-for-child") === retainedRow,
    "data-kit-for replaced its retained component host");
  assert(state.cleanup.forChild === 1, "retained keyed component was cleaned");

  document.getElementById("cleanup-next").click();
  await waitFor(function () {
    return location.pathname === "/cleanup-next" && state.cleanup.throwing === 1 && state.cleanup.after === 1;
  }, "Morph removal did not finish cleanup after a throwing cleanup");
  assert(state.errors === 1, "throwing cleanup reported " + state.errors + " errors instead of one");
  assert(state.cleanup.ifChild === 1 && state.cleanup.forChild === 2,
    "Morph did not clean the retained keyed child exactly once");
  assert(document.getElementById("cleanup-retained") === retainedHost && state.cleanup.retained === 0,
    "Morph cleaned or replaced a retained component host");
  assert(state.wrongThis === 0, "cleanup did not receive its component scope as this");

  document.getElementById("cleanup-again").click();
  await waitFor(function () {
    return location.pathname === "/cleanup-again" && state.init.throwing === 2 && state.init.after === 2;
  }, "released Morph components could not initialize again");
  assert(document.getElementById("cleanup-retained") === retainedHost && state.init.retained === 1,
    "retained component initialized again during Morph");

  document.getElementById("cleanup-next-again").click();
  await waitFor(function () {
    return location.pathname === "/cleanup-next" && state.cleanup.throwing === 2 && state.cleanup.after === 2;
  }, "second Morph removal did not release every cleanup owner");
  assert(state.errors === 2, "throwing cleanup was not reported exactly once per instance");
  assert(state.cleanup.retained === 0 && document.getElementById("cleanup-retained") === retainedHost,
    "retained host cleanup ran during repeated Morphs");
  assert(state.wrongThis === 0, "a repeated cleanup received the wrong this value");
});`

func TestBrowserCleanupObserverIsLazyWithoutOwners(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cleanup-observer browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	artifact, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Components: []ComponentVersion{{Name: "cleanup-passive", Version: "1.0.0"}},
		Scripts: []Script{{Name: "cleanup-passive", Source: []byte(`; kit.component("cleanup-passive", {
  ready: true,
  init: function () { globalThis.__cleanupPassiveInit++; }
});
`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/cleanup-passive.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Passive cleanup</title>
<script>%s globalThis.__cleanupPassiveInit = 0;</script><script src=%q></script></head>
<body><section data-kit-component="cleanup-passive" data-kit-version="1.0.0"><output data-kit-text="ready">server</output></section>
<script>%s __runStandaloneKitTest(async function () {
  await __kitTestWaitFor(function () { return globalThis.__cleanupPassiveInit === 1; }, "passive init did not run");
  __kitTestAssert(globalThis.__cleanupObserverProbe.observe === 0,
    "cleanup observer started without a cleanup owner");
  __kitTestAssert(globalThis.__cleanupObserverProbe.disconnect === 0,
    "cleanup observer disconnected despite never starting");
});</script></body></html>`, cleanupObserverProbe, assetPath, browserHarness)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/cleanup-passive.html")
}

func TestBrowserDirectRemovalCleanupMoveAndRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping direct-removal cleanup retention contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	artifact, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Components: []ComponentVersion{{Name: "cleanup-direct", Version: "1.0.0"}},
		Scripts: []Script{{Name: "cleanup-direct", Source: []byte(`; kit.component("cleanup-direct", {
  value: "ready",
  init: function () {
    var scope = this;
    globalThis.__cleanupDirect.scope = new WeakRef(scope);
    globalThis.__cleanupDirect.init++;
    return function () {
      globalThis.__cleanupDirect.cleanup++;
      if (this !== scope) globalThis.__cleanupDirect.wrongThis++;
    };
  }
});
`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/cleanup-direct.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, cleanupDirectFixture, cleanupObserverProbe, assetPath)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/cleanup-direct.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("direct-removal cleanup retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

const cleanupObserverProbe = `(function () {
  "use strict";
  var observe = MutationObserver.prototype.observe;
  var disconnect = MutationObserver.prototype.disconnect;
  globalThis.__cleanupObserverProbe = { observe: 0, disconnect: 0 };
  MutationObserver.prototype.observe = function () {
    globalThis.__cleanupObserverProbe.observe++;
    return observe.apply(this, arguments);
  };
  MutationObserver.prototype.disconnect = function () {
    globalThis.__cleanupObserverProbe.disconnect++;
    return disconnect.apply(this, arguments);
  };
})();`

const cleanupDirectFixture = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Direct cleanup retention</title>
<script>%s
globalThis.__cleanupDirect = { init: 0, cleanup: 0, wrongThis: 0, scope: null };
</script><script src=%q></script></head>
<body>
  <div id="cleanup-move-target"></div>
  <section id="cleanup-direct-control"><output>control</output></section>
  <section id="cleanup-direct" data-kit-component="cleanup-direct" data-kit-version="1.0.0">
    <output data-kit-text="value">server</output>
  </section>
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
    function collect(hostRef, scopeRef, objectRefs, domRefs, pass) {
      var pressure = [];
      for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
      pressure = null;
      globalThis.gc();
      globalThis.gc();
      if (pass < 7) {
        setTimeout(function () { collect(hostRef, scopeRef, objectRefs, domRefs, pass + 1); }, 0);
        return;
      }
      var objectAlive = alive(objectRefs);
      var hostAlive = hostRef.deref() !== undefined;
      var scopeAlive = scopeRef.deref() !== undefined;
      var domAlive = alive(domRefs);
      if (!objectAlive && !hostAlive && !scopeAlive && !domAlive) {
        finish("passed");
        return;
      }
      // WeakRef.deref() keeps a target alive through the current job. Give
      // Blink several new jobs to release DOM wrappers and ephemeron values
      // before deciding that an owner is retained.
      if (pass < 31) {
        setTimeout(function () { collect(hostRef, scopeRef, objectRefs, domRefs, pass + 1); }, 0);
        return;
      }
      if (objectAlive) {
        finish("unsupported", "forced GC retained control objects");
        return;
      }
      if (hostAlive && domAlive) {
        finish("unsupported", "forced GC retained a detached DOM control");
        return;
      }
      if (hostAlive) fail("runtime retained a directly removed component host");
      if (scopeAlive) fail("runtime retained a directly removed component scope");
      finish("unsupported", "forced GC retained a detached DOM control");
    }
    function waitFor(predicate, message, deadline, done) {
      if (predicate()) { done(); return; }
      if (performance.now() >= deadline) { finish("failed", message); return; }
      setTimeout(function () { waitFor(predicate, message, deadline, done); }, 8);
    }
    function run() {
      try {
        if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
          finish("unsupported", "WeakRef or forced gc() is unavailable");
          return;
        }
        waitFor(function () { return globalThis.__cleanupDirect.init === 1; },
          "cleanup owner did not initialize", performance.now() + 2000, function () {
          try {
            var host = document.getElementById("cleanup-direct");
            var hostRef = new WeakRef(host);
            var scopeRef = globalThis.__cleanupDirect.scope;
            if (globalThis.__cleanupObserverProbe.observe !== 1) {
              fail("cleanup observer started " + globalThis.__cleanupObserverProbe.observe + " times");
            }
            var domControl = document.getElementById("cleanup-direct-control");
            var domControlChild = domControl.firstElementChild;
            var domRefs = [new WeakRef(domControl), new WeakRef(domControlChild)];
            document.getElementById("cleanup-move-target").appendChild(host);
            document.getElementById("cleanup-move-target").appendChild(domControl);
            setTimeout(function () {
              try {
                if (globalThis.__cleanupDirect.cleanup !== 0) fail("same-document move ran cleanup");
                if (!host.isConnected || !domControl.isConnected) fail("same-document move disconnected a node");
                host.remove();
                host = null;
                domControl.remove();
                domControl = null;
                domControlChild = null;
                waitFor(function () { return globalThis.__cleanupDirect.cleanup === 1; },
                  "direct element.remove() did not run cleanup", performance.now() + 2000, function () {
                  try {
                    if (globalThis.__cleanupDirect.wrongThis !== 0) fail("direct cleanup received the wrong this");
                    if (globalThis.__cleanupObserverProbe.disconnect !== 1) {
                      fail("cleanup observer disconnect count was " + globalThis.__cleanupObserverProbe.disconnect);
                    }
                    globalThis.__cleanupDirect.scope = null;
                    setTimeout(function () { collect(hostRef, scopeRef, controls(), domRefs, 0); }, 0);
                  } catch (error) { finish("failed", error); }
                });
              } catch (error) { finish("failed", error); }
            }, 0);
          } catch (error) { finish("failed", error); }
        });
      } catch (error) { finish("failed", error); }
    }
    window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
    window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
    } else setTimeout(run, 0);
  })();
  </script>
</body></html>`
