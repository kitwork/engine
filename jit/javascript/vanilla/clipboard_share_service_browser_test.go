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

func TestClipboardSharePackageStaticContract(t *testing.T) {
	tests := []struct {
		name      string
		required  []string
		forbidden []string
	}{
		{
			name: "clipboard",
			required: []string{
				`writeText: writeText`, `readText: readText`,
				`name: { value: "KitClipboardError" }`,
				`UNAVAILABLE`, `DENIED`, `CANCELLED`, `FAILED`,
			},
			forbidden: []string{`copy:`, `read:`, `navigator.clipboard =`},
		},
		{
			name: "share",
			required: []string{
				`open: open`, `canShare: canShare`, `kit.clipboard.writeText(`,
				`name: { value: "KitShareError" }`,
				`UNAVAILABLE`, `DENIED`, `CANCELLED`, `FAILED`,
			},
			forbidden: []string{`share: open`, `supported: canShare`, `navigator.share =`},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			source := readVanillaFile(t, "service", test.name, "1.0.0.js")
			if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
				t.Fatalf("%s@1.0.0 is not a sealable LF-only classic script", test.name)
			}
			registration := []byte(`kit.service("` + test.name + `"`)
			if got := bytes.Count(source, registration); got != 1 {
				t.Fatalf("%s@1.0.0 registration count = %d, want one", test.name, got)
			}
			if got := bytes.Count(source, []byte(`kit.service(`)); got != 1 {
				t.Fatalf("%s@1.0.0 registrar call count = %d, want one", test.name, got)
			}
			for _, required := range test.required {
				if !bytes.Contains(source, []byte(required)) {
					t.Fatalf("%s@1.0.0 lost %q", test.name, required)
				}
			}
			for _, forbidden := range append(test.forbidden,
				`global.kit`, `globalThis.kit`, `window.kit`, `kit.component(`,
				`document.addEventListener`, `fetch(`, `XMLHttpRequest`, `bridge`,
				`service conflict`, `core.`, `Symbol.for("kitjs:`) {
				if bytes.Contains(source, []byte(forbidden)) {
					t.Fatalf("%s@1.0.0 contains forbidden coupling %q", test.name, forbidden)
				}
			}
		})
	}
}

func TestBuildClipboardShareGraphIsClosedOrderedAndDeterministic(t *testing.T) {
	clipboard := clipboardServicePackage(t)
	share := shareServicePackage(t)

	left, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{share, clipboard},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{clipboard, share},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.Name() != right.Name() || left.SHA256() != right.SHA256() || !bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("clipboard/share discovery order changed deterministic graph identity")
	}
	clipboardAt := bytes.Index(left.Bytes(), clipboard.Source)
	shareAt := bytes.Index(left.Bytes(), share.Source)
	if clipboardAt < 0 || shareAt < 0 || clipboardAt >= shareAt {
		t.Fatalf("share graph order = clipboard:%d share:%d, want dependency before owner", clipboardAt, shareAt)
	}

	withoutEdge := share
	withoutEdge.Requires = nil
	withoutDependencyMetadata, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{clipboard, withoutEdge},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256() == withoutDependencyMetadata.SHA256() || bytes.Equal(left.Bytes(), withoutDependencyMetadata.Bytes()) {
		t.Fatal("share-to-clipboard dependency metadata did not affect graph identity")
	}

	if _, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{share}}); err == nil ||
		!strings.Contains(err.Error(), "requires missing service clipboard@1.0.0") {
		t.Fatalf("missing clipboard dependency error = %v", err)
	}
	clipboardV2 := clipboard
	clipboardV2.Version = "2.0.0"
	if _, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{share, clipboardV2}}); err == nil ||
		!strings.Contains(err.Error(), "requires service clipboard@1.0.0 but graph provides 2.0.0") {
		t.Fatalf("mismatched clipboard dependency error = %v", err)
	}
}

func TestBrowserClipboardShareServicesAndGraphGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping clipboard/share browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	installed := buildClipboardShareArtifact(t)
	different, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{clipboardServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{
		"/assets/" + installed.Name(): installed.Bytes(),
		"/assets/" + different.Name(): different.Bytes(),
	}
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, ok := assets[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		switch request.URL.Path {
		case "/clipboard-share.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(clipboardShareServiceDocument(installed, different)))
		case "/service/clipboard/1.0.0.js", "/service/share/1.0.0.js", "/clipboard.js", "/share.js":
			packageRequests.Add(1)
			http.Error(response, "service packages must already be sealed", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/clipboard-share.html")
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched clipboard/share packages at runtime %d times", got)
	}
}

func clipboardServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "clipboard",
		Version: "1.0.0",
		Source:  readVanillaFile(t, "service", "clipboard", "1.0.0.js"),
	}
}

func shareServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "share",
		Version: "1.0.0",
		Requires: []ServiceVersion{{
			Name: "clipboard", Version: "1.0.0",
		}},
		Source: readVanillaFile(t, "service", "share", "1.0.0.js"),
	}
}

func buildClipboardShareArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: []Service{shareServicePackage(t), clipboardServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func clipboardShareServiceDocument(installed, different Artifact) string {
	return fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Clipboard share contract</title><script>
(function () {
  "use strict";
  globalThis.__clipboardCalls = [];
  globalThis.__shareCalls = [];
  globalThis.__canShareCalls = [];
  globalThis.__graphErrors = [];
  globalThis.__clipboardAvailable = true;
  globalThis.__clipboardMode = "success";
  globalThis.__clipboardRead = "read-value";
  globalThis.__shareMethod = null;
  globalThis.__canShareMethod = null;
  globalThis.__shareAccessError = null;

  globalThis.__clipboardAdapter = {
    writeText: function (value) {
      globalThis.__clipboardCalls.push({ operation: "writeText", value: value, receiver: this === globalThis.__clipboardAdapter });
      if (globalThis.__clipboardMode === "denied") return Promise.reject(new DOMException("raw clipboard denial secret", "NotAllowedError"));
      if (globalThis.__clipboardMode === "cancelled") return Promise.reject(new DOMException("raw clipboard cancellation secret", "AbortError"));
      if (globalThis.__clipboardMode === "failed") throw new Error("raw clipboard failure secret");
      return Promise.resolve("adapter-result-must-not-escape");
    },
    readText: function () {
      globalThis.__clipboardCalls.push({ operation: "readText", receiver: this === globalThis.__clipboardAdapter });
      if (globalThis.__clipboardMode === "denied") return Promise.reject(new DOMException("raw read denial secret", "NotAllowedError"));
      if (globalThis.__clipboardMode === "cancelled") return Promise.reject(new DOMException("raw read cancellation secret", "AbortError"));
      if (globalThis.__clipboardMode === "failed") return Promise.reject(new Error("raw read failure secret"));
      return Promise.resolve(globalThis.__clipboardRead);
    }
  };

  function expose(name, getter) {
    try { Object.defineProperty(navigator, name, { configurable: true, get: getter }); }
    catch (_) { Object.defineProperty(Object.getPrototypeOf(navigator), name, { configurable: true, get: getter }); }
  }
  expose("clipboard", function () {
    if (globalThis.__clipboardAccessError) throw globalThis.__clipboardAccessError;
    return globalThis.__clipboardAvailable ? globalThis.__clipboardAdapter : undefined;
  });
  expose("share", function () {
    if (globalThis.__shareAccessError) throw globalThis.__shareAccessError;
    return globalThis.__shareMethod;
  });
  expose("canShare", function () { return globalThis.__canShareMethod; });

  window.addEventListener("error", function (event) {
    var message = String(event.error && event.error.message || event.message || "");
    if (message.indexOf("installed component graph does not match this artifact") >= 0) {
      globalThis.__graphErrors.push(message);
      event.preventDefault();
    }
  });
})();
</script><script src="/assets/%s"></script><script>
globalThis.__firstKit = globalThis.kit;
globalThis.__firstClipboard = globalThis.kit.clipboard;
globalThis.__firstShare = globalThis.kit.share;
</script><script src="/assets/%s"></script><script>
globalThis.__sameGraph = globalThis.kit === globalThis.__firstKit &&
  globalThis.kit.clipboard === globalThis.__firstClipboard && globalThis.kit.share === globalThis.__firstShare;
</script><script src="/assets/%s"></script></head><body><script>
%s
%s
</script></body></html>`, installed.Name(), installed.Name(), different.Name(), browserHarness, clipboardShareServiceAssertions)
}

const clipboardShareServiceAssertions = `__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var clipboard = globalThis.kit.clipboard;
  var share = globalThis.kit.share;

  async function rejected(run) {
    try { await run(); }
    catch (error) { return error; }
    throw new Error("operation unexpectedly succeeded");
  }
  function normalized(error, name, code, operation, secret) {
    assert(error && error.name === name && error.code === code && error.operation === operation,
      "normalized error was " + String(error && error.name) + "/" + String(error && error.code) + "/" + String(error && error.operation));
    assert(Object.isFrozen(error), name + " was mutable");
    assert(Object.keys(error).join(",") === "code,operation", name + " public keys were " + Object.keys(error).join(","));
    if (secret) assert(String(error.message).indexOf(secret) < 0, name + " leaked adapter error text");
    return error;
  }
  async function typeError(run, label) {
    var error = await rejected(run);
    assert(error instanceof TypeError, label + " did not reject with TypeError: " + String(error && error.name));
  }

  assert(globalThis.__graphErrors.length === 1, "different service graph did not fail exactly once");
  assert(globalThis.__sameGraph && globalThis.kit === globalThis.__firstKit,
    "same or rejected graph replaced the sealed kit facade");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,clipboard,share",
    "sealed service keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.keys(clipboard).slice().sort().join(",") === "readText,writeText" &&
    Object.keys(share).slice().sort().join(",") === "canShare,open", "service namespaces exposed aliases");
  assert(clipboard.version === "1.0.0" && share.version === "1.0.0" &&
    !Object.keys(clipboard).includes("version") && !Object.keys(share).includes("version"), "service versions were not private metadata");
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(clipboard) && Object.isFrozen(share), "service facade was mutable");
  assert(globalThis.kit.service === undefined && globalThis.kit.bridge === undefined &&
    clipboard.adapter === undefined && clipboard.copy === undefined && clipboard.read === undefined &&
    share.adapter === undefined && share.share === undefined && share.supported === undefined,
    "registrar, bridge, adapter, or removed alias escaped publicly");
  var graph = globalThis.kit[Symbol.for("kitjs:graph")];
  assert(graph && Object.isFrozen(graph) && Object.isFrozen(graph.services) &&
    graph.services.clipboard === "1.0.0" && graph.services.share === "1.0.0",
    "clipboard/share graph metadata was missing or mutable");

  await typeError(function () { return clipboard.writeText(7); }, "non-string clipboard input");
  await typeError(function () { return clipboard.writeText("x".repeat(1048577)); }, "oversized clipboard input");
  assert(globalThis.__clipboardCalls.length === 0, "invalid clipboard input reached the adapter");

  globalThis.__clipboardMode = "success";
  var writeResult = await clipboard.writeText("copy-value");
  assert(writeResult === undefined, "clipboard write exposed the adapter return value");
  assert(globalThis.__clipboardCalls.length === 1 && globalThis.__clipboardCalls[0].value === "copy-value" &&
    globalThis.__clipboardCalls[0].receiver, "clipboard write changed value or receiver");
  globalThis.__clipboardRead = "paste-value";
  assert(await clipboard.readText() === "paste-value" && globalThis.__clipboardCalls[1].receiver,
    "clipboard read changed text or receiver");

  globalThis.__clipboardMode = "denied";
  normalized(await rejected(function () { return clipboard.writeText("private"); }),
    "KitClipboardError", "DENIED", "writeText", "raw clipboard denial secret");
  globalThis.__clipboardMode = "cancelled";
  normalized(await rejected(function () { return clipboard.readText(); }),
    "KitClipboardError", "CANCELLED", "readText", "raw read cancellation secret");
  globalThis.__clipboardMode = "failed";
  normalized(await rejected(function () { return clipboard.writeText("private"); }),
    "KitClipboardError", "FAILED", "writeText", "raw clipboard failure secret");
  globalThis.__clipboardMode = "success";
  globalThis.__clipboardAvailable = false;
  normalized(await rejected(function () { return clipboard.readText(); }),
    "KitClipboardError", "UNAVAILABLE", "readText");
  globalThis.__clipboardAvailable = true;
  globalThis.__clipboardAccessError = new DOMException("raw clipboard access secret", "SecurityError");
  normalized(await rejected(function () { return clipboard.writeText("private"); }),
    "KitClipboardError", "DENIED", "writeText", "raw clipboard access secret");
  globalThis.__clipboardAccessError = null;
  globalThis.__clipboardRead = "x".repeat(1048577);
  normalized(await rejected(function () { return clipboard.readText(); }),
    "KitClipboardError", "FAILED", "readText");
  globalThis.__clipboardRead = "paste-value";

  await typeError(function () { return share.open(null); }, "null share input");
  await typeError(function () { return share.open({ text: "safe", secret: "no" }); }, "unknown share field");
  var accessor = {};
  Object.defineProperty(accessor, "text", { get: function () { throw new Error("share accessor ran"); } });
  await typeError(function () { return share.open(accessor); }, "share accessor");
  await typeError(function () { return share.open({ title: "x".repeat(513) }); }, "oversized share title");
  await typeError(function () { return share.open({ text: "x".repeat(65537) }); }, "oversized share text");
  await typeError(function () { return share.open("data:text/plain,secret"); }, "unsafe share URL");

  globalThis.__shareMethod = function (data) {
    globalThis.__shareCalls.push({ data: data, receiver: this === navigator });
    return Promise.resolve("native-result-must-not-escape");
  };
  globalThis.__canShareMethod = function (data) {
    globalThis.__canShareCalls.push({ data: data, receiver: this === navigator });
    return true;
  };
  var clipboardBeforeNative = globalThis.__clipboardCalls.length;
  assert(share.canShare({ text: "native" }) === true, "native share capability was hidden");
  assert(share.canShare() === true, "default current-page share payload was rejected");
  assert(await share.open({ title: "Article", text: "Read this", url: "/article" }) === true,
    "native share did not resolve true");
  var nativeCall = globalThis.__shareCalls[globalThis.__shareCalls.length - 1];
  assert(nativeCall.receiver && Object.isFrozen(nativeCall.data) && nativeCall.data.url === location.origin + "/article",
    "native share payload or receiver was not normalized");
  assert(globalThis.__clipboardCalls.length === clipboardBeforeNative, "native share also wrote the clipboard");
  assert(globalThis.__canShareCalls.every(function (call) { return call.receiver; }), "native canShare lost navigator receiver");

  globalThis.__shareMethod = function () {
    return Promise.reject(new DOMException("raw share denial secret", "NotAllowedError"));
  };
  var clipboardBeforeDenied = globalThis.__clipboardCalls.length;
  normalized(await rejected(function () { return share.open({ text: "denied" }); }),
    "KitShareError", "DENIED", "open", "raw share denial secret");
  assert(globalThis.__clipboardCalls.length === clipboardBeforeDenied, "denied native share fell back to clipboard");
  globalThis.__shareMethod = function () {
    return Promise.reject(new DOMException("raw share cancellation secret", "AbortError"));
  };
  normalized(await rejected(function () { return share.open({ text: "cancelled" }); }),
    "KitShareError", "CANCELLED", "open", "raw share cancellation secret");
  assert(globalThis.__clipboardCalls.length === clipboardBeforeDenied, "cancelled native share fell back to clipboard");

  globalThis.__shareMethod = null;
  globalThis.__canShareMethod = null;
  globalThis.__clipboardMode = "success";
  var fallbackAt = globalThis.__clipboardCalls.length;
  assert(share.canShare({ text: "fallback" }) === false, "fallback was reported as native sharing");
  assert(await share.open({ title: "Article", text: "Read this", url: "/fallback" }) === true,
    "clipboard fallback did not resolve true");
  assert(globalThis.__clipboardCalls.length === fallbackAt + 1 &&
    globalThis.__clipboardCalls[fallbackAt].value === "Article\nRead this\n" + location.origin + "/fallback",
    "share fallback clipboard text was incomplete");

  globalThis.__shareMethod = function () { return Promise.resolve(); };
  globalThis.__canShareMethod = function () { return false; };
  fallbackAt = globalThis.__clipboardCalls.length;
  assert(await share.open("/can-share-false") === true && globalThis.__clipboardCalls.length === fallbackAt + 1,
    "native canShare=false did not use clipboard fallback");
  globalThis.__canShareMethod = function () { throw new Error("raw canShare failure secret"); };
  assert(share.canShare({ text: "probe" }) === false, "throwing native canShare escaped as support");
  fallbackAt = globalThis.__clipboardCalls.length;
  assert(await share.open({ text: "probe" }) === true && globalThis.__clipboardCalls.length === fallbackAt + 1,
    "throwing native canShare did not safely fall back");

  var file = new File(["content"], "note.txt", { type: "text/plain" });
  globalThis.__canShareMethod = function () { return true; };
  globalThis.__shareMethod = function (data) {
    globalThis.__shareCalls.push({ data: data, receiver: this === navigator });
    return Promise.resolve();
  };
  assert(await share.open({ files: [file] }) === true, "native file share failed");
  nativeCall = globalThis.__shareCalls[globalThis.__shareCalls.length - 1];
  assert(nativeCall.data.files[0] === file && Object.isFrozen(nativeCall.data.files), "file share copied or exposed a mutable list");
  globalThis.__shareMethod = null;
  globalThis.__canShareMethod = null;
  fallbackAt = globalThis.__clipboardCalls.length;
  normalized(await rejected(function () { return share.open({ text: "must not hide file loss", files: [file] }); }),
    "KitShareError", "UNAVAILABLE", "open");
  assert(globalThis.__clipboardCalls.length === fallbackAt, "unsupported file share silently copied only its text");
  await typeError(function () { return share.open({ files: new Array(17).fill(file) }); }, "too many share files");

  globalThis.__clipboardMode = "denied";
  normalized(await rejected(function () { return share.open({ text: "fallback denied" }); }),
    "KitShareError", "DENIED", "open", "raw clipboard denial secret");
  globalThis.__clipboardMode = "success";
  globalThis.__clipboardAvailable = false;
  normalized(await rejected(function () { return share.open({ text: "fallback unavailable" }); }),
    "KitShareError", "UNAVAILABLE", "open");
  globalThis.__clipboardAvailable = true;
});`
