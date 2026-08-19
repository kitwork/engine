package javascript

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBrowserStandaloneDriveStableScriptTopology(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone Drive stable-script browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	hydrateJS, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}

	crossOriginSource := []byte(`globalThis.__stableCrossOriginRan = true;`)
	var crossOriginLoads atomic.Int64
	crossOrigin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		crossOriginLoads.Add(1)
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = response.Write(crossOriginSource)
	}))
	defer crossOrigin.Close()

	profileTag := `<script defer src="/stable-hydrate.js?profile=hydrate" data-kit-drive="stable"></script>`
	bundleTag := `<script defer src="/stable-components.js?bundle=shared" data-kit-drive="stable" data-channel="client"></script>`
	exactScripts := profileTag + "\n  " + bundleTag

	poison := func(name, scripts, bodyScripts string) string {
		return stableDriveDocument("Stable "+name, "Poison "+name, name, scripts, bodyScripts, true)
	}
	documents := map[string]string{
		"shared": stableDriveDocument("Stable Shared", "Shared", "shared", exactScripts, "", true),
		"url": poison("url",
			strings.Replace(exactScripts, `/stable-components.js?bundle=shared`, `/stable-components-v2.js?bundle=shared`, 1), ""),
		"query": poison("query",
			strings.Replace(exactScripts, `?bundle=shared`, `?bundle=route`, 1), ""),
		"attribute": poison("attribute",
			strings.Replace(exactScripts, `data-channel="client"`, `data-channel="client" data-release="2"`, 1), ""),
		"order":   poison("order", bundleTag+"\n  "+profileTag, ""),
		"added":   poison("added", exactScripts+"\n  "+`<script defer src="/stable-extra.js" data-kit-drive="stable"></script>`, ""),
		"removed": poison("removed", profileTag, ""),
		"cross-origin": poison("cross-origin",
			strings.Replace(
				strings.Replace(exactScripts, `/stable-components.js?bundle=shared`, crossOrigin.URL+`/stable-components.js?bundle=shared`, 1),
				`data-channel="client"`, `data-channel="client" integrity="`+driveScriptIntegrity(crossOriginSource)+`"`, 1,
			), ""),
		"inline": poison("inline", exactScripts+"\n  "+`<script defer data-kit-drive="stable">sessionStorage.setItem("stableFetchedInline", "ran");</script>`, ""),
		"body":   poison("body", profileTag, bundleTag),
		"module": poison("module",
			strings.Replace(exactScripts, `<script defer src="/stable-components.js`, `<script type="module" defer src="/stable-components.js`, 1), ""),
		"malformed-integrity": poison("malformed-integrity",
			strings.Replace(exactScripts, `data-channel="client"`, `data-channel="client" integrity="sha256-A"`, 1), ""),
		"false": poison("false",
			strings.Replace(exactScripts, `data-kit-drive="stable" data-channel="client"`, `data-kit-drive="false" data-channel="client"`, 1), ""),
		"unknown": poison("unknown",
			strings.Replace(exactScripts, `data-kit-drive="stable" data-channel="client"`, `data-kit-drive="sometimes" data-channel="client"`, 1), ""),
	}

	var hydrateLoads atomic.Int64
	var bundleLoads atomic.Int64
	var unexpectedScriptLoads atomic.Int64
	var requestMu sync.Mutex
	driveRequests := make(map[string]int)
	nativeRequests := make(map[string]int)
	countRequest := func(name string, drive bool) {
		requestMu.Lock()
		defer requestMu.Unlock()
		if drive {
			driveRequests[name]++
		} else {
			nativeRequests[name]++
		}
	}
	requestCounts := func(name string) (int, int) {
		requestMu.Lock()
		defer requestMu.Unlock()
		return driveRequests[name], nativeRequests[name]
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/stable-hydrate.js":
			hydrateLoads.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
			return
		case "/stable-components.js":
			bundleLoads.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(stableDriveComponentBundleSource))
			return
		case "/stable-components-v2.js", "/stable-extra.js":
			unexpectedScriptLoads.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(`globalThis.__stableUnexpectedScriptRan = true;`))
			return
		case "/stable/start":
			writeHydrateHTML(response, stableDriveDocument("Stable Start", "Start", "start", exactScripts, "", false))
			return
		}

		name := strings.TrimPrefix(request.URL.Path, "/stable/")
		if request.URL.Path == "/stable/disabled" {
			drive := request.Header.Get("X-KitJS-Drive") == "1"
			countRequest("disabled", drive)
			if drive {
				writeHydrateHTML(response, documents["shared"])
				return
			}
			response.Header().Set("Set-Cookie", "kit_stable_disabled=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		document, exists := documents[name]
		if !exists {
			http.NotFound(response, request)
			return
		}
		drive := request.Header.Get("X-KitJS-Drive") == "1"
		countRequest(name, drive)
		if !drive {
			response.Header().Set("Set-Cookie", "kit_stable_"+stableDriveCookieName(name)+"=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		writeHydrateHTML(response, document)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/stable/start")

	if got := hydrateLoads.Load(); got != 1 {
		t.Fatalf("self-hosted unsigned Hydrate profile loads = %d, want one", got)
	}
	if got := bundleLoads.Load(); got != 1 {
		t.Fatalf("stable shared component bundle loads = %d, want one", got)
	}
	if got := unexpectedScriptLoads.Load(); got != 0 {
		t.Fatalf("fetched incompatible same-origin scripts loaded %d times, want zero", got)
	}
	if got := crossOriginLoads.Load(); got != 0 {
		t.Fatalf("fetched cross-origin stable scripts loaded %d times, want zero", got)
	}
	if drive, native := requestCounts("shared"); drive != 1 || native != 0 {
		t.Fatalf("exact stable topology requests = Drive %d, native %d; want Drive 1, native 0", drive, native)
	}
	for _, name := range stableDriveMismatchNames {
		if drive, native := requestCounts(name); drive != 1 || native != 1 {
			t.Fatalf("%s mismatch requests = Drive %d, native %d; want Drive 1, native 1", name, drive, native)
		}
	}
	if drive, native := requestCounts("disabled"); drive != 0 || native != 1 {
		t.Fatalf("ancestor-disabled stable link requests = Drive %d, native %d; want Drive 0, native 1", drive, native)
	}
}

func TestBrowserStandaloneDriveInvalidInitialScriptTopologyDoesNotIntercept(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping invalid initial standalone Drive topology browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	hydrateJS, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}
	prelude := []byte(stableDriveInvalidPreludeSource)
	contract := []byte(stableDriveInvalidContractSource)
	document := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Invalid initial stable topology</title>
  <script defer src="/invalid-prelude.js" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/invalid-hydrate.js"></script>
  <script defer src="/invalid-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  <main id="invalid-initial-route">Initial</main>
  <a id="invalid-initial-link" href="/invalid-initial-target">Target</a>
</body>
</html>`, driveScriptIntegrity(prelude), driveScriptIntegrity(contract))

	var driveRequests atomic.Int64
	var nativeRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/invalid-prelude.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(prelude)
		case "/invalid-hydrate.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
		case "/invalid-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contract)
		case "/invalid-initial":
			writeHydrateHTML(response, document)
		case "/invalid-initial-target":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				driveRequests.Add(1)
				writeHydrateHTML(response, document)
				return
			}
			nativeRequests.Add(1)
			response.Header().Set("Set-Cookie", "kit_stable_invalid_initial=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/invalid-initial")
	if got := driveRequests.Load(); got != 0 {
		t.Fatalf("invalid initial topology issued %d Drive fetches, want zero", got)
	}
	if got := nativeRequests.Load(); got != 1 {
		t.Fatalf("invalid initial topology issued %d native requests, want one", got)
	}
}

func TestBrowserStagedDriveKeepsAuthoredStableScriptOutsideManagedLane(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged Drive authored stable-script browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	base := ComponentPackage{
		Name: "staged-stable-base", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedAuthoredStable.baseRuns++;
kit.component("staged-stable-base", { count: 0 });
`),
	}
	extra := ComponentPackage{
		Name: "staged-stable-extra", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedAuthoredStable.extraRuns++;
kit.component("staged-stable-extra", {
  ready: "extra-ready",
  init: function () { globalThis.__stagedAuthoredStable.extraInit++; }
});
`),
	}
	initial, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base, extra},
	})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Runtime.SHA256() != target.Runtime.SHA256() || initial.Hydrate == nil ||
		target.Hydrate == nil || initial.Hydrate.SHA256() != target.Hydrate.SHA256() {
		t.Fatal("staged stable fixture does not keep runtime and Hydrate sealed across graph handoff")
	}

	assets := make(map[string][]byte)
	for _, assembly := range []StagedAssembly{initial, target} {
		for _, artifact := range assembly.Artifacts() {
			assets["/jit/"+artifact.Name()] = artifact.Bytes()
		}
	}
	initialDocument := stagedAuthoredStableDocument(initial, "Staged stable start", "start", false)
	targetDocument := stagedAuthoredStableDocument(target, "Staged stable next", "next", true)
	managedStableDocument := strings.Replace(targetDocument,
		`<script data-kitwork-jit="runtime"`,
		`<script data-kitwork-jit="runtime" data-kit-drive="stable"`, 1)
	managedDowngradeDocument := strings.Replace(targetDocument,
		` integrity="`+target.Runtime.Integrity()+`"`,
		` data-kit-drive="stable"`, 1)

	var authoredStableLoads atomic.Int64
	var nextDrive atomic.Int64
	var nextNative atomic.Int64
	var managedStableDrive atomic.Int64
	var managedStableNative atomic.Int64
	var managedDowngradeDrive atomic.Int64
	var managedDowngradeNative atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			response.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = response.Write(source)
			return
		}
		drive := request.Header.Get("X-KitJS-Drive") == "1"
		switch request.URL.Path {
		case "/staged-authored-stable.js":
			authoredStableLoads.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(stagedAuthoredStableSource))
		case "/staged-stable/start":
			writeStagedDriveHTML(response, initialDocument)
		case "/staged-stable/next":
			if drive {
				nextDrive.Add(1)
				writeStagedDriveHTML(response, targetDocument)
				return
			}
			nextNative.Add(1)
			response.Header().Set("Set-Cookie", "kit_staged_stable_next_native=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/staged-stable/managed-stable":
			if drive {
				managedStableDrive.Add(1)
				writeStagedDriveHTML(response, managedStableDocument)
				return
			}
			managedStableNative.Add(1)
			response.Header().Set("Set-Cookie", "kit_staged_managed_stable=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/staged-stable/managed-downgrade":
			if drive {
				managedDowngradeDrive.Add(1)
				writeStagedDriveHTML(response, managedDowngradeDocument)
				return
			}
			managedDowngradeNative.Add(1)
			response.Header().Set("Set-Cookie", "kit_staged_managed_downgrade=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/staged-stable/start")
	if got := authoredStableLoads.Load(); got != 1 {
		t.Fatalf("ordinary authored stable script loads = %d, want one", got)
	}
	if drive, native := nextDrive.Load(), nextNative.Load(); drive != 1 || native != 0 {
		t.Fatalf("staged graph handoff requests = Drive %d, native %d; want Drive 1, native 0", drive, native)
	}
	if drive, native := managedStableDrive.Load(), managedStableNative.Load(); drive != 1 || native != 1 {
		t.Fatalf("managed stable-marker requests = Drive %d, native %d; want Drive 1, native 1", drive, native)
	}
	if drive, native := managedDowngradeDrive.Load(), managedDowngradeNative.Load(); drive != 1 || native != 1 {
		t.Fatalf("managed integrity-downgrade requests = Drive %d, native %d; want Drive 1, native 1", drive, native)
	}
}

var stableDriveMismatchNames = []string{
	"url",
	"query",
	"attribute",
	"order",
	"added",
	"removed",
	"cross-origin",
	"inline",
	"body",
	"module",
	"malformed-integrity",
	"false",
	"unknown",
}

func stableDriveCookieName(name string) string {
	return strings.NewReplacer("-", "_").Replace(name)
}

func stableDriveDocument(title, route, routeKey, scripts, bodyScripts string, includeNext bool) string {
	next := ""
	if includeNext {
		next = `<section id="stable-next-only" data-kit-component="stable-next">Next-only client component</section>`
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="description" content="%s description">
  <title>%s</title>
  %s
</head>
<body>
  <nav>
    <a id="stable-shared-link" href="/stable/shared">Shared</a>
    <a id="stable-url-link" href="/stable/url">URL</a>
    <a id="stable-query-link" href="/stable/query">Query</a>
    <a id="stable-attribute-link" href="/stable/attribute">Attribute</a>
    <a id="stable-order-link" href="/stable/order">Order</a>
    <a id="stable-added-link" href="/stable/added">Added</a>
    <a id="stable-removed-link" href="/stable/removed">Removed</a>
    <a id="stable-cross-origin-link" href="/stable/cross-origin">Cross origin</a>
    <a id="stable-inline-link" href="/stable/inline">Inline</a>
    <a id="stable-body-link" href="/stable/body">Body</a>
    <a id="stable-module-link" href="/stable/module">Module</a>
    <a id="stable-malformed-integrity-link" href="/stable/malformed-integrity">Malformed integrity</a>
    <a id="stable-false-link" href="/stable/false">False</a>
    <a id="stable-unknown-link" href="/stable/unknown">Unknown</a>
    <span data-kit-drive="false"><a id="stable-disabled-link" href="/stable/disabled" data-kit-drive="stable">Disabled ancestor</a></span>
  </nav>
  <section id="stable-retained" data-kit-component="stable-retained">Retained client component</section>
  <main id="stable-route-%s" data-kit-component="stable-route"><span id="stable-route-label">%s</span></main>
  %s
  %s
</body>
</html>`, title, title, scripts, routeKey, route, next, bodyScripts)
}

