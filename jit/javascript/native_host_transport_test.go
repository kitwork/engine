package javascript

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNativeHostTransportIsStagedOnly(t *testing.T) {
	marker := []byte(`kitwork:native:host:v1`)
	fileMarker := []byte(`kitwork:native:files:v1`)
	validationMarker := []byte(`withNativeServiceValidation`)
	windowService := platformServicePackage(t, "window")
	for _, profile := range []Profile{ProfileKit, ProfileHydrate} {
		source, err := SourceForProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(source, marker) || bytes.Contains(source, fileMarker) {
			t.Fatalf("%s public standalone profile contains the private native host slot", profile)
		}
		if bytes.Contains(source, validationMarker) {
			t.Fatalf("%s public standalone profile contains staged native service validation", profile)
		}
		artifact, err := Build(BuildOptions{
			Profile:  profile,
			Services: []Service{clipboardServicePackage(t)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(artifact.Bytes(), marker) || bytes.Contains(artifact.Bytes(), fileMarker) {
			t.Fatalf("%s monolithic service artifact contains the staged native host slot", profile)
		}
		if bytes.Contains(artifact.Bytes(), validationMarker) {
			t.Fatalf("%s monolithic service artifact contains staged native service validation", profile)
		}
	}
	if _, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{windowService}}); err == nil ||
		!strings.Contains(err.Error(), `invalid service name "window"`) {
		t.Fatalf("monolithic Build window service error = %v", err)
	}
	stagedWindow, err := BuildStaged(StagedBuildOptions{Profile: ProfileKit, Services: []Service{windowService}})
	if err != nil {
		t.Fatalf("staged window service rejected: %v", err)
	}
	if len(stagedWindow.Services) != 1 || stagedWindow.Services[0].Package() != "window" {
		t.Fatalf("staged window service artifacts = %#v", stagedWindow.Services)
	}
	if !bytes.Contains(stagedWindow.Graph.Bytes(), []byte(`core.withNativeServiceValidation(function ()`)) {
		t.Fatal("staged window graph does not scope native service validation")
	}
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if validAuthoredServiceAction("window", "minimize") || validAuthoredServiceAction("window", "drag") ||
		validAuthoredServiceAction("window", "isMaximized") ||
		appGrantsAuthoredService("1.1.0", "window") ||
		len(catalog.services["window"]["1.0.0"].actions) != 0 {
		t.Fatal("staged window service escaped into the authored app action policy")
	}
	blocked := windowService
	blocked.Name = "globalThis"
	if _, err := BuildStaged(StagedBuildOptions{Profile: ProfileKit, Services: []Service{blocked}}); err == nil {
		t.Fatal("staged service exception admitted another blocked global name")
	}
	if _, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileKit,
		Components: []ComponentPackage{{
			Name: "window", Version: "0.1.0", Source: []byte(";kit.component(\"window\", {});\n"),
		}},
	}); err == nil {
		t.Fatal("staged service exception admitted a blocked window component name")
	}

	assembly, err := BuildStaged(StagedBuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{clipboardServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(assembly.Runtime.Bytes(), nativeSource); got != 1 {
		t.Fatalf("staged runtime native host source count = %d, want 1", got)
	}
	for _, artifact := range assembly.Artifacts()[1:] {
		if bytes.Contains(artifact.Bytes(), marker) || bytes.Contains(artifact.Bytes(), fileMarker) {
			t.Fatalf("private native host slot escaped staged runtime into %s", artifact.Role())
		}
	}
	for _, contract := range [][]byte{
		[]byte(`var CONTRACT_VERSION = "1.0.0";`),
		[]byte(`var FILES_CONTRACT_VERSION = "1.0.0";`),
		[]byte(`!/^[a-z][A-Za-z0-9]*\.[a-z][A-Za-z0-9]*$/.test(action)`),
		[]byte(`var MAX_JSON_LENGTH = 8 * 1024 * 1024;`),
		[]byte(`var MAX_TREE_DEPTH = 32;`),
		[]byte(`var MAX_TREE_NODES = 4096;`),
		[]byte(`var MAX_PENDING = 64;`),
		[]byte(`var TIMEOUT_MS = 15000;`),
		[]byte(`var MAX_FILE_FRAME_BYTES = 4 + 24 + 4 + 256 * 1024;`),
		[]byte(`delete document[HOST]`),
		[]byte(`delete document[FILES]`),
	} {
		if !bytes.Contains(nativeSource, contract) {
			t.Fatalf("native host source lost exact contract %q", contract)
		}
	}
}

func TestNativeHostTransportNodeContract(t *testing.T) {
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var HOST = Symbol.for("kitwork:native:host:v1");
var NO_HOST = {};
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(promise) {
  try { await promise; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
function install(seed, target) {
  var document = target || {};
  var core = {
    phase: "events",
    reuse: false,
    blocked: function (name) { return name === "window" || name === "globalThis"; },
    validServiceName: function (name) { return name === "clipboard"; },
    installComponentGraph: function (source) { return source; },
    installStagedDelivery: function (source) { return source; },
    beginComponentHandoff: function () { return core.blocked("window"); }
  };
  Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
  if (seed !== NO_HOST) Object.defineProperty(document, HOST, { value: seed, configurable: true });
` + string(nativeSource) + `
  return { core: core, document: document };
}
(async function () {
  var absent = install(NO_HOST, {});
  assert(absent.core.nativeHost === null, "absent host did not install a null private seam");
  assert(absent.core.nativeFiles === null, "absent host did not install a null private file seam");
  assert(!Object.prototype.propertyIsEnumerable.call(absent.core, "nativeHost"), "private seam became enumerable");
  assert(absent.core.validServiceName("window") === true && absent.core.validServiceName("globalThis") === false,
    "staged service exception widened beyond window");
  assert(absent.core.blocked("window") === true &&
    absent.core.withNativeServiceValidation(function () { return absent.core.blocked("window"); }) === false &&
    absent.core.blocked("window") === true,
    "native service validation did not restore the blocked window identifier synchronously");
  var asyncValidation = absent.core.withNativeServiceValidation(function () {
    return Promise.resolve(absent.core.blocked("window"));
  });
  assert(absent.core.blocked("window") === true && await asyncValidation === false,
    "native service validation remained open across an async boundary");
  assert(absent.core.beginComponentHandoff() === false && absent.core.blocked("window") === true,
    "Drive handoff did not use the scoped native service validator");

  var invalidDocument = {};
  var invalidError = null;
  try { install({ version: "1.0.1", call: function () {} }, invalidDocument); }
  catch (error) { invalidError = error; }
  assert(invalidError && invalidError.name === "KitNativeHostError" && invalidError.code === "INVALID_HOST",
    "invalid exact host version was accepted");
  assert(!Object.prototype.hasOwnProperty.call(invalidDocument, HOST), "invalid raw host slot survived capture");

  var seen = null;
  var seeded = {
    version: "1.0.0",
    call: function (action, params) {
      seen = { receiver: this === seeded, action: action, params: params };
      return { ok: true, nested: [1, { ready: true }] };
    }
  };
  var installed = install(seeded, {});
  var transport = installed.core.nativeHost;
  assert(!Object.prototype.hasOwnProperty.call(installed.document, HOST), "raw host slot survived capture");
  assert(Object.isFrozen(transport) && transport.version === "1.0.0" && typeof transport.call === "function",
    "private transport wrapper is not exact and frozen");
  var original = { text: "hello", metadata: { values: [1, true, null] } };
  var result = await transport.call("clipboard.writeText", original);
  assert(result && result.ok === true && seen.receiver && seen.action === "clipboard.writeText" &&
    seen.params !== original && seen.params.text === "hello", "valid host call was not cloned and forwarded exactly");
  assert(Object.isFrozen(seen.params) && Object.isFrozen(seen.params.metadata) &&
    Object.isFrozen(seen.params.metadata.values), "host payload clone was not deeply frozen");
  assert(Object.isFrozen(result) && Object.isFrozen(result.nested) && Object.isFrozen(result.nested[1]),
    "host result clone was not deeply frozen");

  var failure = await rejected(transport.call("clipboard/writeText", {}));
  assert(failure.name === "KitNativeHostError" && failure.code === "INVALID_ACTION", "invalid action was accepted");
  var cyclic = {}; cyclic.self = cyclic;
  failure = await rejected(transport.call("clipboard.writeText", cyclic));
  assert(failure.code === "INVALID_PAYLOAD", "cyclic payload was accepted");
  var getterCalls = 0;
  var accessor = {};
  Object.defineProperty(accessor, "text", { enumerable: true, get: function () { getterCalls++; return "secret"; } });
  failure = await rejected(transport.call("clipboard.writeText", accessor));
  assert(failure.code === "INVALID_PAYLOAD" && getterCalls === 0, "payload getter executed during rejection");
  var toJSONCalls = 0;
  var customJSON = { text: "safe", toJSON: function () { toJSONCalls++; return { text: "unsafe" }; } };
  failure = await rejected(transport.call("clipboard.writeText", customJSON));
  assert(failure.code === "INVALID_PAYLOAD" && toJSONCalls === 0, "payload toJSON executed during rejection");
  var deep = {};
  var cursor = deep;
  for (var depth = 0; depth < 33; depth++) { cursor.next = {}; cursor = cursor.next; }
  failure = await rejected(transport.call("clipboard.writeText", deep));
  assert(failure.code === "INVALID_PAYLOAD", "payload depth bound exceeded 32");
  failure = await rejected(transport.call("clipboard.writeText", { values: new Array(4096).fill(null) }));
  assert(failure.code === "INVALID_PAYLOAD", "payload node bound exceeded 4096");
  var wideChange = {};
  for (var columnIndex = 0; columnIndex < 128; columnIndex++) wideChange["c" + columnIndex] = columnIndex;
  var manyChanges = Array.from({ length: 100 }, function () { return { original: Object.assign({}, wideChange), values: Object.assign({}, wideChange) }; });
  assert(await transport.call("studioSqlite.commit", { changes: manyChanges }), "bounded wide SQLite batch was blocked by generic node cap");
  failure = await rejected(transport.call("studioSqlite.query", { changes: manyChanges }));
  assert(failure.code === "INVALID_PAYLOAD", "SQLite commit node allowance leaked to ordinary queries");
  var traceTransport = install({version:"1.0.0",call:function(){return {statements:Array.from({length:200},function(){return {
    parameters:Array.from({length:256},function(){return {type:"blob",base64:"",bytes:0};})
  };})};}}, {}).core.nativeHost;
  var wideTrace = await traceTransport.call("studioSqlite.commit", {});
  assert(wideTrace.statements.length === 200 && Object.isFrozen(wideTrace.statements[0].parameters[0]), "bounded BLOB commit traces exceeded node budget");
  failure = await rejected(transport.call("clipboard.writeText", { text: "x".repeat(8 * 1024 * 1024) }));
  assert(failure.code === "INVALID_PAYLOAD", "oversized payload was accepted");

  var denied = install({
    version: "1.0.0",
    call: function () { throw { code: "DENIED", message: "raw host secret" }; }
  }, {}).core.nativeHost;
  failure = await rejected(denied.call("clipboard.writeText", {}));
  assert(failure.code === "DENIED" && failure.message.indexOf("raw host secret") < 0,
    "host denial was not normalized");
  var publicCodes = [
    "UNAVAILABLE", "BUSY", "NOT_FOUND", "INVALID_REQUEST", "INVALID_DATABASE",
    "DATABASE_CORRUPT", "SCHEMA_CHANGED", "READ_ONLY_REQUIRED", "SQL_ERROR",
    "LIMIT", "UNSUPPORTED", "TIMEOUT", "CANCELLED", "DENIED", "FAILED",
    "WRITE_UNAVAILABLE", "READ_ONLY_TABLE", "INVALID_CHANGE", "ROW_CONFLICT", "CONSTRAINT_FAILED"
  ];
  for (var publicIndex = 0; publicIndex < publicCodes.length; publicIndex++) {
    var publicCode = publicCodes[publicIndex];
    var publicFailure = install({
      version: "1.0.0",
      call: (function (code) {
        return function () { return Promise.reject({ code: code, message: "raw native secret" }); };
      })(publicCode)
    }, {}).core.nativeHost;
    failure = await rejected(publicFailure.call("studioSqlite.query", {}));
    assert(failure.code === publicCode && failure.message.indexOf("raw native secret") < 0,
      publicCode + " was not preserved without raw detail");
  }
  var bridgeCodes = {
    BRIDGE_BUSY: "OVERLOADED",
    BRIDGE_TIMEOUT: "TIMEOUT",
    BRIDGE_UNAVAILABLE: "UNAVAILABLE"
  };
  for (var wireCode in bridgeCodes) {
    var bridgeFailure = install({
      version: "1.0.0",
      call: function () { return Promise.reject({ code: wireCode, message: "raw bridge secret" }); }
    }, {}).core.nativeHost;
    failure = await rejected(bridgeFailure.call("files.stat", {}));
    assert(failure.code === bridgeCodes[wireCode] && failure.message.indexOf("raw bridge secret") < 0,
      wireCode + " was not normalized to " + bridgeCodes[wireCode]);
  }
  var unknown = install({
    version: "1.0.0",
    call: function () { return Promise.reject({ code: "PRIVATE_HOST_CODE", message: "secret" }); }
  }, {}).core.nativeHost;
  failure = await rejected(unknown.call("window.restore", {}));
  assert(failure.code === "FAILED" && failure.message.indexOf("secret") < 0,
    "unknown host failure escaped normalization");
  var oversizedResult = install({
    version: "1.0.0",
    call: function () { return "x".repeat(8 * 1024 * 1024 + 1); }
  }, {}).core.nativeHost;
  failure = await rejected(oversizedResult.call("clipboard.readText", {}));
  assert(failure.code === "INVALID_RESULT", "oversized host result was accepted");
  var resultGetterCalls = 0;
  var accessorResult = install({
    version: "1.0.0",
    call: function () {
      var value = {};
      Object.defineProperty(value, "text", { enumerable: true, get: function () { resultGetterCalls++; return "secret"; } });
      return value;
    }
  }, {}).core.nativeHost;
  failure = await rejected(accessorResult.call("clipboard.readText", {}));
  assert(failure.code === "INVALID_RESULT" && resultGetterCalls === 0, "result getter executed during rejection");

  var oldSetTimeout = globalThis.setTimeout;
  var oldClearTimeout = globalThis.clearTimeout;
  var timeoutCallback = null;
  var timeoutDelay = 0;
  globalThis.setTimeout = function (callback, delay) { timeoutCallback = callback; timeoutDelay = delay; return 7; };
  globalThis.clearTimeout = function () {};
  var timed = install({
    version: "1.0.0",
    call: function () { return new Promise(function () {}); }
  }, {}).core.nativeHost;
  var timedPromise = timed.call("window.minimize", {});
  assert(timeoutDelay === 15000 && typeof timeoutCallback === "function", "transport timeout is not exactly 15000ms");
  timeoutCallback();
  failure = await rejected(timedPromise);
  assert(failure.code === "TIMEOUT", "timed out call did not reject with TIMEOUT");

  var overloaded = install({
    version: "1.0.0",
    call: function () { return new Promise(function () {}); }
  }, {}).core.nativeHost;
  var hanging = [];
  for (var index = 0; index < 64; index++) hanging.push(overloaded.call("window.restore", {}));
  failure = await rejected(overloaded.call("window.restore", {}));
  assert(failure.code === "OVERLOADED" && hanging.length === 64, "pending bound is not exactly 64");
  globalThis.setTimeout = oldSetTimeout;
  globalThis.clearTimeout = oldClearTimeout;
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func TestNativeFileTransportNodeContract(t *testing.T) {
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var HOST = Symbol.for("kitwork:native:host:v1");
var FILES = Symbol.for("kitwork:native:files:v1");
var NO_FILES = {};
function assert(value, message) { if (!value) throw new Error(message); }
function transport(seed, fileSeed) {
  var document = {};
  var core = {
    phase: "events",
    reuse: false,
    blocked: function () { return false; },
    validServiceName: function (name) { return name === "files"; },
    installComponentGraph: function (source) { return source; },
    installStagedDelivery: function (source) { return source; },
    beginComponentHandoff: function () { return false; }
  };
  Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
  if (seed !== null) Object.defineProperty(document, HOST, { value: seed, configurable: true });
  if (fileSeed !== NO_FILES) Object.defineProperty(document, FILES, { value: fileSeed, configurable: true });
` + string(nativeSource) + `
  return { core: core, document: document };
}
var host = { version: "1.0.0", call: function () { return true; } };
var admitted = null;
var receiver = false;
var fileSeed = {
  version: "1.0.0",
  send: function (frame) {
    receiver = this === fileSeed;
    admitted = frame;
    return true;
  }
};
var installed = transport(host, fileSeed);
assert(!Object.prototype.hasOwnProperty.call(installed.document, HOST) &&
  !Object.prototype.hasOwnProperty.call(installed.document, FILES), "raw native slots survived capture");
assert(Object.isFrozen(installed.core.nativeFiles) && installed.core.nativeFiles.version === "1.0.0" &&
  typeof installed.core.nativeFiles.send === "function" &&
  !Object.prototype.propertyIsEnumerable.call(installed.core, "nativeFiles"),
  "private native file facade is not exact, frozen, and non-enumerable");
var frame = new ArrayBuffer(33);
var bytes = new Uint8Array(frame);
bytes[0] = 0x4b; bytes[1] = 0x57; bytes[2] = 0x46; bytes[3] = 0x31; bytes[32] = 0x7a;
assert(installed.core.nativeFiles.send(frame) === true && receiver && admitted !== frame,
  "valid KWF1 frame was not copied into the file adapter");
bytes[32] = 0;
assert(new Uint8Array(admitted)[32] === 0x7a, "caller mutation changed an admitted file frame");

for (var candidate of [new ArrayBuffer(32), new ArrayBuffer(32 + 256 * 1024 + 1), new Uint8Array(33)]) {
  var failure = null;
  try { installed.core.nativeFiles.send(candidate); } catch (error) { failure = error; }
  assert(failure && failure.name === "KitNativeHostError" && failure.code === "INVALID_PAYLOAD",
    "invalid file frame escaped exact admission");
}
var wrongHeader = new ArrayBuffer(33);
var failure = null;
try { installed.core.nativeFiles.send(wrongHeader); } catch (error) { failure = error; }
assert(failure && failure.code === "INVALID_PAYLOAD", "non-KWF1 frame was admitted");

var rejectedDocument = null;
try { transport(host, { version: "1.0.1", send: function () { return true; } }); }
catch (error) { rejectedDocument = error; }
assert(rejectedDocument && rejectedDocument.name === "KitNativeHostError" &&
  rejectedDocument.code === "INVALID_HOST", "wrong native file contract version was accepted");
failure = null;
try { transport(null, fileSeed); } catch (error) { failure = error; }
assert(failure && failure.code === "INVALID_HOST", "binary data plane was admitted without its control plane");
var declined = transport(host, { version: "1.0.0", send: function () { return false; } });
failure = null;
try { declined.core.nativeFiles.send(frame); } catch (error) { failure = error; }
assert(failure && failure.code === "INVALID_RESULT", "false file adapter admission escaped normalization");
var denied = transport(host, { version: "1.0.0", send: function () {
  throw { code: "DENIED", message: "raw binary secret" };
} });
failure = null;
try { denied.core.nativeFiles.send(frame); } catch (error) { failure = error; }
assert(failure && failure.code === "DENIED" && failure.message.indexOf("raw binary secret") < 0,
  "file adapter denial escaped normalization");
var noFiles = transport(host, NO_FILES);
assert(noFiles.core.nativeFiles === null, "absent native file adapter did not fail closed");
`
	runNativeHostNode(t, script)
}

func TestClipboardAndWindowNativeAdaptersNodeContract(t *testing.T) {
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	clipboard := readVanillaFile(t, "service", "clipboard", "1.0.0.js")
	windowService := readVanillaFile(t, "service", "window", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var HOST = Symbol.for("kitwork:native:host:v1");
var NO_HOST = {};
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(promise) {
  try { await promise; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
function installTransport(seed) {
  var document = {};
  var core = {
    phase: "events",
    reuse: false,
    blocked: function (name) { return name === "window" || name === "globalThis"; },
    validServiceName: function (name) { return name === "clipboard"; },
    installComponentGraph: function (source) { return source; },
    installStagedDelivery: function (source) { return source; },
    beginComponentHandoff: function () { return core.blocked("window"); }
  };
  Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
  if (seed !== NO_HOST) Object.defineProperty(document, HOST, { value: seed, configurable: true });
` + string(nativeSource) + `
  return { core: core, document: document };
}
function installServices(installed) {
  globalThis.document = installed.document;
  var namespaces = Object.create(null);
  var kit = { service: function (name, namespace) { namespaces[name] = Object.freeze(namespace); } };
` + string(clipboard) + string(windowService) + `
  return namespaces;
}
(async function () {
  var calls = [];
  var mode = "success";
  var nativeMaximized = false;
  var writes = [];
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { clipboard: {
      writeText: function (value) { writes.push(value); return Promise.resolve(); },
      readText: function () { return Promise.resolve("browser clipboard"); }
    } }
  });
  var seeded = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === seeded });
      if (mode === "denied") return Promise.reject({ code: "DENIED", message: "raw native secret" });
      if (mode === "false" && action.indexOf("window.") === 0) return false;
      if (mode === "object" && action.indexOf("window.") === 0) return { ok: true };
      if (action === "window.isMaximized") return nativeMaximized;
      if (action === "window.maximize") nativeMaximized = true;
      if (action === "window.restore") nativeMaximized = false;
      if (action.indexOf("window.") === 0) return true;
      if (action === "clipboard.readText") return "native clipboard";
      if (action === "clipboard.writeText") return true;
      return undefined;
    }
  };
  var installed = installTransport(seeded);
  var services = installServices(installed);
  assert(!Object.prototype.hasOwnProperty.call(installed.document, HOST), "raw host slot survived service install");
  assert(await services.clipboard.writeText("native write") === undefined, "native clipboard write leaked host result");
  assert(await services.clipboard.readText() === "native clipboard", "clipboard read bypassed its native adapter");
  assert(await services.window.isMaximized() === false, "native window maximized query changed false");
  assert(await services.window.maximize() === true && await services.window.isMaximized() === true,
    "native window maximize/query state drifted");
  assert(await services.window.restore() === true, "native window restore changed host result");
  assert(await services.window.isMaximized() === false, "native window restore/query state drifted");
  assert(calls.length === 7 && calls[0].receiver && calls[0].action === "clipboard.writeText" &&
    calls[0].params.text === "native write" && Object.isFrozen(calls[0].params) &&
    calls.slice(1).every(function (call) { return call.receiver && Object.isFrozen(call.params); }) &&
    calls[1].action === "clipboard.readText" && calls[2].action === "window.isMaximized" &&
    calls[3].action === "window.maximize" && calls[4].action === "window.isMaximized" &&
    calls[5].action === "window.restore" && calls[6].action === "window.isMaximized",
    "native service actions, payloads, or frozen boundary drifted");
  mode = "denied";
  var failure = await rejected(services.clipboard.writeText("private"));
  assert(failure.name === "KitClipboardError" && failure.code === "DENIED" &&
    failure.message.indexOf("raw native secret") < 0, "clipboard native denial escaped service normalization");
  failure = await rejected(services.window.close());
  assert(failure.name === "KitWindowError" && failure.code === "DENIED" &&
    failure.message.indexOf("raw native secret") < 0, "window native denial escaped service normalization");
  mode = "false";
  failure = await rejected(services.window.minimize());
  assert(failure.name === "KitWindowError" && failure.code === "FAILED",
    "false native window result escaped the exact boolean contract");
  mode = "object";
  failure = await rejected(services.window.isMaximized());
  assert(failure.name === "KitWindowError" && failure.code === "FAILED" &&
    failure.operation === "isMaximized", "object native window query escaped the exact boolean contract");
  failure = await rejected(services.window.maximize());
  assert(failure.name === "KitWindowError" && failure.code === "FAILED",
    "object native window result escaped the exact boolean contract");

  var browser = installServices(installTransport(NO_HOST));
  assert(await browser.clipboard.writeText("browser write") === undefined && writes[0] === "browser write",
    "clipboard browser write fallback changed");
  assert(await browser.clipboard.readText() === "browser clipboard", "clipboard browser read fallback changed");
  assert(await browser.window.minimize() === false, "browser window fallback must resolve false");
  assert(await browser.window.isMaximized() === false, "browser maximized query fallback must resolve false");
  assert(browser.clipboard.bridge === undefined && browser.window.bridge === undefined,
    "service namespace exposed the private transport");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func TestAppearanceNativeTitlebarAdapterNodeContract(t *testing.T) {
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	appearance := readVanillaFile(t, "service", "appearance", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var HOST = Symbol.for("kitwork:native:host:v1");
function assert(value, message) { if (!value) throw new Error(message); }
var dark = true;
var mediaListeners = [];
var classes = new Set();
var document = {
  documentElement: {
    classList: {
      add: function (name) { classes.add(name); },
      remove: function (name) { classes.delete(name); },
      contains: function (name) { return classes.has(name); }
    },
    style: {}
  }
};
var core = {
  phase: "events",
  reuse: false,
  blocked: function (name) { return name === "window" || name === "globalThis"; },
  validServiceName: function (name) { return name === "appearance"; },
  installComponentGraph: function (source) { return source; },
  installStagedDelivery: function (source) { return source; },
  beginComponentHandoff: function () { return false; }
};
Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
var calls = [];
var seeded = {
  version: "1.0.0",
  call: function (action, params) {
    calls.push({ action: action, params: params, receiver: this === seeded });
    return true;
  }
};
Object.defineProperty(document, HOST, { value: seeded, configurable: true });
globalThis.document = document;
globalThis.localStorage = {
  getItem: function () { return null; },
  setItem: function () {}
};
var media = {
  get matches() { return dark; },
  addEventListener: function (type, listener) {
    if (type === "change") mediaListeners.push(listener);
  }
};
globalThis.matchMedia = function (query) {
  assert(query === "(prefers-color-scheme: dark)", "appearance used the wrong media query");
  return media;
};
globalThis.addEventListener = function () {};
` + string(nativeSource) + `
var services = Object.create(null);
var kit = { service: function (name, namespace) { services[name] = Object.freeze(namespace); } };
` + string(appearance) + `
(async function () {
  await Promise.resolve();
  assert(!Object.prototype.hasOwnProperty.call(document, HOST), "raw native host survived runtime capture");
  assert(calls.length === 1 && calls[0].action === "appearance.setResolved" &&
    calls[0].params.resolved === "dark" && calls[0].receiver,
    "initial resolved dark appearance did not reach the native title bar");

  services.appearance.toggle();
  services.appearance.system();
  dark = false;
  mediaListeners.slice().forEach(function (listener) { listener({ matches: false }); });
  services.appearance.set("light");
  await Promise.resolve();

  var expected = ["dark", "light", "dark", "light"];
  assert(calls.length === expected.length, "appearance emitted duplicate or missing native updates: " + calls.length);
  calls.forEach(function (call, index) {
    assert(call.action === "appearance.setResolved", "appearance widened the native action at " + index);
    assert(Object.keys(call.params).join(",") === "resolved" && call.params.resolved === expected[index],
      "appearance native payload was not the exact resolved enum at " + index);
  });
  assert(services.appearance.bridge === undefined && services.appearance.nativeHost === undefined &&
    services.appearance.dispatch === undefined,
    "appearance exposed raw native dispatch controls");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func runNativeHostNode(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	var command *exec.Cmd
	if len(script) > 24*1024 {
		path := filepath.Join(t.TempDir(), "native-host-contract.js")
		if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		command = exec.Command(node, path)
	} else {
		command = exec.Command(node, "-e", script)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		detail := ""
		marker := "native-host-contract.js:"
		if at := strings.LastIndex(string(output), marker); at >= 0 {
			rest := string(output)[at+len(marker):]
			if end := strings.IndexByte(rest, ':'); end > 0 {
				if line, parseErr := strconv.Atoi(rest[:end]); parseErr == nil {
					lines := strings.Split(script, "\n")
					first := line - 2
					if first < 1 {
						first = 1
					}
					last := line + 2
					if last > len(lines) {
						last = len(lines)
					}
					for index := first; index <= last; index++ {
						detail += "\n" + strconv.Itoa(index) + ": " + lines[index-1]
					}
				}
			}
		}
		t.Fatalf("node native host contract failed: %v\n%s%s", err, strings.TrimSpace(string(output)), detail)
	}
}
