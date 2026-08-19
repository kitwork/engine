package javascript

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type stagedDriveAssetFixture struct {
	source      []byte
	requests    atomic.Int64
	unavailable bool
}

type stagedDrivePageFixture struct {
	route string
	title string
	extra bool
}

// TestStagedDriveComponentOnlyHandoff is the phase-one browser acceptance gate
// for changing exact staged graphs without replacing the document. It stays
// deliberately black-box: the proof observes HTTP requests, the public kit
// identity, component behavior, lifecycle, DOM, title, URL, and history only.
func TestStagedDriveComponentOnlyHandoff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged Drive component-handoff browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	const commonCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'"
	contractSource := []byte(stagedDriveHandoffPrelude + "\n" + browserHarness + "\n" + stagedDriveHandoffAssertions)
	contractIntegrity := driveScriptIntegrity(contractSource)

	counter := ComponentPackage{
		Name: "counter", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedHandoffRuns.counter++;
kit.component("counter", { count: 0 });
`),
	}
	extra := ComponentPackage{
		Name: "extra", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedHandoffRuns.extra++;
kit.component("extra", {
  ready: "extra-ready",
  init: function () {
    globalThis.__stagedHandoffLifecycle.extraInit++;
    return function () { globalThis.__stagedHandoffLifecycle.extraCleanup++; };
  }
});
`),
	}
	stagedBeforeFailure := ComponentPackage{
		Name: "handoff-a-stage", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedHandoffRuns.staged++;
kit.component("handoff-a-stage", { ready: true });
`),
	}
	missing := ComponentPackage{
		Name: "handoff-missing", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedHandoffRuns.missing++;
kit.component("handoff-missing", { ready: true });
`),
	}
	service := Service{
		Name: "handoff-service", Version: "1.0.0",
		Source: []byte(`; globalThis.__stagedHandoffRuns.service++;
kit.service("handoff-service", { ping: function () { return "pong"; } });
`),
	}

	first, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{counter},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{counter, extra},
	})
	if err != nil {
		t.Fatal(err)
	}
	serviceGraph, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Services: []Service{service}, Components: []ComponentPackage{counter},
	})
	if err != nil {
		t.Fatal(err)
	}
	broken, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{counter, stagedBeforeFailure, missing},
	})
	if err != nil {
		t.Fatal(err)
	}

	assemblies := []StagedAssembly{first, second, serviceGraph, broken}
	for _, assembly := range assemblies[1:] {
		if first.Runtime.SHA256() != assembly.Runtime.SHA256() || first.Hydrate == nil ||
			assembly.Hydrate == nil || first.Hydrate.SHA256() != assembly.Hydrate.SHA256() {
			t.Fatal("fixture does not keep runtime and Hydrate stable across exact graphs")
		}
	}
	firstCounter := stagedDriveArtifact(t, first, JITRoleComponent, "counter")
	for _, assembly := range assemblies[1:] {
		candidate := stagedDriveArtifact(t, assembly, JITRoleComponent, "counter")
		if candidate.SHA256() != firstCounter.SHA256() {
			t.Fatal("fixture does not keep the counter package stable across exact graphs")
		}
	}
	graphHashes := map[string]bool{}
	for _, assembly := range assemblies {
		graphHashes[assembly.Graph.SHA256()] = true
	}
	if len(graphHashes) != len(assemblies) {
		t.Fatal("fixture graphs are not distinct")
	}

	assets := make(map[string]*stagedDriveAssetFixture)
	for _, assembly := range assemblies {
		for _, artifact := range assembly.Artifacts() {
			path := "/jit/" + artifact.Name()
			if prior, exists := assets[path]; exists {
				if !bytes.Equal(prior.source, artifact.Bytes()) {
					t.Fatalf("content-addressed fixture collision at %s", path)
				}
				continue
			}
			assets[path] = &stagedDriveAssetFixture{source: artifact.Bytes()}
		}
	}
	missingArtifact := stagedDriveArtifact(t, broken, JITRoleComponent, "handoff-missing")
	stagedBeforeFailureArtifact := stagedDriveArtifact(t, broken, JITRoleComponent, "handoff-a-stage")
	assets["/jit/"+missingArtifact.Name()].unavailable = true

	var aDrive atomic.Int64
	var aFull atomic.Int64
	var bDrive atomic.Int64
	var bFull atomic.Int64
	var serviceDrive atomic.Int64
	var serviceFull atomic.Int64
	var brokenDrive atomic.Int64
	var brokenFull atomic.Int64
	var forgedDrive atomic.Int64
	var forgedFull atomic.Int64
	var attributesDrive atomic.Int64
	var attributesFull atomic.Int64
	var noncontiguousDrive atomic.Int64
	var noncontiguousFull atomic.Int64
	var legacyDrive atomic.Int64
	var legacyFull atomic.Int64
	var liveAnchorDrive atomic.Int64
	var liveAnchorFull atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/staged-drive-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		}
		if asset, exists := assets[request.URL.Path]; exists {
			asset.requests.Add(1)
			if asset.unavailable {
				http.NotFound(response, request)
				return
			}
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			response.Header().Set("ETag", `"`+strings.TrimPrefix(strings.SplitN(request.URL.Path, ".", 2)[0], "/jit/")+`"`)
			response.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = response.Write(asset.source)
			return
		}

		drive := request.Header.Get("X-KitJS-Drive") == "1"
		switch request.URL.Path {
		case "/a":
			if drive {
				aDrive.Add(1)
			} else {
				aFull.Add(1)
			}
			writeStagedDriveHTML(response, stagedDriveHandoffDocument(first, stagedDrivePageFixture{
				route: "a", title: "Staged A",
			}, commonCSP, contractIntegrity))
		case "/b":
			if drive {
				bDrive.Add(1)
				writeStagedDriveHTML(response, stagedDriveHandoffDocument(second, stagedDrivePageFixture{
					route: "b", title: "Staged B", extra: true,
				}, commonCSP, contractIntegrity))
				return
			}
			bFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_unexpected_b_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/service":
			if drive {
				serviceDrive.Add(1)
				writeStagedDriveHTML(response, stagedDriveHandoffDocument(serviceGraph, stagedDrivePageFixture{
					route: "service", title: "Service graph poison",
				}, commonCSP, contractIntegrity))
				return
			}
			serviceFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_service_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/broken":
			if drive {
				brokenDrive.Add(1)
				writeStagedDriveHTML(response, stagedDriveHandoffDocument(broken, stagedDrivePageFixture{
					route: "broken", title: "Broken component handoff",
				}, commonCSP, contractIntegrity))
				return
			}
			brokenFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_broken_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/forged-marker":
			if drive {
				forgedDrive.Add(1)
				changed := stagedDriveHandoffDocument(second, stagedDrivePageFixture{
					route: "forged-marker", title: "Forged staged marker", extra: true,
				}, commonCSP, contractIntegrity)
				changed = strings.Replace(changed, `<script data-kitwork-jit="legacy-theme"`,
					`<script data-kitwork-jit="legacy-theme" data-kitwork-hash="`+strings.Repeat("0", 64)+`"`, 1)
				writeStagedDriveHTML(response, changed)
				return
			}
			forgedFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_forged_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/extra-attribute":
			if drive {
				attributesDrive.Add(1)
				changed := stagedDriveHandoffDocument(second, stagedDrivePageFixture{
					route: "extra-attribute", title: "Extra staged attribute", extra: true,
				}, commonCSP, contractIntegrity)
				changed = strings.Replace(changed, `<script data-kitwork-jit="runtime"`,
					`<script data-kitwork-jit="runtime" data-forged="true"`, 1)
				writeStagedDriveHTML(response, changed)
				return
			}
			attributesFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_attributes_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/noncontiguous":
			if drive {
				noncontiguousDrive.Add(1)
				changed := stagedDriveHandoffDocument(second, stagedDrivePageFixture{
					route: "noncontiguous", title: "Noncontiguous staged block", extra: true,
				}, commonCSP, contractIntegrity)
				changed = strings.Replace(changed, `<script data-kitwork-jit="hydrate"`,
					`<meta name="staged-gap" content="forged"><script data-kitwork-jit="hydrate"`, 1)
				writeStagedDriveHTML(response, changed)
				return
			}
			noncontiguousFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_noncontiguous_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/legacy-ordinary-change":
			if drive {
				legacyDrive.Add(1)
				changed := stagedDriveHandoffDocument(second, stagedDrivePageFixture{
					route: "legacy-ordinary-change", title: "Legacy ordinary change", extra: true,
				}, commonCSP, contractIntegrity)
				changed = strings.Replace(changed, `<script data-kitwork-jit="legacy-theme" defer`,
					`<script data-kitwork-jit="legacy-theme" data-legacy-revision="2" defer`, 1)
				writeStagedDriveHTML(response, changed)
				return
			}
			legacyFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_legacy_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/live-anchor":
			if drive {
				liveAnchorDrive.Add(1)
				writeStagedDriveHTML(response, stagedDriveHandoffDocument(second, stagedDrivePageFixture{
					route: "live-anchor", title: "Live staged anchor", extra: true,
				}, commonCSP, contractIntegrity))
				return
			}
			liveAnchorFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_live_anchor_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/a")

	if got := aFull.Load(); got != 1 {
		t.Fatalf("A full requests = %d, want the initial document only", got)
	}
	if got := aDrive.Load(); got != 1 {
		t.Fatalf("A Drive requests = %d, want one cached return handoff", got)
	}
	if got := bDrive.Load(); got != 2 {
		t.Fatalf("B Drive requests = %d, want first and cached repeat visits", got)
	}
	if got := bFull.Load(); got != 0 {
		t.Fatalf("B full navigations = %d, want none", got)
	}
	if got := serviceDrive.Load(); got != 1 {
		t.Fatalf("service graph Drive probes = %d, want one", got)
	}
	if got := serviceFull.Load(); got != 1 {
		t.Fatalf("service graph hard navigations = %d, want one", got)
	}
	if got := brokenDrive.Load(); got != 1 {
		t.Fatalf("broken graph Drive probes = %d, want one", got)
	}
	if got := brokenFull.Load(); got != 1 {
		t.Fatalf("broken graph hard navigations = %d, want one", got)
	}
	for _, expectation := range []struct {
		name  string
		drive *atomic.Int64
		full  *atomic.Int64
	}{
		{name: "forged marker", drive: &forgedDrive, full: &forgedFull},
		{name: "extra attribute", drive: &attributesDrive, full: &attributesFull},
		{name: "noncontiguous block", drive: &noncontiguousDrive, full: &noncontiguousFull},
		{name: "legacy ordinary change", drive: &legacyDrive, full: &legacyFull},
		{name: "live anchor replacement", drive: &liveAnchorDrive, full: &liveAnchorFull},
	} {
		if got := expectation.drive.Load(); got != 1 {
			t.Errorf("%s Drive probes = %d, want one", expectation.name, got)
		}
		if got := expectation.full.Load(); got != 1 {
			t.Errorf("%s hard navigations = %d, want one", expectation.name, got)
		}
	}

	wantAssetRequests := []struct {
		name     string
		artifact JITArtifact
		want     int64
	}{
		{name: "runtime", artifact: first.Runtime, want: 1},
		{name: "hydrate", artifact: *first.Hydrate, want: 1},
		{name: "A graph", artifact: first.Graph, want: 1},
		{name: "counter", artifact: firstCounter, want: 1},
		{name: "B graph", artifact: second.Graph, want: 1},
		{name: "extra", artifact: stagedDriveArtifact(t, second, JITRoleComponent, "extra"), want: 1},
		{name: "service graph", artifact: serviceGraph.Graph, want: 0},
		{name: "service package", artifact: stagedDriveArtifact(t, serviceGraph, JITRoleService, "handoff-service"), want: 0},
		{name: "broken graph", artifact: broken.Graph, want: 1},
		{name: "staged package before failure", artifact: stagedBeforeFailureArtifact, want: 1},
		{name: "missing package", artifact: missingArtifact, want: 1},
	}
	for _, expectation := range wantAssetRequests {
		path := "/jit/" + expectation.artifact.Name()
		if got := assets[path].requests.Load(); got != expectation.want {
			t.Errorf("%s requests = %d, want %d", expectation.name, got, expectation.want)
		}
	}
}