func stagedAuthoredStableDocument(assembly StagedAssembly, title, route string, includeExtra bool) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>` +
		html.EscapeString(title) + `</title>`)
	output.WriteString(`<script defer src="/staged-authored-stable.js?shared=1" data-kit-drive="stable"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` +
			html.EscapeString(artifact.Name()) + `" integrity="` +
			html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>`)
	}
	output.WriteString(`</head><body>
<nav>
  <a id="staged-stable-next-link" href="/staged-stable/next">Next graph</a>
  <a id="staged-managed-stable-link" href="/staged-stable/managed-stable">Managed stable marker</a>
  <a id="staged-managed-downgrade-link" href="/staged-stable/managed-downgrade">Managed integrity downgrade</a>
</nav>
<main id="staged-stable-route">` + html.EscapeString(route) + `</main>
<section id="staged-stable-base" data-kit-component="staged-stable-base" data-kit-version="1.0.0">
  <button id="staged-stable-add" type="button" data-kit-click="count = count + 1">Add</button>
  <output id="staged-stable-value" data-kit-text="count">0</output>
</section>`)
	if includeExtra {
		output.WriteString(`<section id="staged-stable-extra" data-kit-component="staged-stable-extra" data-kit-version="1.0.0">
  <output id="staged-stable-extra-value" data-kit-text="ready">server</output>
</section>`)
	}
	output.WriteString(`</body></html>`)
	return output.String()
}

