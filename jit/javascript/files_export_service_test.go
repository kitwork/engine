package javascript

import (
	"bytes"
	"testing"
)

const filesService100SHA256 = "8d0fe543ad60721572bc3fa291f5e8fe20cb2328b297897104ab8c0abd67f27e"
const filesService110SHA256 = "7734137c3695d14754b3d76b8268130d3d37f6bcc0fd00dd3c35df75e6a1f46b"

func TestFilesExportServiceStaticContract(t *testing.T) {
	for version, want := range map[string]string{
		"1.0.0": filesService100SHA256,
		"1.1.0": filesService110SHA256,
	} {
		if got := ContentHash(readVanillaFile(t, "service", "files", version+".js")); got != want {
			t.Fatalf("files@%s bytes changed: %s", version, got)
		}
	}
	legacy := readVanillaFile(t, "service", "files", "1.1.0.js")
	if bytes.Contains(legacy, []byte(`export: exportFile`)) || bytes.Contains(legacy, []byte(`beginExport`)) {
		t.Fatal("files@1.1.0 changed after files@1.2.0 introduced export")
	}

	source := readVanillaFile(t, "service", "files", "1.2.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("files@1.2.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("files"`)); got != 1 {
		t.Fatalf("files@1.2.0 registration count = %d, want one", got)
	}
	for _, member := range []string{"pick", "importBlob", "stat", "toBlob", "share", "release"} {
		if !bytes.Contains(source, []byte(member+": "+member)) {
			t.Errorf("files@1.2.0 omitted inherited member %s", member)
		}
	}
	for _, contract := range [][]byte{
		[]byte(`export: exportFile`),
		[]byte(`var EXPORT_POLL_DELAY_MS = 250;`),
		[]byte(`var EXPORT_TIMEOUT_MS = 10 * 60 * 1000;`),
		[]byte(`var FILE_OPERATION = /^[A-Za-z0-9_-]{32}$/;`),
		[]byte(`nativeCall("beginExport", { handle: state.handle }`),
		[]byte(`nativeCall("pollExport", { operation: operation }`),
		[]byte(`nativeCall("releaseExport", { operation: operation }`),
		[]byte(`exportState === "completed"`),
		[]byte(`exportState === "cancelled"`),
		[]byte(`exportState === "failed"`),
	} {
		if !bytes.Contains(source, contract) {
			t.Errorf("files@1.2.0 lost export contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`beginExport: `), []byte(`pollExport: `), []byte(`releaseExport: `),
		[]byte(`globalThis.kit`), []byte(`window.kit`), []byte(`kit.component(`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Errorf("files@1.2.0 exposes forbidden public coupling %q", forbidden)
		}
	}
}

func TestFilesExportServiceNativeNodeContract(t *testing.T) {
	files := readVanillaFile(t, "service", "files", "1.2.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
var timerID = 0;
var exportTimeouts = new Map();
globalThis.setTimeout = function (callback, delay) {
  var id = ++timerID;
  if (delay === 250) queueMicrotask(callback);
  else if (delay === 10 * 60 * 1000) exportTimeouts.set(id, callback);
  else throw new Error("unexpected timer delay " + delay);
  return id;
};
globalThis.clearTimeout = function (id) { exportTimeouts.delete(id); };
function fireExportTimeouts() {
  var callbacks = Array.from(exportTimeouts.values());
  exportTimeouts.clear();
  callbacks.forEach(function (callback) { callback(); });
}

function FakeXHR() {
  this.upload = {};
  this.status = 0;
  this.responseURL = "";
}
FakeXHR.prototype.open = function (method, url, async) {
  assert(method === "POST" && async === true, "import transport changed");
  this.url = url;
};
FakeXHR.prototype.send = function (body) {
  var self = this;
  assert(body instanceof Blob && body.type === "application/octet-stream", "invalid import body");
  queueMicrotask(function () {
    self.status = 204;
    self.responseURL = self.url;
    if (self.upload.onprogress) self.upload.onprogress({ loaded: body.size, total: body.size });
    if (self.onload) self.onload();
  });
};
FakeXHR.prototype.abort = function () { if (this.onabort) this.onabort(); };
globalThis.XMLHttpRequest = FakeXHR;
globalThis.location = { origin: "https://app.test", href: "https://app.test/home" };

var calls = [];
var releaseOperations = [];
var importMetadata = null;
var exportMode = "completed";
var pollIndex = 0;
var reported = 0;
globalThis.reportError = function () { reported++; };
function operationForMode() {
  var character = exportMode === "cancelled" ? "B" : exportMode === "failed" ? "C" :
    exportMode === "malformedState" ? "D" : exportMode === "timeout" ? "E" :
    exportMode === "releaseReject" ? "F" : "A";
  return character.repeat(32);
}
var host = {
  version: "1.0.0",
  call: function (action, params) {
    calls.push({ action: action, params: params });
    if (action === "files.beginImport") {
      importMetadata = { name: params.name, type: params.type, size: params.size };
      return { handle: "H".repeat(32), mode: "http", uploadURL: "https://app.test/upload" };
    }
    if (action === "files.commitImport") {
      return { handle: "H".repeat(32), name: importMetadata.name, type: importMetadata.type,
        size: importMetadata.size, sha256: "0".repeat(64) };
    }
    if (action === "files.beginExport") {
      pollIndex = 0;
      if (exportMode === "beginDenied") return Promise.reject({ code: "DENIED", message: "private secret" });
      if (exportMode === "malformedOperation") return { operation: "too-short" };
      return { operation: operationForMode() };
    }
    if (action === "files.pollExport") {
      assert(params.operation === operationForMode(), "export operation changed between calls");
      if (exportMode === "malformedState") return { state: "unknown" };
      if (exportMode === "timeout") return new Promise(function () {});
      var sequences = {
        completed: ["pending", "writing", "completed"],
        releaseReject: ["completed"],
        cancelled: ["cancelled"],
        failed: ["failed"]
      };
      return { state: sequences[exportMode][pollIndex++] };
    }
    if (action === "files.releaseExport") {
      releaseOperations.push(params.operation);
      if (exportMode === "releaseReject") return Promise.reject({ code: "FAILED" });
      return true;
    }
    if (action === "files.release") return true;
    throw new Error("unexpected native action " + action);
  }
};
var document = {};
Object.defineProperty(document, ASSEMBLY, { value: { nativeHost: host, nativeFiles: null } });
globalThis.document = document;
var service = null;
var kit = { service: function (name, namespace) {
  assert(name === "files" && service === null, "files service registration changed");
  service = Object.freeze(namespace);
} };
` + string(files) + `

(async function () {
  assert(Object.keys(service).sort().join(",") === "export,importBlob,pick,release,share,stat,toBlob",
    "files@1.2.0 public surface widened");
  assert(service.beginExport === undefined && service.pollExport === undefined && service.releaseExport === undefined,
    "private export lifecycle escaped the namespace");
  var reference = await service.importBlob(new Blob(["database"], { type: "application/octet-stream" }),
    { name: "sample.db" });
  assert(Object.isFrozen(reference) && !Object.prototype.hasOwnProperty.call(reference, "handle"),
    "FileRef exposed its private handle");

  exportMode = "completed";
  var result = await service.export(reference);
  assert(result === true && releaseOperations.length === 1 && releaseOperations[0] === "A".repeat(32),
    "completed export did not resolve true and release its terminal operation");

  exportMode = "cancelled";
  result = await service.export(reference);
  assert(result === false && releaseOperations.length === 2 && releaseOperations[1] === "B".repeat(32),
    "cancelled export did not resolve false and release its terminal operation");

  exportMode = "failed";
  var failure = await rejected(service.export(reference));
  assert(failure.name === "KitFilesError" && failure.code === "FAILED" && failure.operation === "export" &&
    Object.isFrozen(failure) && releaseOperations.length === 3 && releaseOperations[2] === "C".repeat(32),
    "failed terminal export was not normalized and released");

  exportMode = "releaseReject";
  result = await service.export(reference);
  await Promise.resolve();
  assert(result === true && reported === 1 && releaseOperations.length === 4,
    "best-effort release changed a completed export result");

  exportMode = "beginDenied";
  failure = await rejected(service.export(reference));
  assert(failure.name === "KitFilesError" && failure.code === "DENIED" && failure.operation === "export" &&
    failure.message.indexOf("private secret") < 0,
    "private beginExport failure escaped public normalization");

  exportMode = "malformedOperation";
  var beforeRelease = releaseOperations.length;
  failure = await rejected(service.export(reference));
  assert(failure.name === "KitFilesError" && failure.code === "FAILED" && failure.operation === "export" &&
    releaseOperations.length === beforeRelease,
    "malformed operation token escaped validation or triggered cleanup without a terminal state");

  exportMode = "malformedState";
  failure = await rejected(service.export(reference));
  assert(failure.name === "KitFilesError" && failure.code === "FAILED" && failure.operation === "export" &&
    releaseOperations.length === beforeRelease,
    "malformed poll state escaped validation or was treated as terminal");

  exportMode = "timeout";
  var pending = service.export(reference);
  for (var turn = 0; turn < 8 && !calls.some(function (entry) {
    return entry.action === "files.pollExport" && entry.params.operation === "E".repeat(32);
  }); turn++) await Promise.resolve();
  fireExportTimeouts();
  failure = await rejected(pending);
  assert(failure.name === "KitFilesError" && failure.code === "TIMEOUT" && failure.operation === "export" &&
    releaseOperations.length === beforeRelease,
    "nonterminal timeout faked cancellation or released an active export");

  var beforeCalls = calls.length;
  var threw = false;
  try { service.export(Object.freeze({ name: "fake", type: "application/octet-stream", size: 1, sha256: "0".repeat(64) })); }
  catch (error) { threw = error instanceof TypeError; }
  assert(threw && calls.length === beforeCalls, "forged FileRef reached the native host");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
