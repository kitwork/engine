package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserNativeAppUsesSealedStagedServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged native host browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	componentSource := []byte(`;(function () {
  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var HOST = Symbol.for("kitwork:native:host:v1");
  var FILES = Symbol.for("kitwork:native:files:v1");
  var core = document[ASSEMBLY];
  var dispatcher = core && core.nativeHost;
  var fileTransport = core && core.nativeFiles;
  globalThis.__sharedNativeCapture = {
    assembly: Boolean(core),
    ownDispatcher: Boolean(core && Object.prototype.hasOwnProperty.call(core, "nativeHost")),
    ownFileTransport: Boolean(core && Object.prototype.hasOwnProperty.call(core, "nativeFiles")),
    dispatcher: dispatcher,
    fileTransport: fileTransport,
    rawHost: document[HOST],
    rawFiles: document[FILES]
  };
  if (dispatcher && typeof dispatcher.call === "function") {
    globalThis.__sharedPrivateDispatch = true;
    Promise.resolve(dispatcher.call("http.request", { method: "GET", path: "/private" })).then(
      function () {}, function () {}
    );
  }
  Promise.resolve().then(function () {
    var later = document[ASSEMBLY];
    globalThis.__sharedNativeCapture.laterDispatcher = later && later.nativeHost;
    globalThis.__sharedNativeCapture.laterFileTransport = later && later.nativeFiles;
  });
  var originalCharCodeAt = String.prototype.charCodeAt;
  var originalIndexOf = String.prototype.indexOf;
  var originalURL = globalThis.URL;
  globalThis.__nativePoisonRejected = [];
  try {
    String.prototype.charCodeAt = function () { return 0; };
    String.prototype.indexOf = function () { return -1; };
    globalThis.URL = function () {
      return { protocol: "https:", username: "", password: "", href: "https://example.test/before\0after" };
    };
    try { kit.clipboard.writeText("\uD800"); }
    catch (error) { globalThis.__nativePoisonRejected.push(error instanceof TypeError); }
    try { kit.clipboard.writeText("before\0after"); }
    catch (error) { globalThis.__nativePoisonRejected.push(error instanceof TypeError); }
    try { kit.share.open({ text: "\uD800" }); }
    catch (error) { globalThis.__nativePoisonRejected.push(error instanceof TypeError); }
    try { kit.share.open("javascript:private"); }
    catch (error) { globalThis.__nativePoisonRejected.push(error instanceof TypeError); }
  } finally {
    String.prototype.charCodeAt = originalCharCodeAt;
    String.prototype.indexOf = originalIndexOf;
    globalThis.URL = originalURL;
  }
})();
kit.component("native-app", {
  copied: false,
  minimized: false,
  text: "native-app clipboard",
  copy: function () {
    var self = this;
    return kit.clipboard.writeText(this.text).then(function () { self.copied = true; });
  },
  minimize: function () {
    var self = this;
    return kit.window.minimize().then(function (value) { self.minimized = value; return value; });
  }
});
`)
	individualProbeSource := []byte(`;(function () {
  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var HOST = Symbol.for("kitwork:native:host:v1");
  var FILES = Symbol.for("kitwork:native:files:v1");
  var core = document[ASSEMBLY];
  var dispatcher = core && core.nativeHost;
  var fileTransport = core && core.nativeFiles;
  globalThis.__individualNativeCapture = {
    assembly: Boolean(core),
    ownDispatcher: Boolean(core && Object.prototype.hasOwnProperty.call(core, "nativeHost")),
    ownFileTransport: Boolean(core && Object.prototype.hasOwnProperty.call(core, "nativeFiles")),
    dispatcher: dispatcher,
    fileTransport: fileTransport,
    rawHost: document[HOST],
    rawFiles: document[FILES]
  };
  if (dispatcher && typeof dispatcher.call === "function") {
    globalThis.__individualPrivateDispatch = true;
    Promise.resolve(dispatcher.call("http.request", { method: "GET", path: "/private" })).then(
      function () {}, function () {}
    );
  }
  Promise.resolve().then(function () {
    var later = document[ASSEMBLY];
    globalThis.__individualNativeCapture.laterDispatcher = later && later.nativeHost;
    globalThis.__individualNativeCapture.laterFileTransport = later && later.nativeFiles;
  });
})();
kit.component("native-probe", { ready: true });
`)
	assembly, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate,
		Services: []Service{
			clipboardServicePackage(t),
			shareServicePackage(t),
			platformServicePackage(t, "window"),
		},
		Components: []ComponentPackage{
			{Name: "native-app", Version: "0.1.0", Source: componentSource},
			{Name: "native-probe", Version: "0.1.0", Source: individualProbeSource},
			{Name: "native-spare", Version: "0.1.0", Source: []byte(`;kit.component("native-spare", { ready: true });` + "\n")},
		},
		SharedComponentNames: []string{"native-app", "native-spare"},
		ComponentRequires: []ComponentServiceRequirement{
			{Component: "native-app", Service: ServiceVersion{Name: "clipboard", Version: "1.0.0"}},
			{Component: "native-app", Service: ServiceVersion{Name: "share", Version: "1.0.0"}},
			{Component: "native-app", Service: ServiceVersion{Name: "window", Version: "1.0.0"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	assets := make(map[string][]byte, len(assembly.Artifacts()))
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	page := nativeAppStagedDocument(assembly)
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

func TestBrowserNativeAppearanceSynchronizesResolvedTitlebar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged native appearance browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	assembly, err := BuildStaged(StagedBuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{appearanceServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}

	assets := make(map[string][]byte, len(assembly.Artifacts()))
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	page := nativeAppearanceStagedDocument(assembly)
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

func nativeAppearanceStagedDocument(assembly StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Native appearance</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var mediaDark = true;
  var mediaListeners = [];
  var calls = [];
  var rejectNext = false;
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === adapter, frozen: Object.isFrozen(params) });
      if (rejectNext) {
        rejectNext = false;
        return Promise.reject({ code: "FAILED", message: "private native failure" });
      }
      if (action === "appearance.setResolved") return Promise.resolve(true);
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  };
  var media = {
    get matches() { return mediaDark; },
    addEventListener: function (type, listener) {
      if (type === "change") mediaListeners.push(listener);
    },
    setDark: function (value) {
      mediaDark = value;
      mediaListeners.slice().forEach(function (listener) { listener({ matches: value }); });
    }
  };
  globalThis.matchMedia = function (query) {
    if (query !== "(prefers-color-scheme: dark)") throw new Error("unexpected media query " + query);
    return media;
  };
  localStorage.removeItem("theme");
  Object.defineProperty(document, HOST, { value: adapter, configurable: true });
  globalThis.__nativeAppearance = {
    calls: calls,
    media: media,
    rejectNext: function () { rejectNext = true; },
    errors: []
  };
  window.addEventListener("error", function (event) {
    globalThis.__nativeAppearance.errors.push(String(event.error && event.error.message || event.message));
  });
  window.addEventListener("unhandledrejection", function (event) {
    globalThis.__nativeAppearance.errors.push(String(event.reason && event.reason.message || event.reason));
  });
})();
</script>
` + tags.String() + `</head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var nextTurn = __kitTestNextTurn;
  var probe = globalThis.__nativeAppearance;
  var appearance = globalThis.kit.appearance;
  var HOST = Symbol.for("kitwork:native:host:v1");
  await nextTurn();

  assert(!Object.prototype.hasOwnProperty.call(document, HOST) && document[HOST] === undefined,
    "staged runtime did not delete the raw native host slot");
  assert(appearance && appearance.resolved === "dark" && probe.calls.length === 1 &&
    probe.calls[0].action === "appearance.setResolved" && probe.calls[0].params.resolved === "dark",
    "initial resolved appearance did not reach native chrome");

  appearance.toggle();
  appearance.system();
  probe.media.setDark(false);
  appearance.set("light");
  await nextTurn();
  var expected = ["dark", "light", "dark", "light"];
  assert(probe.calls.length === expected.length,
    "toggle/system synchronization emitted duplicate or missing calls: " + probe.calls.length);
  probe.calls.forEach(function (call, index) {
    assert(call.receiver && call.frozen && call.action === "appearance.setResolved" &&
      Object.keys(call.params).join(",") === "resolved" && call.params.resolved === expected[index],
      "native appearance call " + index + " widened or carried the wrong resolved value");
  });

  probe.rejectNext();
  appearance.set("dark");
  await nextTurn();
  assert(appearance.resolved === "dark" && probe.calls.length === 5 && probe.errors.length === 0,
    "native rejection corrupted appearance or escaped as an unhandled failure");
  appearance.set("dark");
  await nextTurn();
  assert(probe.calls.length === 6 && probe.calls[5].params.resolved === "dark",
    "an idempotent publish did not retry the failed native synchronization");
  assert(globalThis.kit.bridge === undefined && globalThis.kit.native === undefined &&
    appearance.bridge === undefined && appearance.nativeHost === undefined && appearance.dispatch === undefined,
    "appearance exposed the raw native dispatcher");
});
</script></body></html>`
}

func nativeAppStagedDocument(assembly StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Staged native app</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var FILES = Symbol.for("kitwork:native:files:v1");
  var calls = [];
  var fileCalls = [];
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === adapter, frozen: Object.isFrozen(params) });
      if (action === "window.minimize") return Promise.resolve(true);
      if (action === "clipboard.writeText") return Promise.resolve(true);
      if (action === "share.open") return Promise.resolve(true);
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  };
  var filesAdapter = {
    version: "1.0.0",
    send: function (frame) { fileCalls.push(frame); return true; }
  };
  Object.defineProperty(document, HOST, { value: adapter, configurable: true });
  Object.defineProperty(document, FILES, { value: filesAdapter, configurable: true });
  globalThis.__nativeHostCalls = calls;
  globalThis.__nativeFileCalls = fileCalls;
  globalThis.__nativeHostDiagnostics = [];
  globalThis.__nativeHostErrors = [];
  window.addEventListener("error", function (event) {
    globalThis.__nativeHostErrors.push(String(event.error && event.error.message || event.message));
  });
  window.addEventListener("unhandledrejection", function (event) {
    globalThis.__nativeHostErrors.push(String(event.reason && event.reason.message || event.reason));
  });
  var originalError = console.error;
  console.error = function () {
    globalThis.__nativeHostDiagnostics.push(Array.prototype.map.call(arguments, String).join(" "));
    return originalError.apply(this, arguments);
  };
})();
</script>
` + tags.String() + `</head><body>
<main data-kit-component="native-app@0.1.0" data-kit-as="$app">
  <button id="native-copy" data-kit-click="$app.copy()">Copy</button>
  <button id="native-minimize" data-kit-click="$app.minimize()">Minimize</button>
  <button id="blocked-app-clipboard" data-kit-click="$app.clipboard.writeText('blocked')">Blocked clipboard</button>
  <button id="blocked-app-window" data-kit-click="$app.window.minimize()">Blocked window</button>
  <button id="blocked-kit-window" data-kit-click="kit.window.minimize()">Blocked kit</button>
  <output id="native-copy-state" data-kit-text="copied ? 'copied' : 'idle'"></output>
  <output id="native-window-state" data-kit-text="minimized ? 'minimized' : 'open'"></output>
</main>
<script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var HOST = Symbol.for("kitwork:native:host:v1");
  var FILES = Symbol.for("kitwork:native:files:v1");
  assert(!Object.prototype.hasOwnProperty.call(document, HOST) && document[HOST] === undefined,
    "staged runtime did not delete the raw native host slot");
  assert(!Object.prototype.hasOwnProperty.call(document, FILES) && document[FILES] === undefined,
    "staged runtime did not delete the raw native file slot");
  var captures = [globalThis.__sharedNativeCapture, globalThis.__individualNativeCapture];
  assert(captures.every(function (capture) {
    return capture && capture.assembly && capture.ownDispatcher === false && capture.ownFileTransport === false &&
      capture.dispatcher === undefined && capture.fileTransport === undefined &&
      capture.rawHost === undefined && capture.rawFiles === undefined &&
      capture.laterDispatcher === undefined && capture.laterFileTransport === undefined;
  }), "a shared or individual tenant component captured a native transport through an engine symbol");
  assert(globalThis.__sharedPrivateDispatch !== true && globalThis.__individualPrivateDispatch !== true &&
    globalThis.__nativeHostCalls.length === 0 && globalThis.__nativeFileCalls.length === 0,
    "a tenant component invoked a private native action before publication");
  assert(globalThis.__nativePoisonRejected.length === 4 &&
    globalThis.__nativePoisonRejected.every(Boolean) && globalThis.__nativeHostCalls.length === 0,
    "a tenant component bypassed service validation by poisoning browser intrinsics");
  assert(globalThis.kit && globalThis.kit.clipboard && globalThis.kit.window,
    "sealed clipboard/window services were not published; keys=" +
      Object.keys(globalThis.kit || {}).join(",") + "; errors=" +
      globalThis.__nativeHostErrors.join(" | "));
  assert(globalThis.kit.bridge === undefined && globalThis.kit.native === undefined &&
    globalThis.kit.clipboard.bridge === undefined && globalThis.kit.window.bridge === undefined,
    "private native transport escaped the sealed service graph");
  var graph = globalThis.kit[Symbol.for("kitjs:graph")];
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(globalThis.kit.clipboard) &&
    Object.isFrozen(globalThis.kit.window) && graph && Object.isFrozen(graph) && Object.isFrozen(graph.services),
    "native service graph was not sealed and frozen");

  document.getElementById("native-copy").click();
  try {
    await waitFor(function () {
      return document.getElementById("native-copy-state").textContent.trim() === "copied";
    }, "ordinary native-app copy method did not settle");
  } catch (error) {
    throw new Error(error.message + "; calls=" + JSON.stringify(globalThis.__nativeHostCalls) +
      "; diagnostics=" + globalThis.__nativeHostDiagnostics.join(" | ") +
      "; errors=" + globalThis.__nativeHostErrors.join(" | "));
  }
  document.getElementById("native-minimize").click();
  await waitFor(function () {
    return document.getElementById("native-window-state").textContent.trim() === "minimized";
  }, "ordinary native-app minimize method did not settle");

  var calls = globalThis.__nativeHostCalls;
  assert(calls.length === 2 && calls[0].receiver && calls[0].frozen &&
    calls[0].action === "clipboard.writeText" && calls[0].params.text === "native-app clipboard" &&
    calls[1].action === "window.minimize" && calls[1].frozen,
    "native-app methods did not use the exact sealed service actions");
  var before = calls.length;
  document.getElementById("blocked-app-clipboard").click();
  document.getElementById("blocked-app-window").click();
  document.getElementById("blocked-kit-window").click();
  await __kitTestNextTurn();
  await __kitTestNextTurn();
  assert(calls.length === before, "authored markup bypassed ordinary native-app methods");
  assert(globalThis.__nativeHostDiagnostics.length >= 3,
    "blocked authored native paths did not fail closed with diagnostics");
});
</script></body></html>`
}
