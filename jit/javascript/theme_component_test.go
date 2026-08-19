package javascript

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const themeComponent200SHA256 = "c3910ce0d45a49a228033b8e9dac62ce1a40c2b33e37ec4db192c6fc2eb8baf5"
const themeComponent300SHA256 = "adb281e0bdef7b07cf678a40faf42fb51fa36b439da16577bbe577bd175f7e17"

// theme@1.0.0 remains historical only. HEAD blob
// 9f64ae21d98656943d27350ac32bb75d95c8004a has this exact source digest.
const themeComponentLegacy100SHA256 = "201486d3cb9cbe68a676b5a2db7f9c03eab41b31c8951863da08f577cc75f754"

func TestThemeLegacyV1RemainsUnembeddedAndUnselectable(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.component("theme", "1.0.0"); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("legacy theme@1.0.0 selection error = %v", err)
	}
	if _, err := embeddedDeliveryPackages.ReadFile("component/theme/1.0.0.js"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy theme@1.0.0 embed error = %v", err)
	}
	if themeComponent200SHA256 == themeComponentLegacy100SHA256 {
		t.Fatal("theme@2.0.0 reused the historical theme@1.0.0 bytes")
	}
}

func TestThemeComponentCatalogAndExactComposition(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("theme", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "theme", Version: "3.0.0"}) {
		t.Fatalf("theme default identity = %#v", component.identity)
	}
	if len(component.requires) != 1 || component.requires[0] != (ServiceVersion{Name: "appearance", Version: "1.0.0"}) {
		t.Fatalf("theme@3 dependencies = %#v, want appearance@1.0.0", component.requires)
	}
	appearance, err := catalog.service(ServiceVersion{Name: "appearance", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(appearance.actions, ","); got != "set,toggle,system" {
		t.Fatalf("appearance authored actions = %q", got)
	}
	if got := bytes.Count(component.source, []byte(`kit.component("theme"`)); got != 1 {
		t.Fatalf("theme@3 registration count = %d, want 1", got)
	}
	if got := ContentHash(component.source); got != themeComponent300SHA256 {
		t.Fatalf("theme@3.0.0 bytes changed: %s", got)
	}
	for _, required := range [][]byte{
		[]byte(`kit.appearance.set(mode)`),
		[]byte(`kit.appearance.toggle()`),
		[]byte(`kit.appearance.system()`),
		[]byte(`return kit.appearance.subscribe(`),
	} {
		if !bytes.Contains(component.source, required) {
			t.Fatalf("theme@3 lost adapter contract %q", required)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`), []byte(`document.`), []byte(`window.`),
		[]byte(`querySelector(`), []byte(`createElement(`), []byte(`innerHTML`),
		[]byte(`localStorage`), []byte(`matchMedia`), []byte(`classList`),
	} {
		if bytes.Contains(component.source, forbidden) {
			t.Fatalf("theme@3 contains appearance ownership %q", forbidden)
		}
	}

	composer := &Composer{catalog: catalog}
	fromHTML, err := composer.ComposeHTML([]byte(
		`<html data-kit-component="theme@3.0.0"></html>`,
	))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: "theme", Version: "3.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromHTML.Empty() || fromHTML.ContentHash != explicit.ContentHash ||
		!bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
		t.Fatal("canonical theme@3 and explicit selection produced different artifacts")
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.component("theme"`)); got != 1 {
		t.Fatalf("composed theme registration count = %d, want 1", got)
	}
	if !bytes.Contains(fromHTML.JavaScript, []byte(`components["theme"] = "3.0.0"`)) ||
		!bytes.Contains(fromHTML.JavaScript, []byte(`services["appearance"] = "1.0.0"`)) {
		t.Fatal("composed theme graph lost its exact version")
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.service("appearance"`)); got != 1 {
		t.Fatalf("theme@3 appearance registration count = %d, want 1", got)
	}

	legacy, err := catalog.component("theme", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.requires) != 0 {
		t.Fatalf("theme@2 dependencies = %#v, want none", legacy.requires)
	}
	if got := ContentHash(legacy.source); got != themeComponent200SHA256 {
		t.Fatalf("theme@2.0.0 bytes changed: %s", got)
	}
	legacyBundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "theme", Version: "2.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(legacyBundle.JavaScript, []byte(`components["theme"] = "2.0.0"`)) ||
		bytes.Contains(legacyBundle.JavaScript, []byte(`kit.service("appearance"`)) {
		t.Fatal("explicit theme@2 did not remain selectable and dependency-free")
	}
}

func TestThemeV2AndAppearanceDualOwnerGraphFailsClosed(t *testing.T) {
	const conflict = "theme@2.0.0 conflicts with document owner appearance@1.0.0"
	_, err := Build(BuildOptions{
		Profile: ProfileKit,
		Services: []Service{
			appearanceServicePackage(t),
		},
		Components: []ComponentVersion{{Name: "theme", Version: "2.0.0"}},
	})
	if err == nil || !strings.Contains(err.Error(), conflict) {
		t.Fatalf("direct dual-owner Build error = %v", err)
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	orders := [][]ComponentRef{
		{{Name: "app", Version: "1.0.0"}, {Name: "theme", Version: "2.0.0"}},
		{{Name: "theme", Version: "2.0.0"}, {Name: "app", Version: "1.0.0"}},
	}
	var first string
	for _, references := range orders {
		_, err = composer.ComposeStandalone(references, false)
		if err == nil || !strings.Contains(err.Error(), conflict) {
			t.Fatalf("composed dual-owner graph error = %v", err)
		}
		if first == "" {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("dual-owner error changed with discovery order:\nfirst: %s\nsecond: %s", first, err)
		}
	}
}

func TestBrowserThemeComponentPersistenceSystemAndCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping theme component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(
		`<html data-kit-component="theme" data-kit-version="2.0.0"></html>`,
	))
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/theme.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/theme.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(themeComponentBrowserDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/theme.html")
}

func TestBrowserThemeV3MirrorsAppearanceAndDelegatesActions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping theme@3 browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(
		`<html data-kit-component="theme" data-kit-version="3.0.0"></html>`,
	))
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/theme-v3.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/theme-v3.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(themeV3BrowserDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/theme-v3.html")
}

var themeComponentBrowserDocument = fmt.Sprintf(`<!doctype html>
<html lang="en" data-kit-component="theme" data-kit-version="2.0.0" data-kit-as="$theme">
<head><meta charset="utf-8"><title>Theme component contract</title>
<script>
  (function () {
    var dark = true;
    var listeners = [];
    var media = {
      get matches() { return dark; },
      addEventListener: function (type, listener) { if (type === "change") listeners.push(listener); },
      removeEventListener: function (type, listener) {
        var index = listeners.indexOf(listener); if (type === "change" && index >= 0) listeners.splice(index, 1);
      },
      dispatch: function (value) { dark = value; listeners.slice().forEach(function (fn) { fn({ matches: dark }); }); }
    };
    window.matchMedia = function (query) {
      if (query !== "(prefers-color-scheme: dark)") throw new Error("unexpected media query " + query);
      return media;
    };
    localStorage.removeItem("theme");
    window.__themeMedia = media;
    window.__themeListenerCount = function () { return listeners.length; };
  })();
</script></head>
<body>
  <button id="toggle" type="button" data-kit-click="$theme.toggle()">Toggle</button>
  <button id="system" type="button" data-kit-click="$theme.system()">System</button>
  <output id="mode" data-kit-text="mode">server</output>
  <output id="resolved" data-kit-text="resolved">server</output>
  <section id="cleanup-theme" data-kit-component="theme" data-kit-version="2.0.0"></section>
  <script src="/theme.js"></script>
  <script>
%s
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("mode").textContent === "system" &&
      document.getElementById("resolved").textContent === "dark" &&
      document.documentElement.classList.contains("dark");
  }, "system theme did not follow initial media preference");
  assert(localStorage.getItem("theme") === null, "initial system mode wrote storage");
  assert(window.__themeListenerCount() === 2, "theme media listeners were not registered exactly once per host");

  document.getElementById("toggle").click();
  await waitFor(function () {
    return document.getElementById("mode").textContent === "light" &&
      document.getElementById("resolved").textContent === "light" &&
      !document.documentElement.classList.contains("dark");
  }, "toggle did not resolve and apply light mode");
  assert(localStorage.getItem("theme") === "light", "toggle did not persist the canonical theme key");
  window.__themeMedia.dispatch(false);
  window.__themeMedia.dispatch(true);
  await __kitTestNextTurn();
  assert(!document.documentElement.classList.contains("dark"), "explicit mode followed a system change");

  window.dispatchEvent(new StorageEvent("storage", {
    key: "theme", oldValue: "light", newValue: "dark", storageArea: localStorage
  }));
  await waitFor(function () {
    return document.getElementById("mode").textContent === "dark" &&
      document.documentElement.classList.contains("dark");
  }, "storage event did not synchronize dark mode");

  document.getElementById("system").click();
  await waitFor(function () {
    return document.getElementById("mode").textContent === "system" &&
      document.getElementById("resolved").textContent === "dark";
  }, "system() did not restore system mode");
  assert(localStorage.getItem("theme") === "system", "system() did not persist system mode");
  window.__themeMedia.dispatch(false);
  await waitFor(function () {
    return document.getElementById("resolved").textContent === "light" &&
      !document.documentElement.classList.contains("dark");
  }, "system mode did not follow media change");

  var cleanupHost = document.getElementById("cleanup-theme");
  var cleanupWasDark = cleanupHost.classList.contains("dark");
  cleanupHost.remove();
  await waitFor(function () { return window.__themeListenerCount() === 1; },
    "theme listener survived component disposal");
  window.__themeMedia.dispatch(true);
  await __kitTestNextTurn();
  assert(cleanupHost.classList.contains("dark") === cleanupWasDark,
    "disposed media listener mutated the detached host");
  assert(document.documentElement.classList.contains("dark"), "live system theme stopped following media after sibling disposal");
});
  </script>
</body></html>`, browserHarness)

var themeV3BrowserDocument = fmt.Sprintf(`<!doctype html>
<html lang="en" data-kit-component="theme" data-kit-version="3.0.0" data-kit-as="$theme">
<head><meta charset="utf-8"><title>Theme v3 appearance adapter contract</title>
<script>
  (function () {
    var dark = true;
    var listeners = [];
    var media = {
      get matches() { return dark; },
      addEventListener: function (type, listener) {
        if (type === "change") listeners.push(listener);
      },
      removeEventListener: function (type, listener) {
        var index = listeners.indexOf(listener);
        if (type === "change" && index >= 0) listeners.splice(index, 1);
      },
      dispatch: function (value) {
        dark = value;
        listeners.slice().forEach(function (listener) { listener({ matches: dark }); });
      }
    };
    window.matchMedia = function (query) {
      if (query !== "(prefers-color-scheme: dark)") throw new Error("unexpected media query " + query);
      return media;
    };
    localStorage.removeItem("theme");
    window.__themeV3Media = media;
    window.__themeV3MediaListeners = function () { return listeners.length; };
  })();
</script></head>
<body>
  <button id="toggle-v3" type="button" data-kit-click="$theme.toggle()">Toggle</button>
  <button id="system-v3" type="button" data-kit-click="$theme.system()">System</button>
  <output id="mode-v3" data-kit-text="mode">server</output>
  <output id="resolved-v3" data-kit-text="resolved">server</output>
  <section id="theme-v3-sibling" data-kit-component="theme" data-kit-version="3.0.0">
    <output id="sibling-mode-v3" data-kit-text="mode">server</output>
  </section>
  <script src="/theme-v3.js"></script>
  <script>
%s
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var appearance = globalThis.kit.appearance;
  await waitFor(function () {
    return document.getElementById("mode-v3").textContent === "system" &&
      document.getElementById("resolved-v3").textContent === "dark" &&
      document.getElementById("sibling-mode-v3").textContent === "system";
  }, "theme@3 did not immediately mirror appearance");
  assert(appearance && appearance.version === "1.0.0", "theme@3 graph did not seal appearance@1");
  assert(window.__themeV3MediaListeners() === 1,
    "theme components duplicated the document appearance media listener");
  assert(document.documentElement.classList.contains("dark") &&
    document.documentElement.style.colorScheme === "dark",
    "appearance did not own the initial document presentation");
  assert(!document.getElementById("theme-v3-sibling").classList.contains("dark"),
    "theme adapter applied appearance to its component host");

  document.getElementById("toggle-v3").click();
  await waitFor(function () {
    return document.getElementById("mode-v3").textContent === "light" &&
      document.getElementById("resolved-v3").textContent === "light" &&
      document.getElementById("sibling-mode-v3").textContent === "light" &&
      !document.documentElement.classList.contains("dark");
  }, "theme@3 toggle did not delegate to appearance");
  assert(localStorage.getItem("theme") === "light", "delegated toggle did not persist appearance mode");

  window.__themeV3Media.dispatch(false);
  document.getElementById("system-v3").click();
  await waitFor(function () {
    return document.getElementById("mode-v3").textContent === "system" &&
      document.getElementById("resolved-v3").textContent === "light" &&
      document.getElementById("sibling-mode-v3").textContent === "system";
  }, "theme@3 system did not delegate to appearance");
  assert(localStorage.getItem("theme") === "system", "delegated system did not persist appearance mode");

  appearance.set("dark");
  await waitFor(function () {
    return document.getElementById("mode-v3").textContent === "dark" &&
      document.getElementById("resolved-v3").textContent === "dark" &&
      document.documentElement.classList.contains("dark");
  }, "trusted appearance update was not mirrored by theme@3");

  document.getElementById("theme-v3-sibling").remove();
  await __kitTestNextTurn();
  appearance.set("light");
  await waitFor(function () {
    return document.getElementById("mode-v3").textContent === "light" &&
      !document.documentElement.classList.contains("dark");
  }, "live theme@3 stopped mirroring after sibling cleanup");
  assert(window.__themeV3MediaListeners() === 1,
    "theme adapter cleanup changed document-lifetime appearance ownership");
});
  </script>
</body></html>`, browserHarness)
