package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCameraStagedBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged camera browser contract in short mode")
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
		Components:   []ComponentRef{{Name: "app", Version: "1.4.0"}},
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

	page := `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Camera contract</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var operation = "O".repeat(32);
  var metadata = Object.freeze({
    handle: "H".repeat(32), name: "browser-photo.jpg", type: "image/jpeg",
    size: 7, sha256: "b".repeat(64)
  });
  var polls = 0;
  globalThis.__cameraBrowserMode = "success";
  globalThis.__cameraBrowserCalls = [];
  var host = Object.freeze({
    version: "1.0.0",
    call: function (action, params) {
      globalThis.__cameraBrowserCalls.push({ action: action, params: params, receiver: this === host });
      if (action === "capabilities.supports") return params.id === "camera.capture";
      if (action === "camera.beginCapture") { polls = 0; return { operation: operation }; }
      if (action === "camera.pollCapture") {
        polls++;
        if (globalThis.__cameraBrowserMode === "tooLarge") {
          return { status: "failed", code: "TOO_LARGE" };
        }
        return polls === 1 ? { status: "pending" } : { status: "ready", file: metadata };
      }
      if (action === "camera.releaseCapture") return true;
      if (action === "files.stat") return metadata;
      if (action === "files.release") return true;
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  });
  Object.defineProperty(document, HOST, { value: host, configurable: true });
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
    assert(kit && Object.isFrozen(kit) && graph && graph.services.camera === "1.0.0" &&
      graph.services.files === "1.3.0" && graph.grants.app.camera === "1.0.0",
      "app@1.4.0 did not publish the exact camera graph");
    assert(graph.actions.camera.capture === undefined &&
      document[Symbol.for("kitjs:assembly")] === undefined &&
      document[Symbol.for("kitwork:native:host:v1")] === undefined,
      "camera leaked authored actions or private assembly slots");
    assert(await kit.camera.available() === true, "camera capability negotiation failed");
    var reference = await kit.camera.capture({ facing: "user" });
    assert(Object.isFrozen(reference) && reference.name === "browser-photo.jpg" &&
      reference.type === "image/jpeg" && reference.size === 7 && reference.sha256 === "b".repeat(64) &&
      reference.handle === undefined && Object.keys(reference).sort().join(",") === "name,sha256,size,type",
      "browser capture did not return an opaque canonical FileRef");
    var stat = await kit.files.stat(reference);
    assert(stat.sha256 === reference.sha256 && stat !== reference,
      "camera FileRef was not owned by files@1.3.0");
    assert(await kit.files.release(reference) === true && await kit.files.release(stat) === true,
      "camera FileRef did not use ordinary files lifecycle");
    globalThis.__cameraBrowserMode = "tooLarge";
    var tooLarge = null;
    try { await kit.camera.capture(); }
    catch (error) { tooLarge = error; }
    assert(tooLarge && tooLarge.name === "KitCameraError" && tooLarge.code === "TOO_LARGE" &&
      tooLarge.operation === "capture" && tooLarge.message === "Captured photo is too large" &&
      Object.isFrozen(tooLarge), "browser failed envelope lost its stable TOO_LARGE error");
    var calls = globalThis.__cameraBrowserCalls;
    var begin = calls.find(function (entry) { return entry.action === "camera.beginCapture"; });
    assert(begin && begin.receiver && Object.isFrozen(begin.params) && begin.params.facing === "user" &&
      calls.filter(function (entry) { return entry.action === "camera.releaseCapture"; }).length === 2,
      "staged native transport changed camera params or cleanup");
    root.setAttribute("data-kit-test", "passed");
  }).catch(function (error) {
    root.setAttribute("data-kit-test", "failed");
    root.setAttribute("data-kit-test-error", String(error && error.message || error));
  });
}, { once: true });
</script></head><body><main data-kit-component="app@1.4.0" data-kit-as="$app"></main></body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/camera.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/camera.html")
}
