package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestQRMediaStagedBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged QR/media browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	options, err := composer.stagedBuildOptions(ScanResult{
		Components:   []ComponentRef{{Name: "app", Version: "1.5.0"}},
		NeedsRuntime: true,
	}, ProfileKit, nil)
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}

	assets := make(map[string][]byte)
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}

	page := `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>QR and media contract</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var qrOperation = "Q".repeat(32);
  var qrPolls = 0;
  var fileSequence = 0;
  var importMetadata = null;
  globalThis.__qrMediaCalls = [];
  globalThis.__qrMediaErrors = [];
  globalThis.__pickerAttachedAtBegin = [];
  globalThis.__pickerAttachedAtCommit = [];
  globalThis.__pickerInput = null;
  globalThis.__selectedBrowserFile = new File(["image"], "photo.png", { type: "image/png" });
  HTMLInputElement.prototype.click = function () {
    globalThis.__pickerInput = this;
    Object.defineProperty(this, "files", {
      configurable: true,
      value: [globalThis.__selectedBrowserFile]
    });
    this.dispatchEvent(new Event("change"));
  };
  var host = Object.freeze({
    version: "1.0.0",
    call: function (action, params) {
      globalThis.__qrMediaCalls.push({ action: action, params: params, receiver: this === host });
      if (action === "capabilities.supports") {
        return params.id === "qr.scan" || params.id === "camera.capture" || params.id === "files.import";
      }
      if (action === "qr.beginScan") { qrPolls = 0; return { operation: qrOperation }; }
      if (action === "qr.pollScan") {
        qrPolls++;
        return qrPolls === 1 ? { status: "pending" } : { status: "ready", value: "kitwork://qr/browser" };
      }
      if (action === "qr.releaseScan") return true;
      if (action === "files.beginImport") {
        globalThis.__pickerAttachedAtBegin.push(document.contains(globalThis.__pickerInput));
        fileSequence++;
        importMetadata = {
          handle: "H".repeat(31) + String(fileSequence),
          name: params.name,
          type: params.type,
          size: params.size
        };
        return { handle: importMetadata.handle, mode: "http", uploadURL: location.origin + "/upload" };
      }
      if (action === "files.commitImport") {
        globalThis.__pickerAttachedAtCommit.push(document.contains(globalThis.__pickerInput));
        return {
          handle: importMetadata.handle,
          name: importMetadata.name,
          type: importMetadata.type,
          size: importMetadata.size,
          sha256: String(fileSequence).repeat(64)
        };
      }
      if (action === "files.release") return true;
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  });
  Object.defineProperty(document, HOST, { value: host, configurable: true });
  window.addEventListener("error", function (event) {
    globalThis.__qrMediaErrors.push(String(event.error && event.error.message || event.message));
  });
  window.addEventListener("unhandledrejection", function (event) {
    globalThis.__qrMediaErrors.push(String(event.reason && event.reason.message || event.reason));
  });
})();
</script>
` + tags.String() + `
<script>
document.addEventListener("DOMContentLoaded", function () {
  "use strict";
  var root = document.documentElement;
  function assert(value, message) { if (!value) throw new Error(message); }
  Promise.resolve().then(async function () {
    var kit = globalThis.kit;
    var graph = kit && kit[Symbol.for("kitjs:graph")];
    assert(kit && Object.isFrozen(kit) && graph && graph.services.files === "1.3.0" &&
      graph.services.camera === "1.0.0" && graph.services.media === "1.0.0" &&
      graph.services.qr === "1.0.0" && graph.grants.app.media === "1.0.0" &&
      graph.grants.app.qr === "1.0.0", "app@1.5.0 did not publish the exact QR/media graph");
    assert(kit.qr.version === "1.0.0" && kit.media.version === "1.0.0" &&
      Object.keys(kit.qr).sort().join(",") === "available,scan" &&
      Object.keys(kit.media).join(",") === "pickImage" &&
      graph.actions.qr.scan === undefined && graph.actions.media.pickImage === undefined,
      "QR/media namespaces or authored action fence changed");
    assert(document[Symbol.for("kitjs:assembly")] === undefined &&
      document[Symbol.for("kitwork:native:host:v1")] === undefined,
      "QR/media leaked a private assembly or native host slot");

    assert(await kit.qr.available() === true, "QR capability negotiation failed");
    assert(await kit.qr.scan() === "kitwork://qr/browser", "browser QR scan changed its value");

    var image = await kit.media.pickImage();
    assert(image && Object.isFrozen(image) && image.name === "photo.png" && image.type === "image/png" &&
      image.size === 5 && image.handle === undefined && Object.keys(image).sort().join(",") === "name,sha256,size,type",
      "media.pickImage did not return the existing opaque FileRef");
    assert(globalThis.__pickerAttachedAtBegin.join(",") === "true" &&
      globalThis.__pickerAttachedAtCommit.join(",") === "true" &&
      !document.contains(globalThis.__pickerInput),
      "media.pickImage detached its transient File source before import settled");
    assert(await kit.files.release(image) === true, "selected image did not use ordinary files lifecycle");

    globalThis.__selectedBrowserFile = new File(["text"], "note.txt", { type: "text/plain" });
    var beforeRelease = globalThis.__qrMediaCalls.filter(function (entry) {
      return entry.action === "files.release";
    }).length;
    var unsupported = null;
    try { await kit.media.pickImage(); }
    catch (error) { unsupported = error; }
    var afterRelease = globalThis.__qrMediaCalls.filter(function (entry) {
      return entry.action === "files.release";
    }).length;
    assert(unsupported && unsupported.name === "KitMediaError" && unsupported.code === "UNSUPPORTED" &&
      unsupported.operation === "pickImage" && Object.isFrozen(unsupported) && afterRelease === beforeRelease + 1,
      "advisory image filter bypass did not release before stable rejection");
    assert(globalThis.__pickerAttachedAtBegin.join(",") === "true,true" &&
      globalThis.__pickerAttachedAtCommit.join(",") === "true,true" &&
      !document.contains(globalThis.__pickerInput),
      "rejected media selection did not retain then clean its transient File source");

    var calls = globalThis.__qrMediaCalls;
    var begin = calls.find(function (entry) { return entry.action === "qr.beginScan"; });
    assert(begin && begin.receiver && Object.isFrozen(begin.params) && Object.keys(begin.params).length === 0 &&
      calls.filter(function (entry) { return entry.action === "qr.releaseScan"; }).length === 1 &&
      calls.filter(function (entry) { return entry.action === "files.beginImport"; }).length === 2 &&
      globalThis.__qrMediaErrors.length === 0,
      "staged QR/media transport, cleanup, or diagnostics changed");
    root.setAttribute("data-kit-test", "passed");
  }).catch(function (error) {
    root.setAttribute("data-kit-test", "failed");
    root.setAttribute("data-kit-test-error", String(error && error.message || error));
  });
}, { once: true });
</script></head><body><main data-kit-component="app@1.5.0" data-kit-as="$app"></main></body></html>`

	var uploads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/upload" && request.Method == http.MethodPost {
			uploads.Add(1)
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if request.URL.Path == "/qr-media.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/qr-media.html")
	if got := uploads.Load(); got != 2 {
		t.Fatalf("QR/media browser upload count = %d, want 2", got)
	}
}