const stableDriveComponentBundleSource = `(function (global) {
  "use strict";
  var state = global.__stableDriveState = {
    bundleRuns: (global.__stableDriveState && global.__stableDriveState.bundleRuns || 0) + 1,
    retainedInit: 0,
    retainedCleanup: 0,
    routeInit: 0,
    routeCleanup: 0,
    nextInit: 0,
    nextCleanup: 0
  };
  function tracked(init, cleanup) {
    return {
      init: function () {
        state[init]++;
        return function () { state[cleanup]++; };
      }
    };
  }
  kit.component("stable-retained", tracked("retainedInit", "retainedCleanup"));
  kit.component("stable-route", tracked("routeInit", "routeCleanup"));
  kit.component("stable-next", tracked("nextInit", "nextCleanup"));
})(globalThis);
` + browserHarness + `
` + stableDriveAssertions

const stableDriveAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var state = globalThis.__stableDriveState;

  await waitFor(function () {
    return state.retainedInit === 1 && state.routeInit === 1;
  }, "initial client components did not initialize");
  assert(state.bundleRuns === 1, "stable shared component bundle did not run exactly once initially");
  assert(state.nextInit === 0, "next-route-only component initialized on the initial route");

  var firstKit = globalThis.kit;
  var firstHTML = document.documentElement;
  var retained = document.getElementById("stable-retained");
  var oldRoute = document.getElementById("stable-route-start");
  var historyLength = history.length;
  document.getElementById("stable-shared-link").click();
  await waitFor(function () {
    return location.pathname === "/stable/shared" &&
      document.getElementById("stable-route-label").textContent.trim() === "Shared" &&
      state.routeInit === 2 && state.routeCleanup === 1 && state.nextInit === 1;
  }, "exact unsigned stable script topology did not Morph");

  assert(globalThis.kit === firstKit && document.documentElement === firstHTML,
    "Drive replaced the standalone runtime or document root");
  assert(document.getElementById("stable-retained") === retained,
    "exact stable topology replaced the retained component host");
  assert(!oldRoute.isConnected, "removed route component host remained connected");
  assert(state.retainedInit === 1 && state.retainedCleanup === 0,
    "Morph reinitialized or cleaned the retained component");
  assert(state.bundleRuns === 1, "Morph reran the stable shared component bundle");
  assert(history.length === historyLength + 1, "exact stable Morph did not push one history entry");

  async function expectFallback(name, label) {
    var cookie = "kit_stable_" + name.replace(/-/g, "_") + "=1";
    var title = document.title;
    var path = location.pathname + location.search + location.hash;
    var historyCount = history.length;
    var route = document.getElementById("stable-route-label").textContent.trim();
    document.getElementById("stable-" + name + "-link").click();
    await waitFor(function () { return document.cookie.indexOf(cookie) >= 0; }, label + " did not fall back natively");
    assert(document.title === title && location.pathname + location.search + location.hash === path,
      label + " mutated title or URL before fallback");
    assert(history.length === historyCount && document.getElementById("stable-route-label").textContent.trim() === route,
      label + " mutated history or body before fallback");
    assert(document.getElementById("stable-retained") === retained && state.retainedCleanup === 0,
      label + " replaced or cleaned the retained component");
    assert(state.bundleRuns === 1 && state.routeInit === 2 && state.routeCleanup === 1 &&
      state.nextInit === 1 && state.nextCleanup === 0,
      label + " executed or committed fetched component state");
  }

  var mismatches = [
    ["url", "script URL change"],
    ["query", "script query change"],
    ["attribute", "script attribute change"],
    ["order", "script order change"],
    ["added", "script addition"],
    ["removed", "script removal"],
    ["cross-origin", "cross-origin unsigned stable script"],
    ["inline", "inline stable script"],
    ["body", "body stable script"],
    ["module", "module stable script"],
    ["malformed-integrity", "malformed SRI with stable marker"],
    ["false", "false script policy"],
    ["unknown", "unknown script policy"]
  ];
  for (var index = 0; index < mismatches.length; index++) {
    await expectFallback(mismatches[index][0], mismatches[index][1]);
  }
  assert(sessionStorage.getItem("stableFetchedInline") === null,
    "an inline script from fetched HTML executed");
  assert(globalThis.__stableUnexpectedScriptRan !== true && globalThis.__stableCrossOriginRan !== true,
    "an incompatible external script from fetched HTML executed");
  await expectFallback("disabled", "stable marker below a disabled ancestor");
});`

const stableDriveInvalidPreludeSource = `(function (global) {
  "use strict";
  var state = global.__stableInvalidInitial = { fetches: [], warnings: [] };
  var realFetch = global.fetch.bind(global);
  global.fetch = function (source, options) {
    state.fetches.push(String(source));
    return realFetch(source, options);
  };
  var realWarn = global.console.warn;
  global.console.warn = function () {
    var message = Array.prototype.map.call(arguments, function (value) { return String(value); }).join(" ");
    state.warnings.push(message);
    return realWarn.apply(this, arguments);
  };
})(globalThis);`

const stableDriveInvalidContractSource = browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var state = globalThis.__stableInvalidInitial;
  var disabledWarnings = state.warnings.filter(function (message) {
    return message.indexOf("KitJS Drive") >= 0 && message.indexOf("disabled") >= 0;
  });
  assert(disabledWarnings.length === 1,
    "invalid initial topology warning count was " + disabledWarnings.length + " instead of one");

  var path = location.pathname + location.search + location.hash;
  var historyLength = history.length;
  document.getElementById("invalid-initial-link").click();
  await waitFor(function () {
    return document.cookie.indexOf("kit_stable_invalid_initial=1") >= 0;
  }, "invalid initial topology did not permit native navigation");
  assert(state.fetches.filter(function (source) {
    return new URL(source, location.href).pathname === "/invalid-initial-target";
  }).length === 0, "invalid initial topology intercepted the link with fetch");
  assert(location.pathname + location.search + location.hash === path && history.length === historyLength,
    "204 native navigation mutated the invalid initial document");
  assert(document.getElementById("invalid-initial-route").textContent.trim() === "Initial",
    "invalid initial topology mutated the document");
});`

