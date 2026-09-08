package javascript

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func capabilityLabRequirements() []ServiceVersion {
	return []ServiceVersion{
		{Name: "announce", Version: "1.0.0"},
		{Name: "appearance", Version: "1.0.0"},
		{Name: "camera", Version: "1.0.0"},
		{Name: "capabilities", Version: "1.0.0"},
		{Name: "clipboard", Version: "1.0.0"},
		{Name: "cookie", Version: "1.0.0"},
		{Name: "deepLinks", Version: "1.0.0"},
		{Name: "device", Version: "1.0.0"},
		{Name: "files", Version: "1.3.0"},
		{Name: "fullscreen", Version: "1.0.0"},
		{Name: "lifecycle", Version: "1.0.0"},
		{Name: "media", Version: "1.0.0"},
		{Name: "navigation", Version: "1.0.0"},
		{Name: "network", Version: "1.0.0"},
		{Name: "notifications", Version: "1.0.0"},
		{Name: "progress", Version: "1.0.0"},
		{Name: "qr", Version: "1.0.0"},
		{Name: "request", Version: "1.0.0"},
		{Name: "secureStorage", Version: "1.0.0"},
		{Name: "share", Version: "1.0.0"},
		{Name: "shell", Version: "1.0.0"},
		{Name: "storage", Version: "1.0.0"},
		{Name: "wakeLock", Version: "1.0.0"},
	}
}

