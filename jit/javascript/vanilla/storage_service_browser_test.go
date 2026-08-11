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

func TestPreferencesExampleContract(t *testing.T) {
	artifact := buildPreferencesExampleArtifact(t)
	checked := readVanillaFile(t, "examples", "preferences", artifact.Name())
	if !bytes.Equal(checked, artifact.Bytes()) {
		t.Fatalf("checked preferences artifact %s is stale; rebuild it with examples/preferences/README.md", artifact.Name())
	}
	if artifact.SHA256() != ContentHash(checked) {
		t.Fatal("preferences artifact filename does not identify its exact bytes")
	}

	html := string(readVanillaFile(t, "examples", "preferences", "index.html"))
	matches := externalScriptRE.FindAllStringSubmatch(html, -1)
	if len(matches) != 1 {
		t.Fatalf("preferences external script count = %d, want one sealed artifact", len(matches))
	}
	source := matches[0][1]
	if source == "" {
		source = matches[0][2]
	}
	if want := "./" + artifact.Name(); source != want {
		t.Fatalf("preferences artifact URL = %q, want %q", source, want)
	}
	for _, required := range []string{
		`data-kit-component="preferences"`, `data-kit-version="1.0.0"`,
		`data-kit-click="choose('dark')"`, `data-kit-click="reset()"`, `max-w-8xl`,
		`id="preferences-message" role="status" aria-live="polite" aria-atomic="true"`,
		`bg-indigo-600 text-white`,
		`focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("preferences example lost %s", required)
		}
	}
	lower := strings.ToLower(html)
	for _, forbidden := range []string{"data-kit-app", "data-kit-hydrate", "<style", "<dialog", "<details"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("preferences example contains forbidden construct %q", forbidden)
		}
	}

	component := string(readVanillaFile(t, "examples", "preferences", "preferences.js"))
	if strings.Count(component, `kit.component("preferences"`) != 1 ||
		!strings.Contains(component, "kit.storage.get") ||
		!strings.Contains(component, "kit.storage.set") ||
		!strings.Contains(component, "kit.storage.remove") {
		t.Fatal("preferences component does not visibly use its sealed storage facade")
	}
	if strings.Contains(component, "kit.service") {
		t.Fatal("preferences component contains the private service registrar")
	}
}

func TestBrowserPreferencesExample(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping preferences example browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildPreferencesExampleArtifact(t)
	assetPath := "/examples/preferences/" + artifact.Name()
	stylePath := "/examples/" + exampleStylesheetName
	stylesheet := readVanillaFile(t, "examples", exampleStylesheetName)
	page := injectBrowserAssertions(t,
		readVanillaFile(t, "examples", "preferences", "index.html"),
		preferencesExampleAssertions)
	var artifactRequests atomic.Int64
	var styleRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			artifactRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case stylePath:
			styleRequests.Add(1)
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = response.Write(stylesheet)
		case "/examples/preferences/", "/examples/preferences/index.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(page)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/examples/preferences/")
	if got := artifactRequests.Load(); got != 1 {
		t.Fatalf("preferences artifact requests = %d, want 1", got)
	}
	if got := styleRequests.Load(); got != 1 {
		t.Fatalf("preferences stylesheet requests = %d, want 1", got)
	}
}

func TestSealedStorageServiceBuildContract(t *testing.T) {
	storage := storageServicePackage(t)
	preferences := readVanillaFile(t, "examples", "preferences", "preferences.js")
	withStorage := buildStorageArtifact(t, ProfileKit, []ComponentVersion{{Name: "preferences", Version: "1.0.0"}}, preferences)
	withoutStorage, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Components: []ComponentVersion{{Name: "preferences", Version: "1.0.0"}},
		Scripts:    []Script{{Name: "preferences", Source: preferences}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if withStorage.Name() == withoutStorage.Name() || bytes.Equal(withStorage.Bytes(), withoutStorage.Bytes()) {
		t.Fatal("adding storage@1.0.0 did not change the sealed artifact identity")
	}
	serviceOffset := bytes.Index(withStorage.Bytes(), storage.Source)
	componentOffset := bytes.Index(withStorage.Bytes(), preferences)
	if serviceOffset < 0 || componentOffset < 0 || serviceOffset >= componentOffset {
		t.Fatalf("sealed source order = service %d, component %d; want storage before component package", serviceOffset, componentOffset)
	}
	if withStorage.Release() != ReleaseVersion || withStorage.Profile() != ProfileKit {
		t.Fatalf("storage artifact metadata = release %q profile %q", withStorage.Release(), withStorage.Profile())
	}
}

func TestBrowserSealedStorageService(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sealed storage browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildStorageArtifact(t, ProfileHydrate,
		[]ComponentVersion{{Name: "storage-contract", Version: "1.0.0"}},
		[]byte(storageContractComponentSource))
	assetPath := "/assets/" + artifact.Name()
	var artifactRequests atomic.Int64
	var runtimePackageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			artifactRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/storage.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(storageContractDocument(assetPath)))
		case "/service/storage/1.0.0.js", "/storage.js":
			runtimePackageRequests.Add(1)
			http.Error(response, "storage must already be sealed into the artifact", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/storage.html")
	if got := artifactRequests.Load(); got != 1 {
		t.Fatalf("sealed storage artifact requests = %d, want 1", got)
	}
	if got := runtimePackageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched storage package at runtime %d times", got)
	}
}

func TestBrowserSealedServiceGraphGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sealed service graph guard in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	installed := buildServiceGuardArtifact(t, "1.0.0")
	different := buildServiceGuardArtifact(t, "1.0.1")
	assets := map[string][]byte{
		"/assets/" + installed.Name(): installed.Bytes(),
		"/assets/" + different.Name(): different.Bytes(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, ok := assets[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/service-guard.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(serviceGraphGuardDocument(installed, different)))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/service-guard.html")
}

func TestBrowserBaseArtifactDoesNotExposeServiceSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping base artifact browser service contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{Profile: ProfileKit})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/assets/" + artifact.Name():
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/base.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Base sealed API</title></head><body>
<script src="/assets/%s"></script>
<script>
%s
__runStandaloneKitTest(async function () {
  __kitTestAssert(Object.keys(globalThis.kit).join(",") === "version,component",
    "base artifact public keys were " + Object.keys(globalThis.kit).join(","));
  __kitTestAssert(globalThis.kit.storage === undefined, "base artifact exposed storage");
  __kitTestAssert(globalThis.kit.service === undefined, "base artifact exposed service registrar");
});
</script></body></html>`, artifact.Name(), browserHarness)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/base.html")
}

func TestBrowserSealedServiceReadonlyGetterContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sealed service getter contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	readonly := buildAccessorServiceArtifact(t, false)
	setter := buildAccessorServiceArtifact(t, true)
	assets := map[string][]byte{
		"/assets/" + readonly.Name(): readonly.Bytes(),
		"/assets/" + setter.Name():   setter.Bytes(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, ok := assets[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		switch request.URL.Path {
		case "/readonly-service.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(accessorServiceDocument(readonly, false)))
		case "/setter-service.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(accessorServiceDocument(setter, true)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	t.Run("readonly-getter", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/readonly-service.html")
	})
	t.Run("setter-rejected", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/setter-service.html")
	})
}

func storageServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "storage",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "storage", "1.0.0.js"),
	}
}

func buildPreferencesExampleArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Services:   []Service{storageServicePackage(t)},
		Components: []ComponentVersion{{Name: "preferences", Version: "1.0.0"}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "preferences",
			Service:   ServiceVersion{Name: "storage", Version: "1.0.0"},
		}},
		Scripts: []Script{{
			Name: "preferences", Source: readVanillaFile(t, "examples", "preferences", "preferences.js"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func buildStorageArtifact(t *testing.T, profile Profile, components []ComponentVersion, componentSource []byte) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:    profile,
		Services:   []Service{storageServicePackage(t)},
		Components: components,
		Scripts:    []Script{{Name: "storage-component", Source: componentSource}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func buildServiceGuardArtifact(t *testing.T, version string) Artifact {
	t.Helper()
	serviceSource := []byte(`;globalThis.__sealedServiceSourceRuns = (globalThis.__sealedServiceSourceRuns || 0) + 1;
kit.service("storage", Object.freeze({
  read: function () { return "sealed"; }
}));
`)
	componentSource := []byte(`;globalThis.__sealedComponentSourceRuns = (globalThis.__sealedComponentSourceRuns || 0) + 1;
kit.component("service-guard", {
  value: kit.storage.read()
});
`)
	artifact, err := Build(BuildOptions{
		Profile: ProfileKit,
		Services: []Service{{
			Name: "storage", Version: version, Source: serviceSource,
		}},
		Components: []ComponentVersion{{Name: "service-guard", Version: "1.0.0"}},
		Scripts:    []Script{{Name: "service-guard", Source: componentSource}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func buildAccessorServiceArtifact(t *testing.T, setter bool) Artifact {
	t.Helper()
	setterSource := ""
	if setter {
		setterSource = `, set: function () { globalThis.__serviceSetterRan = true; }`
	}
	source := []byte(fmt.Sprintf(`;var accessorNamespace = Object.create(null);
Object.defineProperty(accessorNamespace, "value", {
  enumerable: true,
  get: function () { return 7; }%s
});
kit.service("accessor", accessorNamespace);
`, setterSource))
	artifact, err := Build(BuildOptions{
		Profile: ProfileKit,
		Services: []Service{{
			Name: "accessor", Version: "1.0.0", Source: source,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func storageContractDocument(assetPath string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sealed storage contract</title>
  <script>
    localStorage.setItem("outside:keep", "safe");
    globalThis.__storageFetches = 0;
    var __storageRealFetch = globalThis.fetch;
    globalThis.fetch = function () {
      globalThis.__storageFetches++;
      return __storageRealFetch.apply(this, arguments);
    };
  </script>
  <script defer src=%q></script>
</head>
<body>
  <main data-kit-component="storage-contract" data-kit-version="1.0.0">
    <output id="storage-value" data-kit-text="value">server</output>
    <output id="storage-status" data-kit-text="status">server</output>
    <output id="storage-present" data-kit-text="present">server</output>
    <output id="storage-cleared" data-kit-text="cleared">server</output>
    <output id="storage-html-read" data-kit-text="kit.storage">HTML cannot read storage</output>
    <button id="storage-save" type="button" data-kit-click="save('theme', 'dark')">Save</button>
    <button id="storage-remove" type="button" data-kit-click="forget('theme')">Remove</button>
    <button id="storage-html-write" type="button" data-kit-click="kit.storage.set('html-poison', 'bad')">HTML write</button>
    <button id="storage-probe-html" type="button" data-kit-click="probe('html-poison')">Probe</button>
    <button id="storage-clear" type="button" data-kit-click="seedAndClear()">Clear</button>
  </main>
  <script>
%s
%s
  </script>
</body>
</html>`, assetPath, browserHarness, storageContractAssertions)
}

func serviceGraphGuardDocument(installed, different Artifact) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Service graph guard</title></head><body>
<main data-kit-component="service-guard" data-kit-version="1.0.0">
  <output id="service-guard-value" data-kit-text="value">server</output>
</main>
<script>
  globalThis.__sealedGraphErrors = [];
  window.addEventListener("error", function (event) {
    var message = String(event.error && event.error.message || event.message || "");
    if (message.indexOf("graph") >= 0 || message.indexOf("artifact") >= 0) {
      globalThis.__sealedGraphErrors.push(message);
      event.preventDefault();
    }
  });
</script>
<script src="/assets/%s"></script>
<script>globalThis.__sealedFirstKit = globalThis.kit;</script>
<script src="/assets/%s"></script>
<script>globalThis.__sealedSameKit = globalThis.kit === globalThis.__sealedFirstKit;</script>
<script src="/assets/%s"></script>
<script>
%s
%s
</script>
</body></html>`, installed.Name(), installed.Name(), different.Name(), browserHarness, serviceGraphGuardAssertions)
}

func accessorServiceDocument(artifact Artifact, rejected bool) string {
	setup := ""
	assertions := `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  assert(globalThis.__accessorErrors.length === 0, "readonly getter service threw");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,accessor",
    "readonly service public keys were " + Object.keys(globalThis.kit).join(","));
  var descriptor = Object.getOwnPropertyDescriptor(globalThis.kit.accessor, "value");
  assert(descriptor && typeof descriptor.get === "function" && descriptor.set === undefined,
    "readonly service getter became a value or setter");
  assert(globalThis.kit.accessor.value === 7 && Object.isFrozen(globalThis.kit.accessor),
    "readonly service getter/freeze contract failed");
});`
	if rejected {
		setup = `
  window.addEventListener("error", function (event) {
    globalThis.__accessorErrors.push(String(event.error && event.error.message || event.message || ""));
    event.preventDefault();
  });`
		assertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  assert(globalThis.__accessorErrors.length === 1, "setter service did not fail exactly once");
  assert(globalThis.kit === undefined, "setter service published a partial public kit");
  assert(globalThis.__serviceSetterRan !== true, "rejected service invoked its setter");
});`
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Service getter</title>
<script>globalThis.__accessorErrors = [];%s</script>
<script src="/assets/%s"></script></head><body>
<script>
%s
%s
</script></body></html>`, setup, artifact.Name(), browserHarness, assertions)
}

const storageContractComponentSource = `;kit.component("storage-contract", {
  value: "loading",
  status: "loading",
  present: false,
  cleared: 0,

  init: async function () {
    this.value = await kit.storage.get("theme", "system");
    this.present = await kit.storage.has("theme");
    this.status = "ready";
  },

  save: async function (key, value) {
    var stored = await kit.storage.set(key, value);
    this.value = await kit.storage.get(key, "missing");
    this.present = await kit.storage.has(key);
    this.status = stored ? "saved" : "failed";
  },

  forget: async function (key) {
    await kit.storage.remove(key);
    this.value = await kit.storage.get(key, "system");
    this.present = await kit.storage.has(key);
    this.status = "removed";
  },

  probe: async function (key) {
    this.present = await kit.storage.has(key);
    this.value = await kit.storage.get(key, "missing");
    this.status = "probed";
  },

  seedAndClear: async function () {
    await kit.storage.set("first", { value: 1 });
    await kit.storage.set("second", [2, 3]);
    this.cleared = await kit.storage.clear();
    this.present = await kit.storage.has("first");
    this.value = await kit.storage.get("second", "cleared");
    this.status = "cleared";
  }
});
`

const preferencesExampleAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("preferences-message").textContent.trim() === "Preference ready";
  }, "preferences init did not read storage");
  assert(document.getElementById("preferences-mode").textContent.trim() === "system",
    "preferences fallback was not system");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,storage",
    "preferences public API was " + Object.keys(globalThis.kit).join(","));
  assert(globalThis.kit.service === undefined && Object.isFrozen(globalThis.kit.storage),
    "preferences artifact leaked a mutable registrar");

  document.getElementById("preferences-dark").click();
  await waitFor(function () {
    return document.getElementById("preferences-mode").textContent.trim() === "dark" &&
      document.getElementById("preferences-message").textContent.trim() === "Saved dark";
  }, "preferences action did not persist dark");
  document.getElementById("preferences-reset").click();
  await waitFor(function () {
    return document.getElementById("preferences-mode").textContent.trim() === "system" &&
      document.getElementById("preferences-message").textContent.trim() === "Reset to system";
  }, "preferences reset did not remove storage value");
});`

const storageContractAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;

  await waitFor(function () {
    return document.getElementById("storage-status").textContent.trim() === "ready";
  }, "storage component init did not settle");
  assert(document.getElementById("storage-value").textContent.trim() === "system",
    "storage fallback did not reach component init");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,storage",
    "storage artifact public keys were " + Object.keys(globalThis.kit).join(","));
  assert(globalThis.kit.service === undefined, "service registrar escaped package assembly");
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(globalThis.kit.storage),
    "public storage facade is mutable");
  assert(Object.keys(globalThis.kit.storage).join(",") === "get,set,remove,has,clear",
    "storage facade members were " + Object.keys(globalThis.kit.storage).join(","));
  var serviceVersion = Object.getOwnPropertyDescriptor(globalThis.kit.storage, "version");
  assert(serviceVersion && serviceVersion.value === "1.0.0" && serviceVersion.enumerable === false,
    "storage namespace lost its hidden exact version");
  assert(document.getElementById("storage-html-read").textContent.trim() === "HTML cannot read storage",
    "authored binding read the trusted kit facade");

  document.getElementById("storage-save").click();
  await waitFor(function () {
    return document.getElementById("storage-status").textContent.trim() === "saved" &&
      document.getElementById("storage-value").textContent.trim() === "dark" &&
      document.getElementById("storage-present").textContent.trim() === "true";
  }, "component action did not save through lexical kit.storage");

  document.getElementById("storage-html-write").click();
  document.getElementById("storage-probe-html").click();
  await waitFor(function () {
    return document.getElementById("storage-status").textContent.trim() === "probed";
  }, "component did not probe the HTML storage key");
  assert(document.getElementById("storage-present").textContent.trim() === "false" &&
    document.getElementById("storage-value").textContent.trim() === "missing",
    "authored HTML called kit.storage directly");

  document.getElementById("storage-save").click();
  await waitFor(function () { return document.getElementById("storage-status").textContent.trim() === "saved"; },
    "storage did not save again");
  document.getElementById("storage-remove").click();
  await waitFor(function () {
    return document.getElementById("storage-status").textContent.trim() === "removed" &&
      document.getElementById("storage-value").textContent.trim() === "system" &&
      document.getElementById("storage-present").textContent.trim() === "false";
  }, "storage remove/has contract failed");

  document.getElementById("storage-clear").click();
  await waitFor(function () {
    return document.getElementById("storage-status").textContent.trim() === "cleared";
  }, "storage clear did not settle");
  assert(Number(document.getElementById("storage-cleared").textContent.trim()) >= 2,
    "storage clear did not report its owned keys");
  assert(document.getElementById("storage-present").textContent.trim() === "false" &&
    document.getElementById("storage-value").textContent.trim() === "cleared",
    "storage clear left owned values behind");
  assert(localStorage.getItem("outside:keep") === "safe", "storage clear crossed its key namespace");
  assert(globalThis.__storageFetches === 0, "Hydrate/storage fetched a runtime package dynamically");

  var graphDescriptor = Object.getOwnPropertyDescriptor(globalThis.kit, Symbol.for("kitjs:graph"));
  assert(graphDescriptor && graphDescriptor.enumerable === false, "sealed graph is missing or enumerable");
  assert(Object.isFrozen(graphDescriptor.value) && Object.isFrozen(graphDescriptor.value.components) &&
    Object.isFrozen(graphDescriptor.value.services), "sealed graph metadata is mutable");
  assert(graphDescriptor.value.services.storage === "1.0.0", "sealed graph lost storage@1.0.0");
});`

const serviceGraphGuardAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("service-guard-value").textContent.trim() === "sealed";
  }, "sealed service graph did not mount");
  assert(globalThis.__sealedGraphErrors.length === 1,
    "different service graph did not throw exactly once: " + globalThis.__sealedGraphErrors.join(" | "));
  assert(globalThis.__sealedSameKit && globalThis.kit === globalThis.__sealedFirstKit,
    "same or rejected service artifact replaced public kit");
  assert(globalThis.__sealedServiceSourceRuns === 1,
    "same/rejected artifact reran service source " + globalThis.__sealedServiceSourceRuns + " times");
  assert(globalThis.__sealedComponentSourceRuns === 1,
    "same/rejected artifact reran component source " + globalThis.__sealedComponentSourceRuns + " times");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,storage",
    "sealed service graph public keys were " + Object.keys(globalThis.kit).join(","));
  assert(globalThis.kit.service === undefined, "sealed service graph exposed registrar");
  var descriptor = Object.getOwnPropertyDescriptor(globalThis.kit, Symbol.for("kitjs:graph"));
  assert(descriptor && descriptor.enumerable === false, "service graph descriptor escaped publicly");
  assert(Object.isFrozen(descriptor.value) && Object.isFrozen(descriptor.value.services) &&
    descriptor.value.services.storage === "1.0.0", "installed service graph is mutable or incorrect");
});`

func TestStorageServiceSourceIsClosedAndReadable(t *testing.T) {
	source := string(storageServicePackage(t).Source)
	if strings.Count(source, `kit.service("storage"`) != 1 {
		t.Fatalf("storage package registrar count = %d, want one", strings.Count(source, `kit.service("storage"`))
	}
	for _, method := range []string{"get", "set", "remove", "has", "clear"} {
		if !strings.Contains(source, method+":") {
			t.Fatalf("storage package source does not visibly define %s", method)
		}
	}
}
