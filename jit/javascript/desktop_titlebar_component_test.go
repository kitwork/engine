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

const desktopTitlebarComponent100SHA256 = "f29d8897d7b875d0d38a50a1cd45359482dde4581abda07583c47fa635554be3"
const desktopTitlebarComponent110SHA256 = "f3e2bfc9b25e75635711536fe425c470e685590d67ad9e22a59c1adef6d604f1"

func TestDesktopTitlebarCatalogSealsPrivateWindowService(t *testing.T) {
	source := readVanillaFile(t, "component", "desktop-titlebar", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("desktop-titlebar@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("desktop-titlebar"`)); got != 1 {
		t.Fatalf("desktop-titlebar@1.0.0 registration count = %d, want 1", got)
	}
	if got := ContentHash(source); got != desktopTitlebarComponent100SHA256 {
		t.Fatalf("desktop-titlebar@1.0.0 bytes changed: %s", got)
	}
	for _, marker := range [][]byte{
		[]byte(`maximized: false`),
		[]byte(`context.listen(host, "mousedown"`),
		[]byte(`context.listen(host, "dblclick"`),
		[]byte(`[data-titlebar-drag]`),
		[]byte(`[data-titlebar-no-drag]`),
		[]byte(`kit.window.drag()`),
		[]byte(`kit.window.isMaximized()`),
		[]byte(`kit.window.minimize()`),
		[]byte(`kit.window.maximize()`),
		[]byte(`kit.window.restore()`),
		[]byte(`kit.window.close()`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("desktop-titlebar@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`), []byte(`document.`), []byte(`globalThis.`),
		[]byte(`addEventListener(`), []byte(`querySelector(`), []byte(`innerHTML`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("desktop-titlebar@1.0.0 contains package-scope coupling %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("desktop-titlebar", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "desktop-titlebar", Version: "1.0.0"}) {
		t.Fatalf("desktop-titlebar default identity = %#v", component.identity)
	}
	if len(component.requires) != 1 || component.requires[0] != (ServiceVersion{Name: "window", Version: "1.0.0"}) {
		t.Fatalf("desktop-titlebar dependencies = %#v, want window@1.0.0", component.requires)
	}
	windowService, err := catalog.service(ServiceVersion{Name: "window", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(windowService.actions) != 0 || validAuthoredServiceAction("window", "drag") ||
		validAuthoredServiceAction("window", "isMaximized") ||
		appGrantsAuthoredService("1.1.0", "window") {
		t.Fatalf("private window service escaped authored policy: actions=%#v", windowService.actions)
	}

	assembly := desktopTitlebarStagedAssembly(t)
	if len(assembly.Services) != 1 || assembly.Services[0].Package() != "window" ||
		len(assembly.Components) != 1 || assembly.Components[0].Package() != "desktop-titlebar" {
		t.Fatalf("desktop titlebar staged graph services=%#v components=%#v", assembly.Services, assembly.Components)
	}
	if got := bytes.Count(assembly.Services[0].Bytes(), []byte(`kit.service("window"`)); got != 1 {
		t.Fatalf("staged titlebar window service registrations = %d, want 1", got)
	}
	if got := bytes.Count(assembly.Components[0].Bytes(), []byte(`kit.component("desktop-titlebar"`)); got != 1 {
		t.Fatalf("staged titlebar component registrations = %d, want 1", got)
	}
	graph := assembly.Graph.Bytes()
	for _, marker := range [][]byte{
		[]byte(`services["window"] = "1.0.0"`),
		[]byte(`components["desktop-titlebar"] = "1.0.0"`),
	} {
		if !bytes.Contains(graph, marker) {
			t.Fatalf("desktop titlebar graph lost %q", marker)
		}
	}
}

func TestDesktopTitlebar110AddsGuardedBrowserNavigation(t *testing.T) {
	source := readVanillaFile(t, "component", "desktop-titlebar", "1.1.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("desktop-titlebar@1.1.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("desktop-titlebar"`)); got != 1 {
		t.Fatalf("desktop-titlebar@1.1.0 registration count = %d, want 1", got)
	}
	if got := ContentHash(source); got != desktopTitlebarComponent110SHA256 {
		t.Fatalf("desktop-titlebar@1.1.0 bytes changed: %s", got)
	}
	for _, marker := range [][]byte{
		[]byte(`canGoBack: false`),
		[]byte(`canGoForward: false`),
		[]byte(`globalThis.navigation`),
		[]byte(`context.listen(navigation, "currententrychange"`),
		[]byte(`if (this.canGoBack !== true) return false`),
		[]byte(`if (this.canGoForward !== true) return false`),
		[]byte(`kit.navigation.back()`),
		[]byte(`kit.navigation.forward()`),
		[]byte(`kit.navigation.reload()`),
		[]byte(`kit.window.drag()`),
		[]byte(`kit.window.isMaximized()`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("desktop-titlebar@1.1.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`history.length`), []byte(`history.go(`), []byte(`kit.service(`),
		[]byte(`document.querySelector(`), []byte(`addEventListener(`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("desktop-titlebar@1.1.0 contains guessed state or unmanaged lifecycle %q", forbidden)
		}
	}

	// A new managed identity must not mutate the already published 1.0.0 package.
	if got := ContentHash(readVanillaFile(t, "component", "desktop-titlebar", "1.0.0.js")); got != desktopTitlebarComponent100SHA256 {
		t.Fatalf("desktop-titlebar@1.0.0 bytes changed while adding 1.1.0: %s", got)
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("desktop-titlebar", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "desktop-titlebar", Version: "1.1.0"}) {
		t.Fatalf("desktop-titlebar@1.1.0 identity = %#v", component.identity)
	}
	wantRequires := []ServiceVersion{
		{Name: "navigation", Version: "1.0.0"},
		{Name: "window", Version: "1.0.0"},
	}
	if len(component.requires) != len(wantRequires) {
		t.Fatalf("desktop-titlebar@1.1.0 dependencies = %#v, want %#v", component.requires, wantRequires)
	}
	for index := range wantRequires {
		if component.requires[index] != wantRequires[index] {
			t.Fatalf("desktop-titlebar@1.1.0 dependencies = %#v, want %#v", component.requires, wantRequires)
		}
	}

	assembly := desktopTitlebarStagedAssemblyVersion(t, "1.1.0")
	if len(assembly.Services) != 2 || len(assembly.Components) != 1 ||
		assembly.Components[0].Package() != "desktop-titlebar" {
		t.Fatalf("desktop titlebar 1.1 staged graph services=%#v components=%#v", assembly.Services, assembly.Components)
	}
	servicePackages := []string{assembly.Services[0].Package(), assembly.Services[1].Package()}
	if strings.Join(servicePackages, ",") != "navigation,window" {
		t.Fatalf("desktop titlebar 1.1 services = %v, want navigation,window", servicePackages)
	}
	graph := assembly.Graph.Bytes()
	for _, marker := range [][]byte{
		[]byte(`services["navigation"] = "1.0.0"`),
		[]byte(`services["window"] = "1.0.0"`),
		[]byte(`components["desktop-titlebar"] = "1.1.0"`),
		[]byte(`grants["desktop-titlebar"]["navigation"] = "1.0.0"`),
		[]byte(`grants["desktop-titlebar"]["window"] = "1.0.0"`),
	} {
		if !bytes.Contains(graph, marker) {
			t.Fatalf("desktop titlebar 1.1 graph lost %q", marker)
		}
	}
}

func TestBrowserDesktopTitlebarOwnsNativeWindowGestures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping desktop titlebar browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	assembly := desktopTitlebarStagedAssembly(t)
	assets := make(map[string][]byte, len(assembly.Artifacts()))
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	page := desktopTitlebarStagedDocument(assembly)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/")
}

func TestBrowserDesktopTitlebar110TracksAndGuardsNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping desktop titlebar navigation browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	assembly := desktopTitlebarStagedAssemblyVersion(t, "1.1.0")
	assets := make(map[string][]byte, len(assembly.Artifacts()))
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	page := desktopTitlebar110StagedDocument(assembly)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/")
}

func TestBrowserDesktopTitlebarSurvivesRepeatedDriveMorphs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping desktop titlebar Drive lifecycle contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	base, extended := desktopTitlebarDriveAssemblies(t)
	contractSource := []byte(desktopTitlebarDrivePrelude + "\n" + browserHarness + "\n" + desktopTitlebarDriveAssertions)
	contractIntegrity := driveScriptIntegrity(contractSource)
	assets := make(map[string][]byte)
	for _, assembly := range []StagedAssembly{base, extended} {
		for _, artifact := range assembly.Artifacts() {
			path := "/jit/" + artifact.Name()
			if previous, exists := assets[path]; exists && !bytes.Equal(previous, artifact.Bytes()) {
				t.Fatalf("desktop titlebar Drive fixture collision at %s", path)
			}
			assets[path] = artifact.Bytes()
		}
	}

	var aDrive atomic.Int64
	var aFull atomic.Int64
	var bDrive atomic.Int64
	var bFull atomic.Int64
	var cDrive atomic.Int64
	var cFull atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/desktop-titlebar-drive-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		}
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = response.Write(source)
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
			writeStagedDriveHTML(response, desktopTitlebarDriveDocument(base, "a", "header", false, contractIntegrity))
		case "/b":
			if !drive {
				bFull.Add(1)
				response.Header().Set("Set-Cookie", "desktop_titlebar_unexpected_b_full=1; Path=/; SameSite=Lax")
				response.WriteHeader(http.StatusNoContent)
				return
			}
			bDrive.Add(1)
			writeStagedDriveHTML(response, desktopTitlebarDriveDocument(extended, "b", "header", true, contractIntegrity))
		case "/c":
			if !drive {
				cFull.Add(1)
				response.Header().Set("Set-Cookie", "desktop_titlebar_unexpected_c_full=1; Path=/; SameSite=Lax")
				response.WriteHeader(http.StatusNoContent)
				return
			}
			cDrive.Add(1)
			writeStagedDriveHTML(response, desktopTitlebarDriveDocument(base, "c", "section", false, contractIntegrity))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/a")
	if got := aFull.Load(); got != 1 {
		t.Errorf("desktop titlebar initial full requests = %d, want 1", got)
	}
	if got := aDrive.Load(); got != 1 {
		t.Errorf("desktop titlebar A Drive requests = %d, want 1", got)
	}
	if got := bDrive.Load(); got != 2 {
		t.Errorf("desktop titlebar B Drive requests = %d, want 2", got)
	}
	if got := cDrive.Load(); got != 2 {
		t.Errorf("desktop titlebar C Drive requests = %d, want 2", got)
	}
	if got := bFull.Load(); got != 0 {
		t.Errorf("desktop titlebar unexpected B full requests = %d", got)
	}
	if got := cFull.Load(); got != 0 {
		t.Errorf("desktop titlebar unexpected C full requests = %d", got)
	}
}

func desktopTitlebarDriveAssemblies(t *testing.T) (StagedAssembly, StagedAssembly) {
	t.Helper()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	use, err := ScanHTML([]byte(`<header data-kit-component="desktop-titlebar@1.0.0" data-kit-as="$bar" data-titlebar-drag></header>`))
	if err != nil {
		t.Fatal(err)
	}
	options, err := composer.stagedBuildOptions(use, ProfileHydrate, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}
	extendedOptions := options
	extendedOptions.Components = append([]ComponentPackage(nil), options.Components...)
	extendedOptions.Components = append(extendedOptions.Components, ComponentPackage{
		Name: "desktop-titlebar-drive-extra", Version: "1.0.0",
		Source: []byte(`;kit.component("desktop-titlebar-drive-extra", { ready: "ready" });
`),
	})
	extended, err := BuildStaged(extendedOptions)
	if err != nil {
		t.Fatal(err)
	}
	if base.Runtime.SHA256() != extended.Runtime.SHA256() || base.Hydrate == nil || extended.Hydrate == nil ||
		base.Hydrate.SHA256() != extended.Hydrate.SHA256() {
		t.Fatal("desktop titlebar Drive fixture changed runtime or Hydrate")
	}
	if len(base.Services) != 1 || len(extended.Services) != 1 ||
		base.Services[0].SHA256() != extended.Services[0].SHA256() {
		t.Fatal("desktop titlebar Drive fixture changed its sealed window service")
	}
	return base, extended
}

func desktopTitlebarDriveDocument(assembly StagedAssembly, route, tag string, extra bool, contractIntegrity string) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Desktop titlebar ` +
		html.EscapeString(strings.ToUpper(route)) + `</title>`)
	output.WriteString(`<script data-kitwork-jit="legacy-theme" defer src="/desktop-titlebar-drive-contract.js" integrity="` +
		html.EscapeString(contractIntegrity) + `" crossorigin="anonymous"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>`)
	}
	output.WriteString(`</head><body><nav>
<a id="to-a" href="/a">A</a><a id="to-b" href="/b">B</a><a id="to-c" href="/c">C</a>
</nav><main id="route">` + html.EscapeString(route) + `</main><` + tag +
		` id="titlebar" data-route="` + html.EscapeString(route) +
		`" data-kit-component="desktop-titlebar@1.0.0" data-kit-as="$bar" data-titlebar-drag>
  <span id="drag-handle">Drag ` + html.EscapeString(route) + `</span>
  <button id="minimize" type="button" data-titlebar-no-drag data-kit-click="$bar.minimize()">Minimize</button>
  <button id="toggle-maximize" type="button" data-titlebar-no-drag data-kit-click="$bar.toggleMaximize()">Toggle</button>
  <output id="maximized-state" data-kit-text="maximized ? 'maximized' : 'restored'">server</output>
</` + tag + `>`)
	if extra {
		output.WriteString(`<aside id="extra-host" data-kit-component="desktop-titlebar-drive-extra@1.0.0">
  <output id="extra-ready" data-kit-text="ready">server-extra</output></aside>`)
	}
	output.WriteString(`</body></html>`)
	return output.String()
}

const desktopTitlebarDrivePrelude = `(function (global, document) {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var calls = [];
  var maximized = false;
  var probe = {
    calls: calls,
    errors: [],
    adds: { mousedown: 0, dblclick: 0 },
    removes: { mousedown: 0, dblclick: 0 }
  };
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === adapter, frozen: Object.isFrozen(params) });
      if (action === "window.isMaximized") return Promise.resolve(maximized);
      if (action === "window.maximize") maximized = true;
      if (action === "window.restore") maximized = false;
      if (action.indexOf("window.") === 0) return Promise.resolve(true);
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  };
  Object.defineProperty(document, HOST, { value: Object.freeze(adapter), configurable: true });
  var add = EventTarget.prototype.addEventListener;
  var remove = EventTarget.prototype.removeEventListener;
  function titlebar(target) {
    return target && target.nodeType === 1 &&
      target.getAttribute("data-kit-component") === "desktop-titlebar@1.0.0";
  }
  EventTarget.prototype.addEventListener = function (type, listener, options) {
    if (titlebar(this) && Object.prototype.hasOwnProperty.call(probe.adds, type)) probe.adds[type]++;
    return add.call(this, type, listener, options);
  };
  EventTarget.prototype.removeEventListener = function (type, listener, options) {
    if (titlebar(this) && Object.prototype.hasOwnProperty.call(probe.removes, type)) probe.removes[type]++;
    return remove.call(this, type, listener, options);
  };
  global.__desktopTitlebarDrive = probe;
  global.addEventListener("error", function (event) {
    probe.errors.push(String(event.error && event.error.message || event.message || "script error"));
  });
  global.addEventListener("unhandledrejection", function (event) {
    probe.errors.push(String(event.reason && event.reason.message || event.reason || "rejection"));
  });
})(globalThis, document);`

const desktopTitlebarDriveAssertions = `__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var probe = globalThis.__desktopTitlebarDrive;

  function actionNames(start) {
    return probe.calls.slice(start).map(function (call) { return call.action; }).join(",");
  }
  function listenerCounts(adds, removes, label) {
    assert(probe.adds.mousedown === adds && probe.adds.dblclick === adds &&
      probe.removes.mousedown === removes && probe.removes.dblclick === removes,
      label + " listener lifecycle was add=" + JSON.stringify(probe.adds) +
      " remove=" + JSON.stringify(probe.removes));
  }
  async function gesture(command, useDoubleClick) {
    var start = probe.calls.length;
    var handle = document.getElementById("drag-handle");
    var minimize = document.getElementById("minimize");
    handle.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 1 }));
    minimize.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0 }));
    await nextTurn();
    assert(probe.calls.length === start, "ignored drag target reached native after Drive");

    handle.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, cancelable: true, button: 0 }));
    await waitFor(function () { return probe.calls.length === start + 1; }, "drag stopped after Drive");
    minimize.click();
    await waitFor(function () { return probe.calls.length === start + 2; }, "minimize stopped after Drive");
    if (useDoubleClick) {
      handle.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, button: 0 }));
    } else document.getElementById("toggle-maximize").click();
    await waitFor(function () { return probe.calls.length === start + 4; }, command + " stopped after Drive");
    assert(actionNames(start) === "window.drag,window.minimize,window.isMaximized," + command,
      "gesture dispatched " + actionNames(start));
    assert(document.getElementById("maximized-state").textContent.trim() ===
      (command === "window.maximize" ? "maximized" : "restored"),
      command + " did not render native state");
  }
  async function navigate(link, path, state, sameHost, adds, removes) {
    var previous = document.getElementById("titlebar");
    var start = probe.calls.length;
    document.getElementById(link).click();
    await waitFor(function () {
      return location.pathname === path && document.getElementById("route").textContent === path.slice(1) &&
        document.getElementById("maximized-state").textContent.trim() === state &&
        probe.calls.length === start + (sameHost ? 0 : 1);
    }, "Drive did not settle " + path + " titlebar lifecycle");
    var current = document.getElementById("titlebar");
    assert((current === previous) === sameHost, path + " host identity did not follow Morph compatibility");
    if (!sameHost) {
      assert(probe.calls[start].action === "window.isMaximized",
        path + " remount did not synchronize native maximized state");
    }
    listenerCounts(adds, removes, path);
    assert(document.cookie.indexOf("desktop_titlebar_unexpected_") < 0,
      path + " fell back to full navigation");
  }

  await waitFor(function () {
    return probe.calls.length === 1 && probe.calls[0].action === "window.isMaximized" &&
      document.getElementById("maximized-state").textContent.trim() === "restored" &&
      probe.adds.mousedown === 1 && probe.adds.dblclick === 1;
  }, "initial desktop titlebar did not mount once");
  listenerCounts(1, 0, "initial");

  await gesture("window.maximize", true);
  await navigate("to-b", "/b", "maximized", true, 1, 0);
  assert(document.getElementById("extra-ready").textContent === "ready",
    "component-only graph handoff did not mount its extra package");
  await gesture("window.restore", false);

  await navigate("to-c", "/c", "restored", false, 2, 1);
  assert(!document.getElementById("extra-ready"), "return graph retained removed component content");
  await gesture("window.maximize", true);
  await navigate("to-a", "/a", "maximized", false, 3, 2);
  await gesture("window.restore", false);

  await navigate("to-b", "/b", "restored", true, 3, 2);
  await gesture("window.maximize", true);
  await navigate("to-c", "/c", "maximized", false, 4, 3);
  await gesture("window.restore", false);

  listenerCounts(4, 3, "final");
  assert(probe.errors.length === 0, "desktop titlebar Drive leaked failures: " + probe.errors.join(" | "));
});`

func desktopTitlebarStagedAssembly(t *testing.T) StagedAssembly {
	return desktopTitlebarStagedAssemblyVersion(t, "1.0.0")
}

func desktopTitlebarStagedAssemblyVersion(t *testing.T, version string) StagedAssembly {
	t.Helper()
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	use, err := ScanHTML([]byte(`<header data-kit-component="desktop-titlebar@` + version + `" data-kit-as="$bar" data-titlebar-drag></header>`))
	if err != nil {
		t.Fatal(err)
	}
	options, err := composer.stagedBuildOptions(use, ProfileHydrate, nil)
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}
	return assembly
}

func desktopTitlebar110StagedDocument(assembly StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Desktop titlebar navigation</title>
<script>
(function (global, document) {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var navigationTarget = new EventTarget();
  var nativeAdd = navigationTarget.addEventListener.bind(navigationTarget);
  var nativeRemove = navigationTarget.removeEventListener.bind(navigationTarget);
  var probe = {
    back: 0, forward: 0, navigationAdds: 0, navigationRemoves: 0,
    nativeCalls: [], errors: [], diagnostics: [], navigation: navigationTarget
  };
  navigationTarget.canGoBack = false;
  navigationTarget.canGoForward = false;
  navigationTarget.addEventListener = function (type, listener, options) {
    if (type === "currententrychange") probe.navigationAdds++;
    return nativeAdd(type, listener, options);
  };
  navigationTarget.removeEventListener = function (type, listener, options) {
    if (type === "currententrychange") probe.navigationRemoves++;
    return nativeRemove(type, listener, options);
  };
  Object.defineProperty(global, "navigation", {
    configurable: true, value: navigationTarget
  });
  Object.defineProperty(history, "back", {
    configurable: true, writable: true, value: function () { probe.back++; }
  });
  Object.defineProperty(history, "forward", {
    configurable: true, writable: true, value: function () { probe.forward++; }
  });
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      probe.nativeCalls.push({ action: action, params: params });
      if (action === "window.isMaximized") return Promise.resolve(false);
      if (action.indexOf("window.") === 0) return Promise.resolve(true);
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  };
  Object.defineProperty(document, HOST, { value: Object.freeze(adapter), configurable: true });
  global.__desktopTitlebarNavigation = probe;
  global.addEventListener("error", function (event) {
    probe.errors.push(String(event.error && event.error.message || event.message || "script error"));
  });
  global.addEventListener("unhandledrejection", function (event) {
    probe.errors.push(String(event.reason && event.reason.message || event.reason || "rejection"));
  });
  var originalError = console.error;
  console.error = function () {
    probe.diagnostics.push(Array.prototype.map.call(arguments, String).join(" "));
    return originalError.apply(this, arguments);
  };
})(globalThis, document);
</script>
` + tags.String() + `</head><body>
<header id="titlebar" data-kit-component="desktop-titlebar@1.1.0" data-kit-as="$bar" data-titlebar-drag>
  <span id="drag-handle">Drag</span>
  <button id="back" type="button" data-titlebar-no-drag data-kit-click="$bar.back()"
    data-kit-bind="disabled: !canGoBack; 'aria-disabled': canGoBack ? 'false' : 'true';"
    disabled aria-disabled="true">Back</button>
  <button id="forward" type="button" data-titlebar-no-drag data-kit-click="$bar.forward()"
    data-kit-bind="disabled: !canGoForward; 'aria-disabled': canGoForward ? 'false' : 'true';"
    disabled aria-disabled="true">Forward</button>
  <button id="reload" type="button" data-titlebar-no-drag data-kit-click="$bar.reload()">Reload</button>
  <output id="back-state" data-kit-text="canGoBack ? 'ready' : 'blocked'">server</output>
  <output id="forward-state" data-kit-text="canGoForward ? 'ready' : 'blocked'">server</output>
</header>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var probe = globalThis.__desktopTitlebarNavigation;
  var navigation = probe.navigation;
  var host = document.getElementById("titlebar");
  var back = document.getElementById("back");
  var forward = document.getElementById("forward");

  await waitFor(function () {
    return probe.nativeCalls.length === 1 &&
      probe.nativeCalls[0].action === "window.isMaximized" &&
      probe.navigationAdds === 1 &&
      document.getElementById("back-state").textContent.trim() === "blocked" &&
      document.getElementById("forward-state").textContent.trim() === "blocked";
  }, "desktop titlebar 1.1 did not mount with fail-closed navigation state");
  assert(back.disabled && forward.disabled &&
    back.getAttribute("aria-disabled") === "true" &&
    forward.getAttribute("aria-disabled") === "true",
    "initial navigation controls were not disabled");
  assert(globalThis.kit && Object.isFrozen(globalThis.kit.navigation) &&
    typeof globalThis.kit.navigation.reload === "function",
    "desktop titlebar did not receive the sealed navigation service");

  back.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  forward.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  await nextTurn();
  await nextTurn();
  assert(probe.back === 0 && probe.forward === 0,
    "disabled navigation methods reached browser history");

  navigation.canGoBack = true;
  navigation.canGoForward = true;
  navigation.dispatchEvent(new Event("currententrychange"));
  await waitFor(function () {
    return !back.disabled && !forward.disabled &&
      document.getElementById("back-state").textContent.trim() === "ready" &&
      document.getElementById("forward-state").textContent.trim() === "ready";
  }, "currententrychange did not enable navigation controls");
  assert(back.getAttribute("aria-disabled") === "false" &&
    forward.getAttribute("aria-disabled") === "false",
    "enabled navigation controls kept stale aria-disabled state");

  back.click();
  forward.click();
  await waitFor(function () { return probe.back === 1 && probe.forward === 1; },
    "enabled titlebar controls did not dispatch through kit.navigation");

  navigation.canGoBack = false;
  navigation.canGoForward = true;
  navigation.dispatchEvent(new Event("currententrychange"));
  await waitFor(function () {
    return back.disabled && !forward.disabled &&
      document.getElementById("back-state").textContent.trim() === "blocked";
  }, "navigation state did not update independently");

  host.remove();
  await waitFor(function () { return probe.navigationRemoves === 1; },
    "removed titlebar retained its Navigation API listener");
  navigation.canGoBack = true;
  navigation.dispatchEvent(new Event("currententrychange"));
  await nextTurn();
  assert(probe.back === 1 && probe.forward === 1,
    "disposed navigation listener issued a command");
  assert(probe.errors.length === 0 && probe.diagnostics.length === 0,
    "desktop titlebar navigation leaked failures: " +
      probe.errors.concat(probe.diagnostics).join(" | "));
});
</script></body></html>`
}

func desktopTitlebarStagedDocument(assembly StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Desktop titlebar</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var calls = [];
  var maximized = false;
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === adapter, frozen: Object.isFrozen(params) });
      if (action === "window.isMaximized") return Promise.resolve(maximized);
      if (action === "window.maximize") maximized = true;
      if (action === "window.restore") maximized = false;
      if (action.indexOf("window.") === 0) return Promise.resolve(true);
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  };
  Object.defineProperty(document, HOST, { value: Object.freeze(adapter), configurable: true });
  globalThis.__desktopTitlebar = { calls: calls, errors: [], diagnostics: [] };
  window.addEventListener("error", function (event) {
    globalThis.__desktopTitlebar.errors.push(String(event.error && event.error.message || event.message));
  });
  window.addEventListener("unhandledrejection", function (event) {
    globalThis.__desktopTitlebar.errors.push(String(event.reason && event.reason.message || event.reason));
  });
  var originalError = console.error;
  console.error = function () {
    globalThis.__desktopTitlebar.diagnostics.push(Array.prototype.map.call(arguments, String).join(" "));
    return originalError.apply(this, arguments);
  };
})();
</script>
` + tags.String() + `</head><body>
<header id="titlebar" data-kit-component="desktop-titlebar@1.0.0" data-kit-as="$bar" data-titlebar-drag>
  <span id="drag-handle">Drag</span>
  <button id="minimize" type="button" data-titlebar-no-drag data-kit-click="$bar.minimize()">Minimize</button>
  <button id="toggle-maximize" type="button" data-titlebar-no-drag data-kit-click="$bar.toggleMaximize()">Toggle maximize</button>
  <button id="close" type="button" data-titlebar-no-drag data-kit-click="$bar.close()">Close</button>
  <button id="blocked-window" type="button" data-titlebar-no-drag data-kit-click="kit.window.drag()">Blocked direct window</button>
  <output id="maximized-state" data-kit-text="maximized ? 'maximized' : 'restored'">server</output>
</header>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var nextTurn = __kitTestNextTurn;
  var waitFor = __kitTestWaitFor;
  var probe = globalThis.__desktopTitlebar;
  var host = document.getElementById("titlebar");
  var handle = document.getElementById("drag-handle");
  var minimize = document.getElementById("minimize");

  await waitFor(function () {
    return probe.calls.length === 1 && probe.calls[0].action === "window.isMaximized" &&
      document.getElementById("maximized-state").textContent.trim() === "restored";
  }, "desktop titlebar did not mount");
  assert(globalThis.kit && globalThis.kit.window && typeof globalThis.kit.window.drag === "function" &&
    typeof globalThis.kit.window.isMaximized === "function" &&
    Object.isFrozen(globalThis.kit.window), "trusted window service did not expose a sealed drag method");

  handle.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 1 }));
  minimize.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0 }));
  await nextTurn();
  assert(probe.calls.length === 1, "non-primary or no-drag press reached the native host");

  handle.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, cancelable: true, button: 0 }));
  await waitFor(function () { return probe.calls.length === 2; }, "drag gesture did not reach the native host");
  assert(probe.calls[1].action === "window.drag" && probe.calls[1].receiver && probe.calls[1].frozen &&
    Object.keys(probe.calls[1].params).length === 0, "drag widened its private native call");

  handle.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, button: 0 }));
  await waitFor(function () {
    return probe.calls.length === 4 && document.getElementById("maximized-state").textContent.trim() === "maximized";
  }, "titlebar double-click did not maximize");
  assert(probe.calls[2].action === "window.isMaximized" && probe.calls[3].action === "window.maximize",
    "double-click issued " + probe.calls[2].action + ", " + probe.calls[3].action);

  document.getElementById("toggle-maximize").click();
  await waitFor(function () {
    return probe.calls.length === 6 && document.getElementById("maximized-state").textContent.trim() === "restored";
  }, "titlebar toggle did not restore");
  assert(probe.calls[4].action === "window.isMaximized" && probe.calls[5].action === "window.restore",
    "toggle issued " + probe.calls[4].action + ", " + probe.calls[5].action);

  minimize.click();
  document.getElementById("close").click();
  await waitFor(function () { return probe.calls.length === 8; }, "titlebar buttons did not dispatch");
  assert(probe.calls[6].action === "window.minimize" && probe.calls[7].action === "window.close",
    "titlebar button actions were out of order");

  document.getElementById("blocked-window").click();
  await nextTurn();
  await nextTurn();
  assert(probe.calls.length === 8 && probe.diagnostics.length >= 1,
    "authored kit.window bypassed the component boundary");

  host.remove();
  await nextTurn();
  await nextTurn();
  handle.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0 }));
  await nextTurn();
  assert(probe.calls.length === 8, "disposed titlebar retained its native gesture listener");
  assert(probe.errors.length === 0, "desktop titlebar leaked browser failures: " + probe.errors.join(" | "));
});
</script></body></html>`
}