const stagedAuthoredStableSource = `(function (global) {
  "use strict";
  var state = global.__stagedAuthoredStable;
  if (!state) {
    state = global.__stagedAuthoredStable = {
      authoredRuns: 0,
      baseRuns: 0,
      extraRuns: 0,
      extraInit: 0
    };
  }
  state.authoredRuns++;
})(globalThis);
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var state = globalThis.__stagedAuthoredStable;

  await waitFor(function () {
    return globalThis.kit && state.baseRuns === 1 &&
      document.getElementById("staged-stable-value").textContent.trim() === "0";
  }, "initial sealed staged graph did not boot beside the authored stable script");
  assert(state.authoredRuns === 1, "ordinary authored stable script did not execute exactly once initially");

  var root = document.documentElement;
  var body = document.body;
  var kitObject = globalThis.kit;
  var baseHost = document.getElementById("staged-stable-base");
  document.getElementById("staged-stable-add").click();
  await waitFor(function () {
    return document.getElementById("staged-stable-value").textContent.trim() === "1";
  }, "staged base component did not update before graph handoff");

  document.getElementById("staged-stable-next-link").click();
  await waitFor(function () {
    var extra = document.getElementById("staged-stable-extra-value");
    return location.pathname === "/staged-stable/next" &&
      document.getElementById("staged-stable-route").textContent.trim() === "next" &&
      extra && extra.textContent.trim() === "extra-ready" && state.extraInit === 1;
  }, "ordinary stable script blocked sealed staged component graph handoff");
  assert(document.documentElement === root && document.body === body && globalThis.kit === kitObject,
    "staged handoff replaced the document roots or public runtime");
  assert(document.getElementById("staged-stable-base") === baseHost &&
    document.getElementById("staged-stable-value").textContent.trim() === "1",
    "staged handoff replaced or reset its stable component");
  assert(state.authoredRuns === 1 && state.baseRuns === 1 && state.extraRuns === 1,
    "staged handoff reran the ordinary stable script or a stable managed package");

  async function expectFallback(link, cookie, label) {
    var title = document.title;
    var path = location.pathname;
    var historyLength = history.length;
    var bodyHTML = document.body.innerHTML;
    document.getElementById(link).click();
    await waitFor(function () { return document.cookie.indexOf(cookie + "=1") >= 0; },
      label + " did not fall back natively");
    assert(document.title === title && location.pathname === path && history.length === historyLength,
      label + " mutated title, URL, or history before fallback");
    assert(document.documentElement === root && document.body === body && document.body.innerHTML === bodyHTML,
      label + " mutated the document before fallback");
    assert(globalThis.kit === kitObject && document.getElementById("staged-stable-base") === baseHost &&
      document.getElementById("staged-stable-value").textContent.trim() === "1",
      label + " replaced the runtime or component state before fallback");
    assert(state.authoredRuns === 1 && state.baseRuns === 1 && state.extraRuns === 1 && state.extraInit === 1,
      label + " executed an incoming script or changed the installed graph");
  }

  await expectFallback("staged-managed-stable-link", "kit_staged_managed_stable",
    "data-kit-drive=stable on a sealed managed asset");
  await expectFallback("staged-managed-downgrade-link", "kit_staged_managed_downgrade",
    "stable marker replacing managed asset integrity");
});`
