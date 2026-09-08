package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFilesExportServiceBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping files export browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	assembly, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileKit,
		Services: []Service{{
			Name: "files", Version: "1.2.0",
			Source: readVanillaFile(t, "service", "files", "1.2.0.js"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := make(map[string][]byte, len(assembly.Artifacts()))
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
	}
	page := filesExportStagedDocument(assembly)
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
		if request.URL.Path == "/" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/")
	if got := uploads.Load(); got != 1 {
		t.Fatalf("files export browser upload count = %d, want 1", got)
	}
}

func filesExportStagedDocument(assembly StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Files export</title>
<script>
(function () {
  "use strict";
  var HOST = Symbol.for("kitwork:native:host:v1");
  var calls = [];
  var poll = 0;
  var handle = "H".repeat(32);
  var operation = "O".repeat(32);
  var metadata = null;
  var adapter = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === adapter, frozen: Object.isFrozen(params) });
      if (action === "files.beginImport") {
        metadata = { name: params.name, type: params.type, size: params.size };
        return { handle: handle, mode: "http", uploadURL: location.origin + "/upload" };
      }
      if (action === "files.commitImport") {
        return { handle: handle, name: metadata.name, type: metadata.type, size: metadata.size,
          sha256: "0".repeat(64) };
      }
      if (action === "files.beginExport") return { operation: operation };
      if (action === "files.pollExport") {
        return { state: ["pending", "writing", "completed"][poll++] };
      }
      if (action === "files.releaseExport") return true;
      return Promise.reject({ code: "UNAVAILABLE" });
    }
  };
  Object.defineProperty(document, HOST, { value: adapter, configurable: true });
  globalThis.__filesExportProbe = { calls: calls, operation: operation, errors: [] };
  window.addEventListener("error", function (event) {
    globalThis.__filesExportProbe.errors.push(String(event.error && event.error.message || event.message));
  });
  window.addEventListener("unhandledrejection", function (event) {
    globalThis.__filesExportProbe.errors.push(String(event.reason && event.reason.message || event.reason));
  });
})();
</script>
` + tags.String() + `</head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  "use strict";
  var assert = __kitTestAssert;
  var files = globalThis.kit && globalThis.kit.files;
  var probe = globalThis.__filesExportProbe;
  var HOST = Symbol.for("kitwork:native:host:v1");
  assert(files && files.version === "1.2.0" && Object.isFrozen(files),
    "files@1.2.0 was not published as a frozen exact-version namespace");
  assert(Object.keys(files).sort().join(",") === "export,importBlob,pick,release,share,stat,toBlob",
    "files@1.2.0 public surface widened: " + Object.keys(files).sort().join(","));
  assert(files.beginExport === undefined && files.pollExport === undefined && files.releaseExport === undefined &&
    files.bridge === undefined && !Object.prototype.hasOwnProperty.call(document, HOST),
    "private export transport escaped the staged service boundary");

  var reference = await files.importBlob(new Blob(["browser database"], { type: "application/octet-stream" }),
    { name: "browser.db" });
  var result = await files.export(reference);
  assert(result === true && typeof result === "boolean" && !Object.prototype.hasOwnProperty.call(reference, "handle") &&
    JSON.stringify(reference).indexOf(probe.operation) < 0,
    "completed export exposed private authority or changed its public result");
  var actions = probe.calls.map(function (call) { return call.action; });
  assert(actions.join(",") === "files.beginImport,files.commitImport,files.beginExport,files.pollExport,files.pollExport,files.pollExport,files.releaseExport",
    "files export lifecycle changed: " + actions.join(","));
  assert(probe.calls.every(function (call) { return call.receiver && call.frozen; }) &&
    probe.calls[2].params.handle === "H".repeat(32) &&
    probe.calls.slice(3).every(function (call) { return call.params.operation === probe.operation; }) &&
    probe.errors.length === 0,
    "files export did not stay inside the frozen private native boundary");
});
</script></body></html>`
}
