package vanilla

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const progressBar110SHA256 = "5f0bc75d2f7fba8e56716a2236d46c3f2059d26f0fd609c39f9f3bea00d59f01"

func TestProgressPackagesStaticContract(t *testing.T) {
	service := readVanillaFile(t, "service", "progress", "1.0.0.js")
	if len(service) == 0 || service[0] != ';' || service[len(service)-1] != '\n' {
		t.Fatal("progress@1.0.0 is not a sealable classic script")
	}
	if got := bytes.Count(service, []byte(`kit.service("progress"`)); got != 1 {
		t.Fatalf("progress@1.0.0 registration count = %d, want 1", got)
	}
	for _, required := range []string{
		`snapshot: snapshot`, `subscribe: subscribe`, `start: start`, `update: update`,
		`finish: finish`, `document.addEventListener("kit:navigation", navigation)`,
	} {
		if !bytes.Contains(service, []byte(required)) {
			t.Fatalf("progress@1.0.0 lost %s", required)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", `kit.component(`} {
		if bytes.Contains(service, []byte(forbidden)) {
			t.Fatalf("progress@1.0.0 contains forbidden runtime coupling %q", forbidden)
		}
	}

	component := readVanillaFile(t, "component", "progress-bar", "1.2.0.js")
	if len(component) == 0 || component[0] != ';' || component[len(component)-1] != '\n' {
		t.Fatal("progress-bar@1.2.0 is not a sealable classic script")
	}
	if got := bytes.Count(component, []byte(`kit.component("progress-bar"`)); got != 1 {
		t.Fatalf("progress-bar@1.2.0 registration count = %d, want 1", got)
	}
	for _, required := range []string{`kit.progress.snapshot()`, `kit.progress.subscribe(`} {
		if !bytes.Contains(component, []byte(required)) {
			t.Fatalf("progress-bar@1.2.0 lost %s", required)
		}
	}
	for _, forbidden := range []string{"kit:navigation", "document.addEventListener", "document.removeEventListener", "fetch("} {
		if bytes.Contains(component, []byte(forbidden)) {
			t.Fatalf("progress-bar@1.2.0 bypasses progress@1.0.0 with %q", forbidden)
		}
	}

	historical := readVanillaFile(t, "component", "progress-bar", "1.1.0.js")
	if got := ContentHash(historical); got != progressBar110SHA256 {
		t.Fatalf("immutable progress-bar@1.1.0 SHA-256 = %s, want %s", got, progressBar110SHA256)
	}
}

func TestBuildComponentServiceRequirementsAreClosedAndDeterministic(t *testing.T) {
	progress := progressServicePackage(t)
	storage := Service{
		Name: "storage", Version: "1.0.0",
		Source: []byte(";kit.service(\"storage\", { get: function () { return null; } });\n"),
	}
	progressBar := ComponentVersion{Name: "progress-bar", Version: "1.2.0"}
	panel := ComponentVersion{Name: "panel", Version: "1.0.0"}
	progressEdge := ComponentServiceRequirement{
		Component: "progress-bar",
		Service:   ServiceVersion{Name: "progress", Version: "1.0.0"},
	}
	storageEdge := ComponentServiceRequirement{
		Component: "panel",
		Service:   ServiceVersion{Name: "storage", Version: "1.0.0"},
	}
	scripts := []Script{
		{Name: "progress-bar", Source: readVanillaFile(t, "component", "progress-bar", "1.2.0.js")},
		{Name: "panel", Source: []byte(";kit.component(\"panel\", {});\n")},
	}

	left, err := Build(BuildOptions{
		Profile: ProfileHydrate, Services: []Service{storage, progress},
		Components:        []ComponentVersion{progressBar, panel},
		ComponentRequires: []ComponentServiceRequirement{progressEdge, storageEdge},
		Scripts:           scripts,
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Build(BuildOptions{
		Profile: ProfileHydrate, Services: []Service{progress, storage},
		Components:        []ComponentVersion{panel, progressBar},
		ComponentRequires: []ComponentServiceRequirement{storageEdge, progressEdge},
		Scripts:           []Script{scripts[1], scripts[0]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.Name() != right.Name() || left.SHA256() != right.SHA256() || !bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("component/service discovery order changed deterministic graph identity")
	}

	withoutProgressEdge, err := Build(BuildOptions{
		Profile: ProfileHydrate, Services: []Service{progress, storage},
		Components:        []ComponentVersion{progressBar, panel},
		ComponentRequires: []ComponentServiceRequirement{storageEdge},
		Scripts:           scripts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256() == withoutProgressEdge.SHA256() || bytes.Equal(left.Bytes(), withoutProgressEdge.Bytes()) {
		t.Fatal("component-to-service dependency metadata did not affect graph identity")
	}
	serviceAt := bytes.Index(left.Bytes(), progress.Source)
	componentAt := bytes.Index(left.Bytes(), scripts[0].Source)
	if serviceAt < 0 || componentAt < 0 || serviceAt >= componentAt {
		t.Fatalf("progress graph order = service:%d component:%d, want service before component", serviceAt, componentAt)
	}
}

func TestBuildRejectsInvalidComponentServiceRequirements(t *testing.T) {
	component := ComponentVersion{Name: "progress-bar", Version: "1.2.0"}
	progress := progressServicePackage(t)
	valid := ComponentServiceRequirement{
		Component: "progress-bar",
		Service:   ServiceVersion{Name: "progress", Version: "1.0.0"},
	}
	tests := []struct {
		name       string
		components []ComponentVersion
		services   []Service
		requires   []ComponentServiceRequirement
		contains   string
	}{
		{
			name: "missing component owner", services: []Service{progress},
			requires: []ComponentServiceRequirement{valid}, contains: "owner component",
		},
		{
			name: "invalid service identity", components: []ComponentVersion{component}, services: []Service{progress},
			requires: []ComponentServiceRequirement{{Component: "progress-bar", Service: ServiceVersion{Name: "progress", Version: "latest"}}},
			contains: "invalid service dependency",
		},
		{
			name: "missing service", components: []ComponentVersion{component},
			requires: []ComponentServiceRequirement{valid}, contains: "requires missing service",
		},
		{
			name: "version mismatch", components: []ComponentVersion{component}, services: []Service{progress},
			requires: []ComponentServiceRequirement{{Component: "progress-bar", Service: ServiceVersion{Name: "progress", Version: "2.0.0"}}},
			contains: "graph provides 1.0.0",
		},
		{
			name: "duplicate dependency", components: []ComponentVersion{component}, services: []Service{progress},
			requires: []ComponentServiceRequirement{valid, valid}, contains: "repeats service dependency",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Build(BuildOptions{
				Profile: ProfileKit, Services: test.services, Components: test.components,
				ComponentRequires: test.requires,
			})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Build error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestBrowserProgressServiceContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping progress service browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	base, err := Build(BuildOptions{Profile: ProfileKit})
	if err != nil {
		t.Fatal(err)
	}
	progress, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{progressServicePackage(t)}})
	if err != nil {
		t.Fatal(err)
	}
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/assets/" + base.Name():
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(base.Bytes())
		case "/assets/" + progress.Name():
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(progress.Bytes())
		case "/base.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, progressBaseDocument, base.Name())
		case "/progress.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, progressServiceDocument, progress.Name())
		case "/service/progress/1.0.0.js", "/progress.js":
			packageRequests.Add(1)
			http.Error(response, "progress must already be sealed", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	t.Run("base-has-no-progress", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/base.html")
	})
	t.Run("sealed-progress", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/progress.html")
	})
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched a progress package at runtime %d times", got)
	}
}

func TestBrowserProgressComponentSubscriptionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping progress component subscription lifecycle in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildProgressLifecycleArtifact(t, ProfileHydrate)
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/one.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, progressLifecycleFirstDocument, assetPath)
		case "/two.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, progressLifecycleSecondDocument, assetPath)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/one.html")
}

func TestBrowserProgressComponentUnsubscribeReleasesListener(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping progress component forced-GC contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildProgressLifecycleArtifact(t, ProfileKit)
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, progressRetentionDocument, assetPath)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced progress collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("progress unsubscribe retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

func buildProgressLifecycleArtifact(t *testing.T, profile Profile) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile: profile,
		Services: []Service{
			{Name: "a-progress-probe", Version: "1.0.0", Source: []byte(progressSubscriptionProbeSource)},
			progressServicePackage(t),
		},
		Components: []ComponentVersion{{Name: "progress-bar", Version: "1.2.0"}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "progress-bar",
			Service:   ServiceVersion{Name: "progress", Version: "1.0.0"},
		}},
		Scripts: []Script{{
			Name: "progress-bar", Source: readVanillaFile(t, "component", "progress-bar", "1.2.0.js"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func progressServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name: "progress", Version: "1.0.0",
		Source: readVanillaFile(t, "service", "progress", "1.0.0.js"),
	}
}

const progressBaseDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Base progress isolation</title><script src="/assets/%s"></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  __kitTestAssert(Object.keys(globalThis.kit).join(",") === "version,component",
    "base artifact keys were " + Object.keys(globalThis.kit).join(","));
  __kitTestAssert(globalThis.kit.progress === undefined, "base artifact exposed progress");
});
</script></body></html>`

const progressServiceDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Progress service contract</title><script>
globalThis.__progressFetches = 0;
globalThis.__progressNavigationAdds = 0;
globalThis.__progressReported = [];
var __progressFetch = globalThis.fetch;
globalThis.fetch = function () {
  globalThis.__progressFetches++;
  return __progressFetch.apply(this, arguments);
};
var __progressAdd = document.addEventListener.bind(document);
document.addEventListener = function (type, listener, options) {
  if (type === "kit:navigation") globalThis.__progressNavigationAdds++;
  return __progressAdd(type, listener, options);
};
globalThis.reportError = function (error) {
  globalThis.__progressReported.push(String(error && error.message || error));
};
</script><script src="/assets/%s"></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var progress = globalThis.kit.progress;
  function check(value, label) {
    assert(Object.isFrozen(value), label + " snapshot was mutable");
    assert(Object.keys(value).join(",") === "id,phase,source,url,loaded,total,outcome",
      label + " snapshot keys were " + Object.keys(value).join(","));
    assert(typeof value.id === "string" && typeof value.phase === "string" &&
      typeof value.source === "string" && typeof value.url === "string" &&
      typeof value.loaded === "number" && Number.isFinite(value.loaded) &&
      (value.total === null || typeof value.total === "number" && Number.isFinite(value.total)) &&
      (value.outcome === null || typeof value.outcome === "string"), label + " snapshot was not primitive-only");
    return value;
  }

  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress",
    "sealed artifact keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(progress), "public progress namespace was mutable");
  assert(progress.version === "1.0.0", "progress version was " + progress.version);
  assert(Object.keys(progress).slice().sort().join(",") === "finish,snapshot,start,subscribe,update",
    "progress namespace members were " + Object.keys(progress).join(","));
  assert(globalThis.kit.service === undefined && globalThis.kit.bridge === undefined &&
    progress.bridge === undefined && progress.emit === undefined && progress.register === undefined,
    "progress exposed registration, bridge, or runtime controls");
  assert(globalThis.__progressNavigationAdds === 1, "progress navigation adapter count was " + globalThis.__progressNavigationAdds);

  var idle = check(progress.snapshot(), "idle");
  assert(idle.id === "" && idle.phase === "idle" && idle.source === "" && idle.url === "" &&
    idle.loaded === 0 && idle.total === null && idle.outcome === null, "idle snapshot was not empty");
  assert(progress.snapshot() === idle, "snapshot() copied unchanged state");

  var immediate = [];
  var stopImmediate = progress.subscribe(function (value) { immediate.push(value); });
  assert(immediate.length === 1 && immediate[0] === idle, "subscribe did not immediately deliver current snapshot");
  stopImmediate();
  stopImmediate();
  progress.start("unsubscribed", { source: "bridge", url: "bridge://ignored" });
  assert(immediate.length === 1, "idempotently unsubscribed listener still received state");

  var received = [];
  var stopThrowing = progress.subscribe(function () { throw new Error("subscriber failure"); });
  var stop = progress.subscribe(function (value) { received.push(check(value, "delivery")); });
  var reportsBeforePublish = globalThis.__progressReported.length;
  var start = progress.start(7, { source: "bridge", url: "bridge://upload", total: 200 });
  assert(received[received.length - 1] === start && start.id === "7" && start.phase === "start" &&
    start.source === "bridge" && start.url === "bridge://upload" && start.loaded === 0 &&
    start.total === 200 && start.outcome === null, "manual bridge start was not normalized");
  assert(globalThis.__progressReported.length === reportsBeforePublish + 1,
    "throwing subscriber blocked or escaped isolated delivery");
  stopThrowing();

  var measured = progress.update(7, 50, 200);
  assert(measured.phase === "progress" && measured.loaded === 50 && measured.total === 200,
    "manual bridge update was not published");
  var latest = progress.start("latest", { source: "bridge", url: "bridge://latest" });
  assert(latest.id === "latest" && latest.total === null, "latest start did not replace the active operation");
  assert(progress.update(7, -1, 0) === false && progress.snapshot() === latest,
    "stale update validated or replaced latest state");
  assert(progress.finish(7, "not-an-outcome") === false && progress.snapshot() === latest,
    "stale finish validated or replaced latest state");
  progress.update("latest", 25, 100);
  var loaded = progress.finish("latest", "loaded");
  assert(loaded.phase === "finish" && loaded.loaded === 100 && loaded.total === 100 && loaded.outcome === "loaded",
    "loaded finish did not close known progress at total");
  assert(progress.finish("latest", "loaded") === false && progress.snapshot() === loaded,
    "duplicate finish republished terminal state");

  var reentrantSeen = [];
  var nestedSeen = [];
  var reentrantArmed = false;
  var stopNested = null;
  var stopReentrantFinisher = progress.subscribe(function (value) {
    if (!reentrantArmed || value.id !== "reentrant" || value.phase !== "start") return;
    progress.finish(value.id, "loaded");
    stopNested = progress.subscribe(function (nested) {
      if (nested.id === "reentrant") nestedSeen.push(nested.phase + ":" + nested.outcome);
    });
  });
  var stopReentrantObserver = progress.subscribe(function (value) {
    if (reentrantArmed && value.id === "reentrant") {
      reentrantSeen.push(value.phase + ":" + value.outcome);
    }
  });
  reentrantArmed = true;
  var reentrantStart = progress.start("reentrant", { source: "bridge", total: 1 });
  assert(reentrantStart.phase === "start" && progress.snapshot().phase === "finish",
    "reentrant finish did not leave the latest terminal snapshot");
  assert(reentrantSeen.join(",") === "start:null,finish:loaded",
    "reentrant observer delivery order was " + reentrantSeen.join(","));
  assert(nestedSeen.join(",") === "finish:loaded",
    "nested subscriber received a queued snapshot more than once: " + nestedSeen.join(","));
  stopReentrantFinisher();
  stopReentrantObserver();
  if (stopNested) stopNested();

  var beforeMalformed = progress.snapshot();
  document.dispatchEvent(new CustomEvent("kit:navigation", { detail: { id: "bad", phase: "start" } }));
  assert(progress.snapshot() === beforeMalformed, "malformed document navigation entered trusted state");
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: 42, phase: "start", url: "/next" })
  }));
  var navigation = progress.snapshot();
  assert(navigation.id === "42" && navigation.phase === "start" && navigation.source === "navigation" &&
    navigation.url === "/next", "navigation start was not adapted");
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: 42, phase: "progress", url: "/next", loaded: 3, total: 9 })
  }));
  assert(progress.snapshot().phase === "progress" && progress.snapshot().loaded === 3,
    "navigation progress was not adapted");
  var bridgeReplacement = progress.start("bridge-replacement", { source: "bridge" });
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: 42, phase: "finish", url: "/next", outcome: "loaded" })
  }));
  assert(progress.snapshot() === bridgeReplacement, "stale navigation finish replaced a newer manual source");
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: "nav-2", phase: "start", url: "/error" })
  }));
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: "nav-2", phase: "finish", url: "/error", outcome: "error" })
  }));
  assert(progress.snapshot().source === "navigation" && progress.snapshot().phase === "finish" &&
    progress.snapshot().outcome === "error", "navigation error finish was not adapted");

  var deliveries = received.length;
  stop();
  stop();
  progress.start("after-unsubscribe");
  assert(received.length === deliveries, "idempotently removed subscriber received another snapshot");
  var invalidThrew = false;
  try { progress.start(""); } catch (error) { invalidThrew = error instanceof TypeError; }
  assert(invalidThrew, "trusted manual controls accepted an empty id");
  assert(globalThis.__progressFetches === 0, "progress service performed " + globalThis.__progressFetches + " fetches");
});
</script></body></html>`

const progressSubscriptionProbeSource = `;globalThis.__progressLifecycle = {
  subscribes: 0,
  unsubscribes: 0,
  listenerRefs: []
};
var progressProbeRegister = kit.service;
progressProbeRegister("a-progress-probe", {});
Object.defineProperty(kit, "service", {
  configurable: true,
  value: function (name, namespace) {
    if (name === "progress") {
      var subscribe = namespace.subscribe;
      namespace.subscribe = function (listener) {
        var probe = globalThis.__progressLifecycle;
        probe.subscribes++;
        if (typeof WeakRef === "function") probe.listenerRefs.push(new WeakRef(listener));
        var unsubscribe = subscribe(listener);
        var active = true;
        return function () {
          if (!active) return;
          active = false;
          probe.unsubscribes++;
          unsubscribe();
        };
      };
    }
    return progressProbeRegister(name, namespace);
  }
});
`

const progressLifecycleFirstDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Progress lifecycle one</title><script defer src=%q></script></head><body>
<main id="progress-one" data-kit-component="progress-bar" data-kit-version="1.2.0">
  <output id="progress-one-message" data-kit-text="message">server</output>
</main>
<a id="progress-next" href="/two.html">Next</a>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  await waitFor(function () {
    return globalThis.__progressLifecycle && globalThis.__progressLifecycle.subscribes === 1 &&
      document.getElementById("progress-one-message").textContent.trim() === "Ready";
  }, "progress component did not subscribe exactly once");

  for (var index = 0; index < 100; index++) {
    var id = "synthetic-navigation-" + index;
    var url = "/synthetic/" + index;
    document.dispatchEvent(new CustomEvent("kit:navigation", {
      detail: Object.freeze({ id: id, phase: "start", url: url })
    }));
    document.dispatchEvent(new CustomEvent("kit:navigation", {
      detail: Object.freeze({ id: id, phase: "finish", url: url, outcome: "cancelled" })
    }));
  }
  await nextTurn();
  assert(globalThis.__progressLifecycle.subscribes === 1 && globalThis.__progressLifecycle.unsubscribes === 0,
    "100 navigation lifecycles changed the component subscription count");
  assert(document.getElementById("progress-one-message").textContent.trim() === "Ready",
    "cancelled navigation series did not return progress to idle");

  document.getElementById("progress-next").click();
  await waitFor(function () {
    return location.pathname === "/two.html" && globalThis.__progressLifecycle.subscribes === 2 &&
      globalThis.__progressLifecycle.unsubscribes === 1 && document.getElementById("progress-two");
  }, "Drive Morph did not unsubscribe the removed component and subscribe its replacement exactly once");

  var replacement = document.getElementById("progress-two");
  var detachedMessage = document.getElementById("progress-two-message");
  replacement.remove();
  await waitFor(function () { return globalThis.__progressLifecycle.unsubscribes === 2; },
    "direct removal did not unsubscribe the replacement exactly once");
  var retainedText = detachedMessage.textContent;
  document.dispatchEvent(new CustomEvent("kit:navigation", {
    detail: Object.freeze({ id: "after-removal", phase: "start", url: "/detached" })
  }));
  await nextTurn();
  assert(detachedMessage.textContent === retainedText, "removed progress component still received service snapshots");
  assert(globalThis.__progressLifecycle.subscribes === 2 && globalThis.__progressLifecycle.unsubscribes === 2,
    "removed progress component changed subscription ownership after disposal");
});
</script></body></html>`

const progressLifecycleSecondDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Progress lifecycle two</title><script defer src=%q></script></head><body>
<main id="progress-two" data-kit-component="progress-bar" data-kit-version="1.2.0">
  <output id="progress-two-message" data-kit-text="message">server</output>
</main>
</body></html>`

const progressRetentionDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Progress subscriber retention</title><script defer src=%q></script></head><body>
<main id="progress-retention" data-kit-component="progress-bar" data-kit-version="1.2.0">
  <output data-kit-text="message">server</output>
</main>
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
  function collect(refs, controlRefs, pass) {
    var pressure = [];
    for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
    pressure = null;
    globalThis.gc();
    globalThis.gc();
    if (pass < 7) {
      setTimeout(function () { collect(refs, controlRefs, pass + 1); }, 0);
      return;
    }
    if (alive(controlRefs) !== 0) {
      finish("unsupported", "forced GC retained control objects");
      return;
    }
    if (alive(refs) !== 0) fail("progress service retained a removed component or subscriber");
    finish("passed");
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
      waitFor(function () {
        return globalThis.__progressLifecycle && globalThis.__progressLifecycle.subscribes === 1;
      }, "progress component did not subscribe", performance.now() + 2000, function () {
        try {
          var host = document.getElementById("progress-retention");
          var refs = [new WeakRef(host), globalThis.__progressLifecycle.listenerRefs[0]];
          host.remove();
          host = null;
          waitFor(function () { return globalThis.__progressLifecycle.unsubscribes === 1; },
            "progress component did not unsubscribe", performance.now() + 2000, function () {
            setTimeout(function () { collect(refs, controls(), 0); }, 0);
          });
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
</script></body></html>`