func TestCapabilityLabCatalogClosesExactManagedGraph(t *testing.T) {
	source := readVanillaFile(t, "component", "capability-lab", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("capability-lab@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.component("capability-lab"`)); got != 1 {
		t.Fatalf("capability-lab@1.0.0 registration count = %d, want 1", got)
	}
	for _, marker := range [][]byte{
		[]byte(`capabilities.supports`),
		[]byte(`screen.keepAwake`),
		[]byte(`notifications.show`),
		[]byte(`deepLinks.receive`),
		[]byte(`lifecycleState`),
		[]byte(`lifecycleHistory`),
		[]byte(`permission: permission`),
		[]byte(`result: result`),
		[]byte(`schema: "kitwork.capability-lab.v1"`),
		[]byte(`context.cleanup(`),
	} {
		if !bytes.Contains(source, marker) {
			t.Fatalf("capability-lab@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`kit.service(`),
		[]byte(`addEventListener(`),
		[]byte(`setInterval(`),
		[]byte(`setTimeout(`),
		[]byte(`fetch(`),
		[]byte(`querySelector(`),
		[]byte(`createElement(`),
		[]byte(`innerHTML`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("capability-lab package evaluation contains side-effect ownership %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	component, err := catalog.component("capability-lab", "")
	if err != nil {
		t.Fatal(err)
	}
	if component.identity != (ComponentVersion{Name: "capability-lab", Version: "1.0.0"}) {
		t.Fatalf("capability-lab default identity = %#v", component.identity)
	}
	wantRequirements := capabilityLabRequirements()
	if !reflect.DeepEqual(component.requires, wantRequirements) {
		t.Fatalf("capability-lab requirements = %#v, want %#v", component.requires, wantRequirements)
	}
	if !bytes.Equal(component.source, source) {
		t.Fatal("capability-lab catalog did not retain its embedded source bytes")
	}

	composer := &Composer{catalog: catalog}
	components, requirements, _, services, err := composer.closePackages([]ComponentRef{{
		Name: "capability-lab", Version: "1.0.0",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(components, []ComponentVersion{{Name: "capability-lab", Version: "1.0.0"}}) {
		t.Fatalf("closed capability-lab components = %#v", components)
	}
	wantEdges := make([]ComponentServiceRequirement, len(wantRequirements))
	for index, requirement := range wantRequirements {
		wantEdges[index] = ComponentServiceRequirement{Component: "capability-lab", Service: requirement}
	}
	if !reflect.DeepEqual(requirements, wantEdges) {
		t.Fatalf("closed capability-lab edges = %#v, want %#v", requirements, wantEdges)
	}
	gotServices := make([]ServiceVersion, len(services))
	for index, service := range services {
		gotServices[index] = ServiceVersion{Name: service.Name, Version: service.Version}
	}
	if !reflect.DeepEqual(gotServices, wantRequirements) {
		t.Fatalf("closed capability-lab services = %#v, want %#v", gotServices, wantRequirements)
	}

	fromHTML, err := composer.ComposeHTML([]byte(`<main data-kit-component="capability-lab@1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: "capability-lab", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if fromHTML.Empty() || fromHTML.ContentHash != explicit.ContentHash ||
		!bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
		t.Fatal("authored and explicit capability-lab selections produced different artifacts")
	}
	if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.component("capability-lab"`)); got != 1 {
		t.Fatalf("composed capability-lab registration count = %d, want 1", got)
	}
	if bytes.Contains(fromHTML.JavaScript, []byte(`services["window"]`)) {
		t.Fatal("capability-lab graph widened to the unrelated window service")
	}
	for _, requirement := range wantRequirements {
		marker := []byte(`services["` + requirement.Name + `"] = "` + requirement.Version + `"`)
		if !bytes.Contains(fromHTML.JavaScript, marker) {
			t.Fatalf("composed capability-lab graph lost %s@%s", requirement.Name, requirement.Version)
		}
	}
}

func TestBrowserCapabilityLabDiscoveryHasNoActionSideEffects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping capability-lab browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	source := readVanillaFile(t, "component", "capability-lab", "1.0.0.js")
	page := capabilityLabDiscoveryDocument()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/capability-lab.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/")
}

func capabilityLabDiscoveryDocument() string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Capability Lab discovery contract</title>
<script>
(function () {
  "use strict";
  var calls = [];
  var definition = null;
  function action(name) {
    return function () { calls.push(name); return Promise.resolve(true); };
  }
  var kit = {
    component: function (name, value) {
      if (name !== "capability-lab" || definition !== null) throw new Error("unexpected component registration");
      definition = value;
    },
    announce: { say: action("announce.say") },
    appearance: { toggle: action("appearance.toggle") },
    camera: { capture: action("camera.capture") },
    capabilities: { supports: function (id) {
      calls.push("capabilities.supports:" + id);
		return Promise.resolve(id === "device.info" || id === "screen.keepAwake" || id === "notifications.show" || id === "deepLinks.receive");
    } },
    clipboard: { writeText: action("clipboard.writeText") },
    cookie: { get: action("cookie.get") },
		device: { info: action("device.info"), vibrate: action("device.vibrate") },
		deepLinks: { snapshot: function () { return Promise.resolve(null); }, subscribe: function () { return function () {}; } },
    files: {
      importBlob: action("files.importBlob"), pick: action("files.pick"),
      share: action("files.share"), export: action("files.export")
    },
		fullscreen: { request: action("fullscreen.request") },
    lifecycle: { snapshot: function () { return Object.freeze({ state: "active" }); }, subscribe: function (listener) { globalThis.__lifecycleListener = listener; listener(Object.freeze({ state: "active" })); return function () { globalThis.__lifecycleListener = null; }; } },
    media: { pickImage: action("media.pickImage") },
    navigation: { reload: action("navigation.reload") },
    network: { status: action("network.status") },
    notifications: {
      permission: action("notifications.permission"),
      requestPermission: action("notifications.requestPermission"),
      show: action("notifications.show")
    },
    progress: { snapshot: action("progress.snapshot") },
    qr: { scan: action("qr.scan") },
    request: { get: action("request.get") },
    secureStorage: { get: action("secureStorage.get") },
    share: { open: action("share.open") },
    shell: { open: action("shell.open") },
    storage: { get: action("storage.get") },
    wakeLock: { request: action("wakeLock.request"), release: action("wakeLock.release") }
  };
  globalThis.kit = kit;
  globalThis.__capabilityLabProbe = {
    calls: calls,
    definition: function () { return definition; }
  };
})();
</script>
<script src="/capability-lab.js"></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var probe = globalThis.__capabilityLabProbe;
  var definition = probe.definition();
  assert(definition && typeof definition.init === "function" && typeof definition.refresh === "function",
    "capability-lab did not register one usable definition");
  assert(probe.calls.length === 0, "package evaluation called a service: " + probe.calls.join(","));

	var instance = {};
	Object.keys(definition).forEach(function (key) { instance[key] = definition[key]; });
	var cleanup = null;
	var listenCount = 0;
	var initialized = await instance.init({
	  listen: function (target, type, callback) {
	    assert(target === document && type === "visibilitychange" && typeof callback === "function",
	      "capability-lab requested an unexpected lifecycle listener");
	    listenCount++;
	  },
	  cleanup: function (callback) { cleanup = callback; }
	});
	assert(initialized === true && typeof cleanup === "function", "capability-lab init did not finish or own cleanup");
	assert(listenCount === 1, "capability-lab did not own exactly one visibility listener");
  var expected = [
    "camera.capture", "clipboard.writeText", "device.info", "device.vibrate",
    "files.export", "files.import", "files.import", "files.import", "files.share",
		"deepLinks.receive", "network.status", "notifications.show", "qr.scan", "screen.keepAwake", "secureStorage.get", "share.open", "shell.open"
  ].sort().join(",");
  var discovered = probe.calls.map(function (call) {
    assert(call.indexOf("capabilities.supports:") === 0, "discovery invoked an action: " + call);
    return call.slice("capabilities.supports:".length);
  }).sort().join(",");
  assert(discovered === expected, "discovery queried the wrong grants: " + discovered);
	assert(instance.items.every(function (item) {
	  return item.id === "lifecycle" ? item.result === "pass" && item.detail === "active" : item.result === "not-run";
	}), "discovery changed an unrelated test outcome");
	assert(instance.lifecycleHistory === "Lịch sử: active", "initial lifecycle history was not recorded");
	globalThis.__lifecycleListener(Object.freeze({ state: "inactive" }));
	globalThis.__lifecycleListener(Object.freeze({ state: "background" }));
	globalThis.__lifecycleListener(Object.freeze({ state: "active" }));
	globalThis.__lifecycleListener(Object.freeze({ state: "active" }));
	assert(instance.lifecycleHistory === "Lịch sử: active → inactive → background → active",
	  "lifecycle history lost ordering or dedupe: " + instance.lifecycleHistory);

  probe.calls.length = 0;
  assert(await instance.refresh() === true, "explicit refresh did not finish");
	assert(probe.calls.length === 17 && probe.calls.every(function (call) {
    return call.indexOf("capabilities.supports:") === 0;
  }), "refresh invoked a permission prompt or service action: " + probe.calls.join(","));
  cleanup();
  assert(instance.active === false, "component cleanup did not deactivate discovery updates");
	assert(globalThis.__lifecycleListener === null, "component cleanup retained its lifecycle subscriber");
});
</script></body></html>`
}

func TestCapabilityLabRequirementsRemainCanonical(t *testing.T) {
	requirements := capabilityLabRequirements()
	names := make([]string, len(requirements))
	for index, requirement := range requirements {
		names[index] = requirement.Name
		if requirement.Version == "" || requirement.Version == "latest" || strings.HasPrefix(requirement.Version, "v") {
			t.Fatalf("non-exact capability-lab requirement: %#v", requirement)
		}
	}
	if strings.Join(names, ",") != strings.Join(sortedStrings(append([]string(nil), names...)), ",") {
		t.Fatalf("capability-lab requirements are not in canonical name order: %v", names)
	}
}

func sortedStrings(values []string) []string {
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			if values[right] < values[left] {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
	return values
}