func stagedDriveArtifact(t *testing.T, assembly StagedAssembly, role JITRole, packageName string) JITArtifact {
	t.Helper()
	for _, artifact := range assembly.Artifacts() {
		if artifact.Role() == role && artifact.Package() == packageName {
			return artifact
		}
	}
	t.Fatalf("staged fixture omitted %s package %q", role, packageName)
	return JITArtifact{}
}

func writeStagedDriveHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write([]byte(source))
}

func stagedDriveHandoffDocument(assembly StagedAssembly, page stagedDrivePageFixture, csp, contractIntegrity string) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	if csp != "" {
		output.WriteString(`<meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(csp) + `">`)
	}
	output.WriteString(`<title>` + html.EscapeString(page.title) + `</title>`)
	output.WriteString(`<script data-kitwork-jit="legacy-theme" defer src="/staged-drive-contract.js" integrity="` +
		html.EscapeString(contractIntegrity) + `" crossorigin="anonymous"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` +
			html.EscapeString(artifact.Name()) + `" integrity="` +
			html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	output.WriteString(`</head><body><nav aria-label="Staged handoff routes">
<a id="to-a" href="/a">A</a>
<a id="to-b" href="/b">B</a>
<a id="to-service" href="/service">Service graph</a>
<a id="to-broken" href="/broken">Broken graph</a>
<a id="to-forged-marker" href="/forged-marker">Forged marker</a>
<a id="to-extra-attribute" href="/extra-attribute">Extra attribute</a>
<a id="to-noncontiguous" href="/noncontiguous">Noncontiguous block</a>
<a id="to-legacy-ordinary-change" href="/legacy-ordinary-change">Legacy ordinary change</a>
<a id="to-live-anchor" href="/live-anchor">Live anchor</a>
</nav>
<main id="route">` + html.EscapeString(page.route) + `</main>
<section id="counter-host" data-kit-component="counter" data-kit-version="1.0.0">
  <button id="counter-add" type="button" data-kit-click="count = count + 1">Add</button>
  <output id="counter-value" data-kit-text="count">0</output>
</section>`)
	if page.extra {
		output.WriteString(`<section id="extra-host" data-kit-component="extra" data-kit-version="1.0.0">
  <output id="extra-value" data-kit-text="ready">server-extra</output>
</section>`)
	}
	output.WriteString(`</body></html>`)
	return output.String()
}

const stagedDriveHandoffPrelude = `(function (global, document) {
  "use strict";
  global.__stagedHandoffRuns = { counter: 0, extra: 0, service: 0, staged: 0, missing: 0 };
  global.__stagedHandoffLifecycle = { extraInit: 0, extraCleanup: 0 };
  global.__stagedHandoffIncomingScript = 0;
  global.__stagedHandoffBootError = "";
  global.__stagedHandoffListenerAdds = {
    document: { click: 0, submit: 0 },
    window: { popstate: 0, scroll: 0, pagehide: 0 }
  };
  var addDocument = document.addEventListener;
  document.addEventListener = function (type, listener, options) {
    if (Object.prototype.hasOwnProperty.call(global.__stagedHandoffListenerAdds.document, type)) {
      global.__stagedHandoffListenerAdds.document[type]++;
    }
    return addDocument.call(this, type, listener, options);
  };
  var addWindow = global.addEventListener;
  global.addEventListener = function (type, listener, options) {
    if (Object.prototype.hasOwnProperty.call(global.__stagedHandoffListenerAdds.window, type)) {
      global.__stagedHandoffListenerAdds.window[type]++;
    }
    return addWindow.call(this, type, listener, options);
  };
  global.addEventListener("error", function (event) {
    if (!global.__stagedHandoffBootError) {
      global.__stagedHandoffBootError = String(event.error && event.error.message || event.message || "script error");
    }
  });
})(globalThis, document);`

const stagedDriveHandoffAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var runs = globalThis.__stagedHandoffRuns;
  var lifecycle = globalThis.__stagedHandoffLifecycle;

  await waitFor(function () {
    if (globalThis.__stagedHandoffBootError) {
      throw new Error("initial staged boot failed: " + globalThis.__stagedHandoffBootError);
    }
    return globalThis.kit && document.getElementById("counter-value").textContent === "0" &&
      runs.counter === 1;
  }, "initial staged counter did not boot exactly once");
  var root = document.documentElement;
  var body = document.body;
  var kitObject = globalThis.kit;
  var counterHost = document.getElementById("counter-host");
  var listenerBaseline = JSON.stringify(globalThis.__stagedHandoffListenerAdds);

  document.getElementById("counter-add").click();
  await waitFor(function () { return document.getElementById("counter-value").textContent === "1"; },
    "counter state did not update before handoff");

  document.getElementById("to-b").click();
  await waitFor(function () {
    return location.pathname === "/b" && document.getElementById("route").textContent === "b" &&
      document.getElementById("extra-value") &&
      document.getElementById("extra-value").textContent === "extra-ready";
  }, "component-only graph did not hand off through Drive");
  assert(document.documentElement === root && document.body === body,
    "component-only handoff replaced a document root");
  assert(document.getElementById("counter-host") === counterHost &&
    document.getElementById("counter-value").textContent === "1",
    "component-only handoff replaced or reset the stable counter");
  assert(globalThis.kit === kitObject && Object.keys(globalThis.kit).join(",") === "version,component",
    "component-only handoff replaced or expanded the public kit facade");
  assert(runs.counter === 1 && runs.extra === 1 && lifecycle.extraInit === 1 && lifecycle.extraCleanup === 0,
    "first component-only handoff reran a stable package or mounted extra incorrectly");
  assert(JSON.stringify(globalThis.__stagedHandoffListenerAdds) === listenerBaseline,
    "component-only handoff reinstalled runtime or Hydrate listeners");

  document.getElementById("to-a").click();
  await waitFor(function () {
    return location.pathname === "/a" && document.getElementById("route").textContent === "a" &&
      !document.getElementById("extra-host") && lifecycle.extraCleanup === 1;
  }, "return component handoff did not reuse the installed graph");
  assert(document.getElementById("counter-host") === counterHost &&
    document.getElementById("counter-value").textContent === "1",
    "return handoff replaced or reset the stable counter");

  document.getElementById("to-b").click();
  await waitFor(function () {
    return location.pathname === "/b" && document.getElementById("route").textContent === "b" &&
      document.getElementById("extra-value") &&
      document.getElementById("extra-value").textContent === "extra-ready" && lifecycle.extraInit === 2;
  }, "repeated component handoff did not reuse the cached package");
  assert(runs.counter === 1 && runs.extra === 1 && lifecycle.extraCleanup === 1,
    "repeated component handoff re-executed a cached package");
  assert(document.getElementById("counter-host") === counterHost &&
    document.getElementById("counter-value").textContent === "1" && globalThis.kit === kitObject,
    "repeated component handoff lost stable document state");
  assert(JSON.stringify(globalThis.__stagedHandoffListenerAdds) === listenerBaseline,
    "repeated component handoff duplicated runtime listeners");

  function snapshot() {
    var route = document.getElementById("route");
    return {
      root: document.documentElement,
      body: document.body,
      title: document.title,
      path: location.pathname,
      head: document.head.innerHTML,
      bodyHTML: document.body.innerHTML,
      route: route,
      routeText: route.textContent,
      counter: document.getElementById("counter-host"),
      counterText: document.getElementById("counter-value").textContent,
      extra: document.getElementById("extra-host"),
      historyLength: history.length,
      historyState: JSON.stringify(history.state)
    };
  }
  function unchanged(before, label) {
    assert(document.documentElement === before.root && document.body === before.body,
      label + " replaced a document root before fallback");
    assert(document.title === before.title && location.pathname === before.path,
      label + " committed title or URL before fallback");
    assert(document.head.innerHTML === before.head && document.body.innerHTML === before.bodyHTML,
      label + " partially mutated head or body before fallback");
    assert(document.getElementById("route") === before.route && before.route.textContent === before.routeText,
      label + " replaced the live route before fallback");
    assert(document.getElementById("counter-host") === before.counter &&
      document.getElementById("counter-value").textContent === before.counterText &&
      document.getElementById("extra-host") === before.extra,
      label + " reset component identity or state before fallback");
    assert(history.length === before.historyLength && JSON.stringify(history.state) === before.historyState,
      label + " mutated history before fallback");
    assert(globalThis.kit === kitObject, label + " replaced the public kit object before fallback");
  }

  async function expectMetadataFallback(link, cookie, label) {
    var before = snapshot();
    document.getElementById(link).click();
    await waitFor(function () { return document.cookie.indexOf(cookie + "=1") >= 0; },
      label + " did not hard-navigate");
    unchanged(before, label);
  }

  await expectMetadataFallback("to-forged-marker", "staged_handoff_forged_full",
    "unknown role with a reserved hash marker");
  await expectMetadataFallback("to-extra-attribute", "staged_handoff_attributes_full",
    "staged script with an extra attribute");
  await expectMetadataFallback("to-noncontiguous", "staged_handoff_noncontiguous_full",
    "noncontiguous staged block");
  await expectMetadataFallback("to-legacy-ordinary-change", "staged_handoff_legacy_full",
    "changed signed legacy JIT script");

  var stable = snapshot();
  document.getElementById("to-service").click();
  await waitFor(function () { return document.cookie.indexOf("staged_handoff_service_full=1") >= 0; },
    "service graph change did not hard-navigate");
  unchanged(stable, "service graph change");
  assert(runs.service === 0, "service graph package executed during component-only handoff");

  stable = snapshot();
  document.getElementById("to-broken").click();
  await waitFor(function () { return document.cookie.indexOf("staged_handoff_broken_full=1") >= 0; },
    "failed package transaction did not fall back to hard navigation");
  unchanged(stable, "failed package transaction");
  assert(globalThis.__stagedHandoffIncomingScript === 0,
    "failed package transaction executed an incoming body script");
  assert(runs.staged === 0 && runs.missing === 0,
    "failed multi-package transaction executed a staged component installer");
  assert(runs.counter === 1 && runs.extra === 1 && lifecycle.extraInit === 2 && lifecycle.extraCleanup === 1,
    "failed package transaction changed stable package lifecycle");
  assert(JSON.stringify(globalThis.__stagedHandoffListenerAdds) === listenerBaseline,
    "failed package transaction duplicated runtime listeners");

  var originalRuntime = document.querySelector('script[data-kitwork-jit="runtime"]');
  var replacementRuntime = originalRuntime.cloneNode(true);
  originalRuntime.parentNode.replaceChild(replacementRuntime, originalRuntime);
  stable = snapshot();
  document.getElementById("to-live-anchor").click();
  await waitFor(function () { return document.cookie.indexOf("staged_handoff_live_anchor_full=1") >= 0; },
    "replaced live staged anchor did not hard-navigate");
  unchanged(stable, "replaced live staged anchor");
  await nextTurn();
});`
