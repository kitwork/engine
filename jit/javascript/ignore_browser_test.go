package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBrowserDataKitIgnoreIsInertAndMorphOpaque(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping data-kit-ignore browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "ignore-active", Version: "1.0.0"},
			{Name: "ignore-hidden", Version: "1.0.0"},
			{Name: "ignore-transition", Version: "1.0.0"},
		},
		Scripts: []Script{{Name: "ignore-contract", Source: []byte(ignoreComponentSource)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	preludeSource := []byte(ignorePreludeSource)
	contractSource := []byte(ignoreAssertions)
	scriptTags := ignoreScriptTags(
		assetPath,
		driveScriptIntegrity(preludeSource),
		driveScriptIntegrity(artifact.Bytes()),
		driveScriptIntegrity(contractSource),
	)
	var nextRequests atomic.Int32
	var finalRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/assets/ignore-prelude.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(preludeSource)
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/assets/ignore-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
		case "/ignore":
			writeIgnoreHTML(response, ignoreInitialPage(scriptTags))
		case "/ignore-next":
			nextRequests.Add(1)
			writeIgnoreHTML(response, ignoreNextPage(scriptTags))
		case "/ignore-final":
			finalRequests.Add(1)
			writeIgnoreHTML(response, ignoreFinalPage(scriptTags))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/ignore")
	if nextRequests.Load() != 1 || finalRequests.Load() != 1 {
		t.Fatalf("Drive requests next=%d final=%d, want one each", nextRequests.Load(), finalRequests.Load())
	}
}

func writeIgnoreHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}

const ignoreComponentSource = `; (function (kit) {
  "use strict";
  kit.component("ignore-active", {
    touches: 0,
    init: function () { globalThis.__ignoreProbe.activeInit++; },
    touch: function () { this.touches++; }
  });
  kit.component("ignore-hidden", {
    value: 0,
    init: function () {
      globalThis.__ignoreProbe.hiddenInit++;
      return function () { globalThis.__ignoreProbe.hiddenCleanup++; };
    }
  });
  kit.component("ignore-transition", {
    value: "active",
    init: function () {
      globalThis.__ignoreProbe.transitionInit++;
      return function () { globalThis.__ignoreProbe.transitionCleanup++; };
    }
  });
})(kit);
`

const ignorePreludeSource = `
globalThis.__ignoreProbe = {
  activeInit: 0, hiddenInit: 0, hiddenCleanup: 0,
  transitionInit: 0, transitionCleanup: 0, errors: []
};
(function () {
  var report = console.error;
  console.error = function (error) {
    globalThis.__ignoreProbe.errors.push(String(error && error.message || error));
    return report.apply(this, arguments);
  };
})();
`

func ignoreScriptTags(assetPath, preludeIntegrity, artifactIntegrity, contractIntegrity string) string {
	return fmt.Sprintf(`<script defer src="/assets/ignore-prelude.js" integrity="%s" crossorigin="anonymous"></script>
<script defer src="%s" integrity="%s" crossorigin="anonymous"></script>
<script defer src="/assets/ignore-contract.js" integrity="%s" crossorigin="anonymous"></script>`,
		preludeIntegrity, assetPath, artifactIntegrity, contractIntegrity)
}

func ignoreHead(title, scriptTags string) string {
	return fmt.Sprintf(`<head><meta charset="utf-8"><title>%s</title>%s</head>`, title, scriptTags)
}

func ignoreOpaqueZone(serverText string) string {
	return fmt.Sprintf(`<section id="ignore-zone" data-kit-ignore data-server=%q
    data-kit-component="missing-component" data-kit-version="not-semver"
    data-kit-as="$target" data-kit-retain="invalid key" data-kit-unknown="ignored">
  <button id="ignore-event" type="button" data-kit-click="count = count + 100">ignored event</button>
  <input id="ignore-model" data-kit-model="count" value="server">
  <output id="ignore-text" data-kit-text="count">%s</output>
  <section data-kit-component="ignore-hidden" data-kit-version="1.0.0"></section>
  <section data-kit-scope="not valid("><output data-kit-show="bad(">invalid scope</output></section>
  <template data-kit-scope="bad("><span data-kit-text="bad(">invalid template scope</span></template>
  <template data-kit-component="missing-component"><span>invalid template component</span></template>
  <template data-kit-if="bad("><span id="ignore-branch">invalid structure</span></template>
</section>`, serverText, serverText)
}

func ignoreActiveAlias() string {
	return `<section id="ignore-active" data-kit-component="ignore-active" data-kit-version="1.0.0" data-kit-as="$target">
  <output id="ignore-touches" data-kit-text="touches">server-active</output>
</section>
<button id="ignore-alias-call" type="button" data-kit-click="$target.touch()">active alias</button>`
}

func ignoreInitialPage(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html><html lang="en">%s<body>
<main id="ignore-route" data-kit-scope="count: 0">
  <output id="ignore-count" data-kit-text="count">server-count</output>
  %s
  %s
  <section id="ignore-transition" data-kit-component="ignore-transition" data-kit-version="1.0.0">
    <output data-kit-text="value">server-transition</output>
  </section>
  <div id="ignore-drive-marker" data-kit-drive="false">Drive marker</div>
  <a id="ignore-next" href="/ignore-next">next</a>
</main>
</body></html>`, ignoreHead("Ignore initial", scriptTags), ignoreOpaqueZone("server-initial"), ignoreActiveAlias())
}

func ignoreNextPage(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html><html lang="en">%s<body>
<main id="ignore-route" data-kit-scope="count: 0">
  <output id="ignore-count" data-kit-text="count">incoming-count</output>
  %s
  %s
  <section id="ignore-transition" data-kit-ignore data-kit-component="missing-component" data-kit-version="bad">
    <output data-kit-text="bad(">incoming ignored transition</output>
  </section>
  <div id="ignore-next-marker">next route</div>
  <a id="ignore-final" href="/ignore-final">final</a>
</main>
</body></html>`, ignoreHead("Ignore next", scriptTags), ignoreOpaqueZone("server-incoming"), ignoreActiveAlias())
}

