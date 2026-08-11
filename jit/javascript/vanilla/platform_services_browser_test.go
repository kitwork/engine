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

func TestPlatformServicePackagesStaticContract(t *testing.T) {
	tests := []struct {
		name    string
		members []string
	}{
		{name: "announce", members: []string{"say: say", "polite: polite", "assertive: assertive", "clear: clear"}},
		{name: "fullscreen", members: []string{"request: request", "exit: exit", "active: active"}},
		{name: "navigation", members: []string{"back: back", "forward: forward", "reload: reload"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			source := readVanillaFile(t, "service", test.name, "1.0.0.js")
			if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
				t.Fatalf("%s@1.0.0 is not a sealable classic script", test.name)
			}
			registration := []byte(`kit.service("` + test.name + `"`)
			if got := bytes.Count(source, registration); got != 1 {
				t.Fatalf("%s@1.0.0 registration count = %d, want 1", test.name, got)
			}
			for _, member := range test.members {
				if !bytes.Contains(source, []byte(member)) {
					t.Fatalf("%s@1.0.0 lost %s", test.name, member)
				}
			}
			for _, forbidden := range []string{
				`kit.component(`, `global.kit`, `window.kit`, `fetch(`, `XMLHttpRequest`,
				`localStorage`, `sessionStorage`, `.innerHTML`, `insertAdjacentHTML`,
			} {
				if bytes.Contains(source, []byte(forbidden)) {
					t.Fatalf("%s@1.0.0 contains forbidden coupling %q", test.name, forbidden)
				}
			}
		})
	}
}

func TestPlatformServiceArtifactsContainOnlySelectedServices(t *testing.T) {
	packages := []Service{
		platformServicePackage(t, "announce"),
		platformServicePackage(t, "fullscreen"),
		platformServicePackage(t, "navigation"),
	}

	for _, selected := range packages {
		selected := selected
		t.Run(selected.Name, func(t *testing.T) {
			artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{selected}})
			if err != nil {
				t.Fatal(err)
			}
			if got := bytes.Count(artifact.Bytes(), selected.Source); got != 1 {
				t.Fatalf("%s source count in sealed artifact = %d, want 1", selected.Name, got)
			}
			for _, other := range packages {
				if other.Name != selected.Name && bytes.Contains(artifact.Bytes(), other.Source) {
					t.Fatalf("%s-only artifact contained %s@%s", selected.Name, other.Name, other.Version)
				}
			}
			if graph := string(artifact.Bytes()); !strings.Contains(graph, `services["`+selected.Name+`"] = "1.0.0";`) {
				t.Fatalf("%s graph metadata was missing", selected.Name)
			}
		})
	}
}

func TestBrowserPlatformServiceContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping platform service browser contracts in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifacts := make(map[string]Artifact, 3)
	for _, name := range []string{"announce", "fullscreen", "navigation"} {
		artifact, err := Build(BuildOptions{
			Profile:  ProfileKit,
			Services: []Service{platformServicePackage(t, name)},
		})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact
	}

	var packageRequests atomic.Int64
	var reloadPageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		for name, artifact := range artifacts {
			if request.URL.Path == "/assets/"+artifact.Name() {
				response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				_, _ = response.Write(artifact.Bytes())
				return
			}
			if request.URL.Path == "/service/"+name+"/1.0.0.js" || request.URL.Path == "/"+name+".js" {
				packageRequests.Add(1)
				http.Error(response, "service must already be sealed", http.StatusGone)
				return
			}
		}

		switch request.URL.Path {
		case "/announce.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, announceServiceDocument,
				"/assets/"+artifacts["announce"].Name(),
				"/assets/"+artifacts["announce"].Name())
		case "/fullscreen.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, fullscreenServiceDocument,
				"/assets/"+artifacts["fullscreen"].Name())
		case "/navigation.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, navigationServiceDocument,
				"/assets/"+artifacts["navigation"].Name())
		case "/reload.html":
			reloadPageRequests.Add(1)
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprintf(response, navigationReloadDocument,
				"/assets/"+artifacts["navigation"].Name())
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	for _, page := range []string{"announce", "fullscreen", "navigation", "reload"} {
		page := page
		t.Run(page, func(t *testing.T) {
			runVanillaBrowser(t, browser, server.URL+"/"+page+".html")
		})
	}
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched authored platform service packages %d times", got)
	}
	if got := reloadPageRequests.Load(); got < 2 {
		t.Fatalf("navigation.reload page requests = %d, want at least 2", got)
	}
}

func TestBrowserAnnounceRemovedRegionIsCollectable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping announce forced-GC contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{platformServicePackage(t, "announce")},
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
		case "/retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, announceRetentionDocument, assetPath)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced announce collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("announce removed-region retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

