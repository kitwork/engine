package javascript

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	jittheme "github.com/kitwork/engine/jit/theme"
)

const appearanceService100SHA256 = "5ca23562929a4c632ebe5dc04635026421323280e9259b4f801adb110a79ad49"

func TestAppearanceServiceSourceIsClosedDocumentCapability(t *testing.T) {
	source := readVanillaFile(t, "service", "appearance", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
		t.Fatal("appearance@1.0.0 is not a sealable classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("appearance"`)); got != 1 {
		t.Fatalf("appearance registration count = %d, want 1", got)
	}
	if got := ContentHash(source); got != appearanceService100SHA256 {
		t.Fatalf("appearance@1.0.0 bytes changed: %s", got)
	}
	for _, required := range [][]byte{
		[]byte(`var storageKey = "theme"`),
		[]byte(`document.documentElement`),
		[]byte(`global.matchMedia(mediaQuery)`),
		[]byte(`root.style.colorScheme = resolved`),
		[]byte(`global.addEventListener("storage", storageChange)`),
		[]byte(`if (attached) return`),
		[]byte(`Object.freeze({ mode: mode, resolved: resolved })`),
	} {
		if !bytes.Contains(source, required) {
			t.Fatalf("appearance source lost contract %q", required)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.component(`), []byte(`kit.storage`), []byte(`"kit:theme"`),
		[]byte(`sessionStorage`), []byte(`fetch(`), []byte(`XMLHttpRequest`),
		[]byte(`querySelector(`), []byte(`createElement(`), []byte(`innerHTML`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("appearance source contains forbidden coupling %q", forbidden)
		}
	}

	service := appearanceServicePackage(t)
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{service}})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(artifact.Bytes(), service.Source); got != 1 {
		t.Fatalf("appearance source count in artifact = %d, want 1", got)
	}
	if !strings.Contains(string(artifact.Bytes()), `services["appearance"] = "1.0.0";`) {
		t.Fatal("appearance graph identity is missing")
	}
}

func TestBrowserAppearanceServiceStatePersistenceAndLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping appearance browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	artifact, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{appearanceServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = response.Write(artifact.Bytes())
		case "/appearance.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, appearanceServiceDocument, assetPath, assetPath)
		case "/service/appearance/1.0.0.js", "/appearance.js", "/appearance.service.js":
			packageRequests.Add(1)
			http.Error(response, "appearance must already be sealed", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/appearance.html")
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched sealed appearance package %d times", got)
	}
}

func TestBrowserAppearanceMatchesPrepaintNormalizationAndFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping appearance/prepaint browser parity in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	artifact, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{appearanceServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	type scenario struct {
		stored       *string
		mediaDark    bool
		storageThrow bool
		mediaThrow   bool
		mode         string
		resolved     string
	}
	value := func(input string) *string { return &input }
	scenarios := map[string]scenario{
		"explicit-light": {stored: value("light"), mediaDark: true, mode: "light", resolved: "light"},
		"explicit-dark":  {stored: value("dark"), mode: "dark", resolved: "dark"},
		"uppercase-dark": {stored: value("DARK"), mode: "dark", resolved: "dark"},
		"system-dark":    {stored: value("system"), mediaDark: true, mode: "system", resolved: "dark"},
		"missing-dark":   {mediaDark: true, mode: "system", resolved: "dark"},
		"invalid-light":  {stored: value("sepia"), mode: "system", resolved: "light"},
		"storage-throws": {mediaDark: true, storageThrow: true, mode: "system", resolved: "dark"},
		"media-throws":   {stored: value("system"), mediaDark: true, mediaThrow: true, mode: "system", resolved: "light"},
	}
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == assetPath {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
			return
		}
		const prefix = "/prepaint/"
		if strings.HasPrefix(request.URL.Path, prefix) && strings.HasSuffix(request.URL.Path, ".html") {
			name := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, prefix), ".html")
			scenario, exists := scenarios[name]
			if exists {
				response.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = response.Write([]byte(appearancePrepaintPage(assetPath, scenario.stored,
					scenario.mediaDark, scenario.storageThrow, scenario.mediaThrow,
					scenario.mode, scenario.resolved)))
				return
			}
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	for name := range scenarios {
		name := name
		t.Run(name, func(t *testing.T) {
			runVanillaBrowser(t, browser, server.URL+"/prepaint/"+name+".html")
		})
	}
}

func appearancePrepaintPage(assetPath string, stored *string, mediaDark, storageThrow, mediaThrow bool, mode, resolved string) string {
	storedValue := "null"
	if stored != nil {
		storedValue = strconv.Quote(*stored)
	}
	storageResult := "return " + storedValue + ";"
	if storageThrow {
		storageResult = `throw new Error("storage blocked");`
	}
	mediaResult := fmt.Sprintf(`return { matches: %t, addEventListener: function () {}, removeEventListener: function () {} };`, mediaDark)
	if mediaThrow {
		mediaResult = `throw new Error("media blocked");`
	}
	page := fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Appearance prepaint parity</title><script>
(function () {
  var getItem = Storage.prototype.getItem;
  Storage.prototype.getItem = function (key) {
    if (key === "theme") { %s }
    return getItem.call(this, key);
  };
  globalThis.matchMedia = function (query) {
    if (query !== "(prefers-color-scheme: dark)") throw new Error("unexpected media query " + query);
    %s
  };
})();
</script><script data-kitwork-jit="theme"></script><script>
globalThis.__appearancePrepaint = {
  dark: document.documentElement.classList.contains("dark"),
  colorScheme: document.documentElement.style.colorScheme
};
</script><script src=%q></script></head><body><script>
%s
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var before = globalThis.__appearancePrepaint;
  var appearance = globalThis.kit.appearance;
  var current = appearance.snapshot();
  var expectedMode = %q;
  var expectedResolved = %q;
  assert(before.dark === (expectedResolved === "dark"),
    "prepaint dark class did not resolve " + expectedResolved);
  assert(before.colorScheme === expectedResolved,
    "prepaint color-scheme was " + before.colorScheme + ", want " + expectedResolved);
  assert(current.mode === expectedMode && current.resolved === expectedResolved,
    "appearance resolved " + current.mode + "/" + current.resolved +
      ", want " + expectedMode + "/" + expectedResolved);
  assert(document.documentElement.classList.contains("dark") === before.dark &&
    document.documentElement.style.colorScheme === before.colorScheme,
    "appearance changed the normalized prepaint result during startup");
});
</script></body></html>`, storageResult, mediaResult, assetPath, browserHarness, mode, resolved)
	return jittheme.Render(page)
}

func appearanceServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "appearance",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "appearance", "1.0.0.js"),
	}
}

const appearanceServiceDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Appearance service contract</title><script>
(function () {
  "use strict";
  var dark = true;
  var mediaListeners = [];
  var probe = globalThis.__appearanceProbe = {
    mediaAdds: 0,
    storageAdds: 0,
    reports: []
  };
  var media = {
    get matches() { return dark; },
    addEventListener: function (type, listener) {
      if (type !== "change") throw new Error("unexpected media event " + type);
      probe.mediaAdds++;
      mediaListeners.push(listener);
    },
    removeEventListener: function () {
      throw new Error("document-lifetime appearance listener was removed");
    },
    dispatch: function (value) {
      dark = value;
      mediaListeners.slice().forEach(function (listener) { listener({ matches: dark }); });
    }
  };
  globalThis.matchMedia = function (query) {
    if (query !== "(prefers-color-scheme: dark)") throw new Error("unexpected media query " + query);
    return media;
  };
  var add = globalThis.addEventListener;
  globalThis.addEventListener = function (type, listener, options) {
    if (type === "storage") probe.storageAdds++;
    return add.call(this, type, listener, options);
  };
  globalThis.reportError = function (error) {
    probe.reports.push(String(error && error.message || error));
  };
  localStorage.removeItem("theme");
  globalThis.__appearanceMedia = media;
})();
</script><script src=%q></script><script src=%q></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var appearance = globalThis.kit.appearance;
  var probe = globalThis.__appearanceProbe;
  function state(mode, resolved, label) {
    var value = appearance.snapshot();
    assert(Object.isFrozen(value), label + " snapshot was mutable");
    assert(value.mode === mode && value.resolved === resolved,
      label + " snapshot was " + value.mode + "/" + value.resolved);
    assert(appearance.mode === mode && appearance.resolved === resolved,
      label + " getters did not mirror the snapshot");
    assert(document.documentElement.classList.contains("dark") === (resolved === "dark"),
      label + " did not own the root dark class");
    assert(document.documentElement.style.colorScheme === resolved,
      label + " color-scheme was " + document.documentElement.style.colorScheme);
    return value;
  }

  assert(Object.keys(globalThis.kit).join(",") === "version,component,appearance",
    "appearance-only artifact keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(appearance),
    "appearance facade was mutable");
  assert(appearance.version === "1.0.0", "appearance version was " + appearance.version);
  assert(Object.keys(appearance).slice().sort().join(",") ===
    "mode,resolved,set,snapshot,subscribe,system,toggle",
    "appearance members were " + Object.keys(appearance).join(","));
  var modeDescriptor = Object.getOwnPropertyDescriptor(appearance, "mode");
  var resolvedDescriptor = Object.getOwnPropertyDescriptor(appearance, "resolved");
  assert(modeDescriptor && typeof modeDescriptor.get === "function" && modeDescriptor.set === undefined &&
    resolvedDescriptor && typeof resolvedDescriptor.get === "function" && resolvedDescriptor.set === undefined,
    "appearance state was not exposed through readonly getters");
  assert(globalThis.kit.service === undefined && appearance.bridge === undefined,
    "appearance leaked service registration or bridge controls");
  assert(probe.mediaAdds === 1 && probe.storageAdds === 1,
    "duplicate artifact reuse attached " + probe.mediaAdds + " media and " + probe.storageAdds + " storage listeners");

  var initial = state("system", "dark", "initial system");
  assert(localStorage.getItem("theme") === null, "initial system mode wrote storage");
  assert(appearance.snapshot() === initial, "snapshot identity changed without a state transition");

  var inactiveDeliveries = [];
  var unsubscribe = appearance.subscribe(function (value) { inactiveDeliveries.push(value); });
  assert(inactiveDeliveries.length === 1 && inactiveDeliveries[0] === initial,
    "subscribe did not deliver the current snapshot immediately");
  unsubscribe();
  unsubscribe();

  var deliveries = [];
  var activeCleanup = appearance.subscribe(function (value) { deliveries.push(value); });
  assert(deliveries.length === 1 && deliveries[0] === initial,
    "active subscription did not deliver immediately");
  var light = appearance.set("LIGHT");
  assert(light === appearance.snapshot(), "set did not synchronously return the published snapshot");
  state("light", "light", "explicit light");
  assert(localStorage.getItem("theme") === "light", "set did not persist the bare theme key");
  assert(inactiveDeliveries.length === 1, "idempotently removed subscriber received another delivery");
  assert(deliveries.length === 2 && deliveries[1] === light, "set did not notify the active subscriber");

  var sameLight = appearance.set("light");
  assert(sameLight === light && deliveries.length === 2,
    "idempotent set republished an unchanged state");
  var dark = appearance.toggle();
  assert(dark === appearance.snapshot(), "toggle did not synchronously return its snapshot");
  state("dark", "dark", "toggle dark");
  assert(localStorage.getItem("theme") === "dark", "toggle did not persist dark mode");

  var system = appearance.system();
  assert(system === appearance.snapshot(), "system did not synchronously return its snapshot");
  state("system", "dark", "restored system");
  assert(localStorage.getItem("theme") === "system", "system did not persist system mode");
  globalThis.__appearanceMedia.dispatch(false);
  state("system", "light", "system media change");

  globalThis.dispatchEvent(new StorageEvent("storage", {
    key: "theme", oldValue: "system", newValue: "dark", storageArea: localStorage
  }));
  state("dark", "dark", "cross-tab dark");
  globalThis.__appearanceMedia.dispatch(false);
  state("dark", "dark", "explicit mode after media change");
  globalThis.dispatchEvent(new StorageEvent("storage", {
    key: "theme", oldValue: "dark", newValue: null, storageArea: localStorage
  }));
  state("system", "light", "cross-tab storage removal");

  var beforeCleanup = deliveries.length;
  activeCleanup();
  activeCleanup();
  appearance.set("dark");
  state("dark", "dark", "post-cleanup explicit dark");
  assert(deliveries.length === beforeCleanup, "cleaned subscriber received another delivery");
  var invalidSubscribe = false;
  try { appearance.subscribe(null); } catch (error) { invalidSubscribe = error instanceof TypeError; }
  assert(invalidSubscribe, "subscribe accepted a non-function listener");
  assert(probe.reports.length === 0, "appearance reported unexpected errors: " + probe.reports.join(" | "));
});
</script></body></html>`
