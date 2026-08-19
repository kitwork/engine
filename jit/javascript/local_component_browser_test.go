package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStagedLocalComponentBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged local component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	managedSource := []byte(`; globalThis.__localCore = document[Symbol.for("kitjs:assembly")];` + "\n" +
		`kit.component("managed", { value: "managed" });` + "\n")
	assembly, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentPackage{{
			Name:    "managed",
			Version: "1.0.0",
			Source:  managedSource,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentPackage{
			{Name: "managed", Version: "1.0.0", Source: managedSource},
			{Name: "test", Version: "1.0.0", Source: []byte(`; kit.component("test", { count: 99 });` + "\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := make(map[string][]byte)
	for _, candidate := range []StagedAssembly{assembly, target} {
		for _, artifact := range candidate.Artifacts() {
			assets["/jit/"+artifact.Name()] = artifact.Bytes()
		}
	}
	page := stagedLocalComponentDocument(assembly, target)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		case "/local.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(stagedLocalRegistrationScript))
		case "/assert.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(stagedLocalAssertionScript))
		default:
			if source, exists := assets[request.URL.Path]; exists {
				response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				_, _ = response.Write(source)
				return
			}
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/")
}

func TestStandaloneUnmarkedCustomComponentBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone custom component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	kitJS, err := SourceForProfile(ProfileKit)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(standaloneCustomComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/")
}

func stagedLocalComponentDocument(assembly, target StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html><head><meta charset="utf-8"><title>Staged local component</title>
<script>
globalThis.__localConsoleErrors = [];
globalThis.__originalConsoleError = console.error;
console.error = function (error) {
  globalThis.__localConsoleErrors.push(String(error && error.message || error));
  globalThis.__originalConsoleError.apply(console, arguments);
};
try { globalThis.kit.component("too-early", {}); }
catch (error) { globalThis.__preRuntimeError = error; }
globalThis.__localTargetGraph = ` + stagedArtifactLiteral(target.Graph) + `;
</script>
` + tags.String() + `<script src="/local.js" defer></script>
<script src="/assert.js" defer></script>
</head><body>
<section id="managed" data-kit-component="managed@1.0.0">
  <output id="managed-output" data-kit-text="value">server</output>
</section>
<section id="local" data-kit-component="test" data-kit-as="$test"
  data-kit-scope="{ count: 2 }">
  <button id="local-increment" type="button" data-kit-click="$test.increment()">Increment</button>
  <output id="local-output" data-kit-text="count">server</output>
</section>
<section id="missing" data-kit-component="missing" data-kit-local>
  <output id="missing-output" data-kit-text="value">missing-ssr</output>
</section>
<section id="unknown" data-kit-component="unknown@1.0.0">
  <output id="unknown-output" data-kit-text="value">unknown-ssr</output>
</section>
<section id="managed-shadow" data-kit-component="managed" data-kit-local>
  <output id="managed-shadow-output" data-kit-text="value">shadow-ssr</output>
</section>
<section id="versioned-local" data-kit-component="versioned-local" data-kit-local data-kit-version="1.0.0">
  <output id="versioned-local-output" data-kit-text="value">versioned-ssr</output>
</section>
</body></html>`
}

const stagedLocalRegistrationScript = `"use strict";
document.addEventListener("DOMContentLoaded", function () {
  globalThis.__localBeforeRegistration = document.getElementById("local-output").textContent.trim();
  kit.component("test", {
    count: 0,
    init: function () { globalThis.__localInitCount = (globalThis.__localInitCount || 0) + 1; },
    increment: function () { this.count++; }
  });
  try { kit.component("test", {}); }
  catch (error) { globalThis.__localDuplicateError = error; }
  try { kit.component("managed", {}); }
  catch (error) { globalThis.__managedConflictError = error; }
  for (var index = 0; index < 255; index++) {
    kit.component("local-slot-" + index, {});
  }
  try { kit.component("local-slot-overflow", {}); }
  catch (error) { globalThis.__localLimitError = error; }
}, { once: true });
`

const stagedLocalAssertionScript = `"use strict";
document.addEventListener("DOMContentLoaded", function () {
  var root = document.documentElement;
  function fail(error) {
    root.setAttribute("data-kit-test", "failed");
    root.setAttribute("data-kit-test-error", String(error && error.message || error));
  }
  function assert(value, message) { if (!value) throw new Error(message); }
  function includes(message) {
    return globalThis.__localConsoleErrors.some(function (entry) { return entry.indexOf(message) >= 0; });
  }
  function waitFor(predicate, message) {
    return new Promise(function (resolve, reject) {
      var deadline = performance.now() + 2000;
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
  function testHandoffConflict() {
    return new Promise(function (resolve, reject) {
      var HANDOFF = Symbol.for("kitjs:handoff");
      var asset = globalThis.__localTargetGraph;
      var bridge = Object.freeze({
        graph: function (_, graph, delivery) {
          try { globalThis.__localCore.beginComponentHandoff(graph, delivery); }
          catch (error) { globalThis.__localHandoffConflict = error; }
        }
      });
      Object.defineProperty(document, HANDOFF, { value: bridge, configurable: true });
      var script = document.createElement("script");
      script.setAttribute("data-kitwork-jit", "graph");
      script.setAttribute("data-kitwork-hash", asset.hash);
      script.setAttribute("data-kitwork-handoff", "");
      script.setAttribute("integrity", asset.integrity);
      script.setAttribute("crossorigin", "anonymous");
      script.defer = true;
      script.async = false;
      script.onload = function () {
        delete document[HANDOFF];
        script.remove();
        resolve();
      };
      script.onerror = function () {
        delete document[HANDOFF];
        reject(new Error("target graph did not load"));
      };
      script.src = "/jit/" + asset.name;
      document.head.appendChild(script);
    });
  }
  Promise.resolve().then(function () {
    assert(globalThis.__preRuntimeError instanceof TypeError,
      "pre-runtime inline component registration unexpectedly succeeded");
    assert(globalThis.__localBeforeRegistration === "server",
      "local host did not remain SSR-visible before DCL registration");
    assert(globalThis.__localDuplicateError && /already exists/.test(globalThis.__localDuplicateError.message),
      "duplicate local definition was not rejected");
    assert(globalThis.__managedConflictError && /already exists/.test(globalThis.__managedConflictError.message),
      "local registration was allowed to replace a managed component");
    assert(globalThis.__localLimitError && /client component registry limit exceeded/.test(globalThis.__localLimitError.message),
      "client component definitions were not bounded");
    return waitFor(function () {
      return document.getElementById("managed-output").textContent.trim() === "managed" &&
        document.getElementById("local-output").textContent.trim() === "2" &&
        globalThis.__localInitCount === 1;
    }, "registered local component did not mount with its authored scope seed");
  }).then(function () {
    document.getElementById("local-increment").click();
    return waitFor(function () {
      return document.getElementById("local-output").textContent.trim() === "3";
    }, "local component alias did not resolve after staged boot");
  }).then(function () {
    return waitFor(function () {
      return includes('client component "missing" has no registered definition');
    }, "unregistered client component did not report after DCL");
  }).then(function () {
    assert(!includes('client component "test" has no registered definition'),
      "DCL-registered client component was reported missing prematurely");
    assert(includes('component "unknown" is not present in the installed graph'),
      "unknown unmarked component did not remain graph-strict");
    assert(includes('client component "managed" conflicts with the installed graph'),
      "client host was allowed to shadow a managed component");
    assert(includes("data-kit-local components cannot use data-kit-version"),
      "versioned local component was not rejected");
    assert(document.getElementById("missing-output").textContent.trim() === "missing-ssr" &&
      document.getElementById("unknown-output").textContent.trim() === "unknown-ssr" &&
      document.getElementById("managed-shadow-output").textContent.trim() === "shadow-ssr" &&
      document.getElementById("versioned-local-output").textContent.trim() === "versioned-ssr",
      "invalid or unresolved component mutated SSR content");
    return testHandoffConflict();
  }).then(function () {
    assert(globalThis.__localHandoffConflict &&
      /conflicts with client component/.test(globalThis.__localHandoffConflict.message),
      "component handoff was allowed to replace a registered client component");
    console.error = globalThis.__originalConsoleError;
    root.setAttribute("data-kit-test", "passed");
  }).catch(fail);
}, { once: true });
`

const standaloneCustomComponentDocument = `<!doctype html><html><head><meta charset="utf-8">
<title>Standalone custom component</title>
<script>
globalThis.__standaloneComponentErrors = [];
globalThis.__standaloneOriginalConsoleError = console.error;
console.error = function (error) {
  globalThis.__standaloneComponentErrors.push(String(error && error.message || error));
  globalThis.__standaloneOriginalConsoleError.apply(console, arguments);
};
</script></head><body>
<section data-kit-component="plain" data-kit-as="$plain" data-kit-scope="{ count: 4 }">
  <button id="plain-increment" type="button" data-kit-click="$plain.increment()">Increment</button>
  <output id="plain-output" data-kit-text="count">server</output>
</section>
<section id="standalone-missing" data-kit-component="missing-standalone">missing-ssr</section>
<script src="/kit.js"></script>
<script>
kit.component("plain", { count: 0, increment: function () { this.count++; } });
document.addEventListener("DOMContentLoaded", function () {
  var root = document.documentElement;
  function finish(status, error) {
    console.error = globalThis.__standaloneOriginalConsoleError;
    root.setAttribute("data-kit-test", status);
    if (error) root.setAttribute("data-kit-test-error", String(error && error.message || error));
  }
  var deadline = performance.now() + 2000;
  function waitMissing() {
    try {
      var count = globalThis.__standaloneComponentErrors.filter(function (message) {
        return message.indexOf('client component "missing-standalone" has no registered definition') >= 0;
      }).length;
      if (count === 0 && performance.now() < deadline) {
        setTimeout(waitMissing, 8);
        return;
      }
      if (count !== 1) throw new Error("unmarked missing client definition report count was " + count);
      if (document.getElementById("standalone-missing").textContent.trim() !== "missing-ssr") {
        throw new Error("missing client component mutated SSR content");
      }
      finish("passed");
    } catch (error) { finish("failed", error); }
  }
  function wait() {
    if (document.getElementById("plain-output").textContent.trim() !== "4") {
      setTimeout(wait, 8);
      return;
    }
    document.getElementById("plain-increment").click();
    setTimeout(function () {
      try {
        if (document.getElementById("plain-output").textContent.trim() !== "5") {
          throw new Error("standalone unmarked custom component did not update");
        }
        waitMissing();
      } catch (error) { finish("failed", error); }
    }, 0);
  }
  wait();
}, { once: true });
</script>
</body></html>`