func platformServicePackage(t *testing.T, name string) Service {
	t.Helper()
	return Service{
		Name:    name,
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", name, "1.0.0.js"),
	}
}

const announceServiceDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Announce service contract</title><script>
globalThis.__announceFetches = 0;
var __announceFetch = globalThis.fetch;
globalThis.fetch = function () {
  globalThis.__announceFetches++;
  return __announceFetch.apply(this, arguments);
};
</script><script src=%q></script><script src=%q></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var announce = globalThis.kit.announce;
  function query(mode) { return document.querySelectorAll('[data-kit-announcer="' + mode + '"]'); }
  function invalid(run, label) {
    var threw = false;
    try { run(); } catch (error) { threw = error instanceof TypeError; }
    assert(threw, label + " did not throw TypeError");
  }

  assert(Object.keys(globalThis.kit).join(",") === "version,component,announce",
    "announce-only artifact keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(announce), "announce facade was mutable");
  assert(announce.version === "1.0.0", "announce version was " + announce.version);
  assert(Object.keys(announce).slice().sort().join(",") === "assertive,clear,polite,say",
    "announce members were " + Object.keys(announce).join(","));
  assert(globalThis.kit.service === undefined && announce.bridge === undefined && announce.mount === undefined,
    "announce exposed private controls or component state");
  assert(query("polite").length === 0 && query("assertive").length === 0,
    "announce allocated live regions before use or duplicate artifact reuse reran initialization");

  var first = announce.polite("First");
  var second = announce.say("Second", "polite");
  assert(first instanceof Promise && second instanceof Promise, "announce calls did not return Promises");
  assert(await first === false, "superseded polite announcement did not resolve false");
  assert(await second === true, "delivered polite announcement did not resolve true");
  var polite = query("polite")[0];
  assert(query("polite").length === 1 && query("assertive").length === 0 && polite.textContent === "Second",
    "polite live region was not created lazily exactly once");
  assert(polite.getAttribute("role") === "status" && polite.getAttribute("aria-live") === "polite" &&
    polite.getAttribute("aria-atomic") === "true" && polite.style.position === "absolute" &&
    polite.style.width === "1px" && polite.style.height === "1px" && polite.style.overflow === "hidden",
    "polite live region lost its hidden accessible contract");

  var cancelled = announce.assertive("Cancelled");
  assert(announce.clear("assertive") === true && await cancelled === false,
    "selected clear did not cancel a pending assertive announcement");
  assert(query("assertive").length === 0 && polite.textContent === "Second",
    "selected clear allocated a region or cleared the other channel");
  assert(await announce.assertive("Urgent") === true, "assertive announcement was not delivered");
  var assertive = query("assertive")[0];
  assert(query("assertive").length === 1 && assertive.textContent === "Urgent" &&
    assertive.getAttribute("role") === "alert" && assertive.getAttribute("aria-live") === "assertive",
    "assertive live region contract failed");

  polite.remove();
  assert(!polite.isConnected, "fixture did not remove the polite live region");
  assert(await announce.polite("Second") === true, "announcement after Morph-style removal was not delivered");
  var replacement = query("polite")[0];
  assert(query("polite").length === 1 && replacement !== polite && replacement.isConnected &&
    replacement.textContent === "Second", "removed live region was not recreated exactly once");

  var clearedPending = announce.polite("Never visible");
  assert(announce.clear() === true && await clearedPending === false,
    "global clear did not cancel a pending announcement");
  assert(replacement.textContent === "" && assertive.textContent === "",
    "global clear did not empty both connected channels");

  invalid(function () { announce.say(7); }, "non-string announcement");
  invalid(function () { announce.say("   "); }, "blank announcement");
  invalid(function () { announce.say("x".repeat(1025)); }, "oversized announcement");
  invalid(function () { announce.say("Hello", "urgent"); }, "unknown announcement mode");
  invalid(function () { announce.clear("urgent"); }, "unknown clear mode");
  assert(query("polite").length === 1 && query("assertive").length === 1,
    "validation or repeated artifact use duplicated document resources");
  assert(globalThis.__announceFetches === 0, "announce performed " + globalThis.__announceFetches + " fetches");
});
</script></body></html>`

const fullscreenServiceDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Fullscreen service contract</title><script>
globalThis.__fullscreenTarget = null;
globalThis.__fullscreenRequestCalls = 0;
globalThis.__fullscreenExitCalls = 0;
globalThis.__fullscreenEnabled = true;
Object.defineProperty(document, "fullscreenElement", {
  configurable: true,
  get: function () { return globalThis.__fullscreenTarget; }
});
Object.defineProperty(document, "fullscreenEnabled", {
  configurable: true,
  get: function () { return globalThis.__fullscreenEnabled; }
});
Object.defineProperty(Element.prototype, "requestFullscreen", {
  configurable: true,
  writable: true,
  value: function () {
    globalThis.__fullscreenRequestCalls++;
    globalThis.__fullscreenTarget = this;
    return Promise.resolve();
  }
});
Object.defineProperty(document, "exitFullscreen", {
  configurable: true,
  writable: true,
  value: function () {
    globalThis.__fullscreenExitCalls++;
    globalThis.__fullscreenTarget = null;
    return Promise.resolve();
  }
});
</script><script src=%q></script></head><body><button id="fullscreen-target">Target</button><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var fullscreen = globalThis.kit.fullscreen;
  async function rejected(run, code, label) {
    var promise = run();
    assert(promise instanceof Promise, label + " did not return a Promise");
    var error = null;
    try { await promise; } catch (caught) { error = caught; }
    assert(error && error.name === "KitFullscreenError" && error.code === code && Object.isFrozen(error) &&
      Object.keys(error).join(",") === "code,operation", label + " error was not normalized as " + code);
    return error;
  }

  assert(Object.keys(globalThis.kit).join(",") === "version,component,fullscreen",
    "fullscreen-only artifact keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(fullscreen), "fullscreen facade was mutable");
  assert(fullscreen.version === "1.0.0" && Object.keys(fullscreen).slice().sort().join(",") === "active,exit,request",
    "fullscreen surface was wrong");
  assert(globalThis.kit.service === undefined && fullscreen.toggle === undefined && fullscreen.element === undefined,
    "fullscreen exposed private or expanded controls");
  assert(fullscreen.active() === false, "fullscreen began active");

  var defaultRequest = fullscreen.request();
  assert(defaultRequest instanceof Promise && await defaultRequest === true,
    "default fullscreen request did not resolve true");
  assert(globalThis.__fullscreenTarget === document.documentElement && fullscreen.active() === true,
    "default fullscreen target was not documentElement");
  assert(await fullscreen.exit() === true && fullscreen.active() === false && globalThis.__fullscreenExitCalls === 1,
    "fullscreen exit did not resolve true and clear active state");
  assert(await fullscreen.exit() === false && globalThis.__fullscreenExitCalls === 1,
    "inactive fullscreen exit did not resolve false without invoking the API");

  var target = document.getElementById("fullscreen-target");
  assert(await fullscreen.request(target) === true && globalThis.__fullscreenTarget === target,
    "explicit Element fullscreen target failed");
  await fullscreen.exit();
  var invalid = fullscreen.request({ requestFullscreen: function () {} });
  assert(invalid instanceof Promise, "invalid fullscreen target threw synchronously");
  var invalidError = null;
  try { await invalid; } catch (error) { invalidError = error; }
  assert(invalidError instanceof TypeError, "non-Element fullscreen target did not reject TypeError");
  var foreign = document.implementation.createHTMLDocument("foreign").documentElement;
  var foreignError = null;
  try { await fullscreen.request(foreign); } catch (error) { foreignError = error; }
  assert(foreignError instanceof TypeError, "foreign-document Element was accepted");

  target.requestFullscreen = undefined;
  await rejected(function () { return fullscreen.request(target); }, "UNAVAILABLE", "unavailable request API");
  target.requestFullscreen = function () { throw new Error("permission denied"); };
  await rejected(function () { return fullscreen.request(target); }, "REQUEST_FAILED", "synchronous request failure");
  target.requestFullscreen = function () { return Promise.reject(new Error("permission denied")); };
  await rejected(function () { return fullscreen.request(target); }, "REQUEST_FAILED", "asynchronous request failure");
  globalThis.__fullscreenEnabled = false;
  await rejected(function () { return fullscreen.request(); }, "UNAVAILABLE", "disabled fullscreen API");
  globalThis.__fullscreenEnabled = true;

  globalThis.__fullscreenTarget = document.documentElement;
  document.exitFullscreen = undefined;
  await rejected(function () { return fullscreen.exit(); }, "UNAVAILABLE", "unavailable exit API");
  document.exitFullscreen = function () { throw new Error("exit denied"); };
  await rejected(function () { return fullscreen.exit(); }, "EXIT_FAILED", "synchronous exit failure");
  document.exitFullscreen = function () { return Promise.reject(new Error("exit denied")); };
  await rejected(function () { return fullscreen.exit(); }, "EXIT_FAILED", "asynchronous exit failure");
});
</script></body></html>`

const navigationServiceDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Navigation service contract</title><script>
globalThis.__navigationCalls = { back: 0, forward: 0, go: 0, push: 0, replace: 0 };
Object.defineProperty(history, "back", { configurable: true, writable: true, value: function () {
  globalThis.__navigationCalls.back++;
}});
Object.defineProperty(history, "forward", { configurable: true, writable: true, value: function () {
  globalThis.__navigationCalls.forward++;
}});
Object.defineProperty(history, "go", { configurable: true, writable: true, value: function () {
  globalThis.__navigationCalls.go++;
}});
Object.defineProperty(history, "pushState", { configurable: true, writable: true, value: function () {
  globalThis.__navigationCalls.push++;
}});
Object.defineProperty(history, "replaceState", { configurable: true, writable: true, value: function () {
  globalThis.__navigationCalls.replace++;
}});
</script><script src=%q></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var navigation = globalThis.kit.navigation;
  function failure(run, code, operation) {
    var error = null;
    try { run(); } catch (caught) { error = caught; }
    assert(error && error.name === "KitNavigationError" && error.code === code &&
      error.operation === operation && Object.isFrozen(error) && Object.keys(error).join(",") === "code,operation",
      operation + " error was not normalized as " + code);
  }

  assert(Object.keys(globalThis.kit).join(",") === "version,component,navigation",
    "navigation-only artifact keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(navigation), "navigation facade was mutable");
  assert(navigation.version === "1.0.0" && Object.keys(navigation).slice().sort().join(",") === "back,forward,reload",
    "navigation surface was wrong");
  assert(globalThis.kit.service === undefined && navigation.visit === undefined && navigation.push === undefined &&
    navigation.replace === undefined && navigation.drive === undefined,
    "navigation exposed Drive or history-state controls");
  assert(navigation.back() === undefined && navigation.forward() === undefined,
    "navigation primitives returned browser internals");
  assert(globalThis.__navigationCalls.back === 1 && globalThis.__navigationCalls.forward === 1 &&
    globalThis.__navigationCalls.go === 0 && globalThis.__navigationCalls.push === 0 &&
    globalThis.__navigationCalls.replace === 0, "navigation touched Drive/history state or wrong primitives");

  history.back = function () { throw new Error("blocked"); };
  failure(function () { navigation.back(); }, "FAILED", "back");
  history.forward = undefined;
  failure(function () { navigation.forward(); }, "UNAVAILABLE", "forward");
  assert(globalThis.__navigationCalls.go === 0 && globalThis.__navigationCalls.push === 0 &&
    globalThis.__navigationCalls.replace === 0, "navigation failures mutated history state");
});
</script></body></html>`

const navigationReloadDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Navigation reload contract</title><script src=%q></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var key = "kit-navigation-reload-contract";
  var visits = Number(sessionStorage.getItem(key) || "0");
  if (visits === 0) {
    sessionStorage.setItem(key, "1");
    var result = globalThis.kit.navigation.reload();
    assert(result === undefined, "reload returned browser internals");
    await new Promise(function () {});
    return;
  }
  sessionStorage.removeItem(key);
  assert(visits === 1 && Object.keys(globalThis.kit).join(",") === "version,component,navigation",
    "reload did not perform one normal document navigation");
});
</script></body></html>`

const announceRetentionDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Announce retention contract</title><script src=%q></script></head><body><script>
(function () {
  "use strict";
  var root = document.documentElement;
  function finish(status, error) {
    root.setAttribute("data-kit-retention-test", status);
    if (error) root.setAttribute("data-kit-retention-error", String(error && error.message || error));
  }
  function controls() {
    var refs = [];
    for (var index = 0; index < 128; index++) refs.push(new WeakRef({ index: index }));
    return refs;
  }
  function alive(refs) {
    var count = 0;
    refs.forEach(function (ref) { if (ref.deref() !== undefined) count++; });
    return count;
  }
  function collect(regionRef, controlRefs, pass) {
    var pressure = [];
    for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
    pressure = null;
    globalThis.gc();
    globalThis.gc();
    if (pass < 7) {
      setTimeout(function () { collect(regionRef, controlRefs, pass + 1); }, 0);
      return;
    }
    if (alive(controlRefs) !== 0) {
      finish("unsupported", "forced GC retained control objects");
      return;
    }
    if (regionRef.deref() !== undefined) {
      finish("failed", "announce retained a live region after direct removal");
      return;
    }
    finish("passed");
  }
  function run() {
    if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
      finish("unsupported", "WeakRef or forced gc() is unavailable");
      return;
    }
    globalThis.kit.announce.polite("Collect me").then(function (delivered) {
      if (!delivered) throw new Error("announce retention fixture was not delivered");
      var region = document.getElementById("kit-announcer-polite");
      if (!region) throw new Error("announce retention fixture did not create a region");
      var regionRef = new WeakRef(region);
      region.remove();
      region = null;
      if (document.getElementById("kit-announcer-polite")) {
        throw new Error("removed announce region remained connected");
      }
      setTimeout(function () { collect(regionRef, controls(), 0); }, 0);
    }).catch(function (error) { finish("failed", error); });
  }
  window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
  window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
  } else setTimeout(run, 0);
})();
</script></body></html>`