func ignoreFinalPage(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html><html lang="en">%s<body>
<main id="ignore-route" data-kit-scope="count: 0">
  <output id="ignore-count" data-kit-text="count">final-count</output>
  %s
  %s
  <section id="ignore-transition" data-kit-component="ignore-transition" data-kit-version="1.0.0">
    <output data-kit-text="value">final transition</output>
  </section>
  <div id="ignore-final-marker">final route</div>
</main>
</body></html>`, ignoreHead("Ignore final", scriptTags), ignoreOpaqueZone("server-final"), ignoreActiveAlias())
}

const ignoreAssertions = `(function () {
  "use strict";
  var root = document.documentElement;
  var probe = globalThis.__ignoreProbe;
  function fail(message) {
    root.setAttribute("data-kit-test", "failed");
    root.setAttribute("data-kit-error", String(message));
  }
  function assert(condition, message) { if (!condition) throw new Error(message); }
  function waitFor(predicate, message, deadline, done) {
    if (predicate()) { done(); return; }
    if (performance.now() >= deadline) { fail(message); return; }
    setTimeout(function () { waitFor(predicate, message, deadline, done); }, 8);
  }
  waitFor(function () {
    return document.getElementById("ignore-count").textContent === "0" &&
      document.getElementById("ignore-touches").textContent === "0" &&
      probe.activeInit === 1 && probe.transitionInit === 1;
  }, "active fixtures did not mount", performance.now() + 2000, function () {
    try {
      var ignored = document.getElementById("ignore-zone");
      var ignoredText = document.getElementById("ignore-text");
      var ignoredInput = document.getElementById("ignore-model");
      var transition = document.getElementById("ignore-transition");
      assert(probe.hiddenInit === 0 && probe.hiddenCleanup === 0, "ignored component mounted");
      assert(probe.errors.length === 0, "ignored markup reported: " + probe.errors.join(" | "));
      document.getElementById("ignore-event").click();
      ignoredInput.value = "99";
      ignoredInput.dispatchEvent(new Event("input", { bubbles: true }));
      document.getElementById("ignore-alias-call").click();
      setTimeout(function () {
        try {
          assert(document.getElementById("ignore-count").textContent === "0", "ignored event/model changed outer state");
          assert(ignoredText.textContent === "server-initial", "ignored binding rendered");
          assert(document.getElementById("ignore-touches").textContent === "1", "ignored alias shadowed active alias");
          assert(probe.errors.length === 0, "initial ignored directives reported");
          ignored.setAttribute("data-client-state", "preserved");
          ignoredText.textContent = "client-preserved";
          ignoredInput.value = "client-preserved";
          document.getElementById("ignore-next").click();
          waitFor(function () { return location.pathname === "/ignore-next"; },
            "Drive did not reach ignored next route", performance.now() + 2000, function () {
            try {
              assert(document.getElementById("ignore-zone") === ignored, "Morph replaced paired ignored host");
              assert(ignored.getAttribute("data-server") === "server-initial", "Morph patched ignored host attributes");
              assert(ignored.getAttribute("data-client-state") === "preserved", "Morph removed ignored client state");
              assert(ignoredText.textContent === "client-preserved" && ignoredInput.value === "client-preserved",
                "Morph patched ignored subtree");
              assert(document.getElementById("ignore-transition") !== transition,
                "adding data-kit-ignore did not replace the active boundary");
              assert(probe.transitionCleanup === 1 && probe.transitionInit === 1,
                "active to ignored transition lifecycle was not exact");
              assert(probe.hiddenInit === 0 && probe.errors.length === 0,
                "incoming ignored metadata was prepared");
              document.getElementById("ignore-alias-call").click();
              document.getElementById("ignore-final").click();
              waitFor(function () { return location.pathname === "/ignore-final" && probe.transitionInit === 2; },
                "removing data-kit-ignore did not activate a fresh boundary", performance.now() + 2000, function () {
                try {
                  assert(document.getElementById("ignore-zone") === ignored,
                    "paired ignored host lost identity on the final route");
                  assert(probe.transitionCleanup === 1 && probe.hiddenInit === 0,
                    "ignored lifecycle changed on final route");
                  assert(document.getElementById("ignore-touches").textContent === "2",
                    "active alias stopped after ignored Morph");
                  assert(probe.errors.length === 0, "ignored contract reported: " + probe.errors.join(" | "));
                  root.setAttribute("data-kit-test", "passed");
                } catch (error) { fail(error.message || error); }
              });
            } catch (error) { fail(error.message || error); }
          });
        } catch (error) { fail(error.message || error); }
      }, 0);
    } catch (error) { fail(error.message || error); }
  });
  window.addEventListener("error", function (event) { fail(event.error || event.message); });
  window.addEventListener("unhandledrejection", function (event) { fail(event.reason); });
})();`
