package javascript

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCameraServiceStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "camera", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("camera@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("camera"`)); got != 1 {
		t.Fatalf("camera registration count = %d, want one", got)
	}
	for _, contract := range [][]byte{
		[]byte(`available: available`),
		[]byte(`capture: capture`),
		[]byte(`supportsCapability.call(capabilities, "camera.capture")`),
		[]byte(`nativeCall("beginCapture", { facing: facing }`),
		[]byte(`nativeCall("pollCapture", { operation: state.operation }`),
		[]byte(`nativeCall("releaseCapture", { operation: state.operation }`),
		[]byte(`var POLL_DELAY_MS = 250;`),
		[]byte(`var CAPTURE_TIMEOUT_MS = 10 * 60 * 1000;`),
		[]byte(`var CAPTURE_MAX_POLLS = 2400;`),
		[]byte(`name: { value: "KitCameraError" }`),
		[]byte(`TOO_LARGE: "Captured photo is too large"`),
		[]byte(`code === "CONFLICT") return "OVERLOADED"`),
		[]byte(`exactObject(value, ["status", "code"])`),
		[]byte(`facing !== "user" && facing !== "environment"`),
	} {
		if !bytes.Contains(source, contract) {
			t.Errorf("camera@1.0.0 lost contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`globalThis.kit`), []byte(`global.kit`), []byte(`window.kit`),
		[]byte(`kit.component(`), []byte(`beginCapture: `), []byte(`pollCapture: `),
		[]byte(`releaseCapture: `), []byte(`path`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Errorf("camera@1.0.0 exposes forbidden coupling %q", forbidden)
		}
	}

	for _, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		legacy := readVanillaFile(t, "service", "files", version+".js")
		if bytes.Contains(legacy, []byte(`kitjs:files:adopt:v1`)) {
			t.Fatalf("files@%s changed after the private adoption seam was introduced", version)
		}
	}
	files130 := readVanillaFile(t, "service", "files", "1.3.0.js")
	for _, contract := range [][]byte{
		[]byte(`KitJS staged service: files@1.3.0`),
		[]byte(`graph.services.files !== "1.3.0"`),
		[]byte(`graph.services.camera !== "1.0.0"`),
		[]byte(`return makeRef(metadata(value));`),
		[]byte(`installFileAdopter();`),
	} {
		if !bytes.Contains(files130, contract) {
			t.Errorf("files@1.3.0 lost private adoption contract %q", contract)
		}
	}
	if bytes.Contains(files130, []byte(`adopt: `)) {
		t.Fatal("files@1.3.0 widened its public FileRef surface")
	}
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.3.0.js"),
		readVanillaFile(t, "component", "app", "1.4.0.js")) {
		t.Fatal("app@1.4.0 changed component behavior instead of only closing a newer service graph")
	}
}

func TestCameraCatalogClosesExactSealedGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	app140, err := composer.catalog.component("app", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	requires := make(map[string]string, len(app140.requires))
	for _, service := range app140.requires {
		requires[service.Name] = service.Version
	}
	if requires["camera"] != "1.0.0" || requires["capabilities"] != "1.0.0" ||
		requires["files"] != "1.3.0" {
		t.Fatalf("app@1.4.0 native requirements = %#v", requires)
	}
	if appGrantsAuthoredService("1.4.0", "camera") || validAuthoredServiceAction("camera", "capture") {
		t.Fatal("camera escaped into authored HTML actions")
	}
	camera, err := composer.catalog.service(ServiceVersion{Name: "camera", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(camera.actions) != 0 || len(camera.requires) != 2 ||
		camera.requires[0] != (ServiceVersion{Name: "capabilities", Version: "1.0.0"}) ||
		camera.requires[1] != (ServiceVersion{Name: "files", Version: "1.3.0"}) {
		t.Fatalf("camera catalog definition = %#v", camera)
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "camera", Version: "1.0.1"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown camera version error = %v", err)
	}

	bundle, err := composer.ComposeHTML([]byte(`<main data-kit-component="app@1.4.0" data-kit-as="$app"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.4.0"`),
		[]byte(`services["camera"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
		[]byte(`grants["app"]["camera"] = "1.0.0"`),
		[]byte(`grants["app"]["files"] = "1.3.0"`),
	} {
		if !bytes.Contains(bundle.JavaScript, marker) {
			t.Fatalf("app@1.4.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(bundle.JavaScript, []byte(`actions["camera"]["`)) {
		t.Fatal("camera gained an authored action manifest")
	}
	filesIndex := bytes.Index(bundle.JavaScript, []byte(`KitJS staged service: files@1.3.0`))
	cameraIndex := bytes.Index(bundle.JavaScript, []byte(`KitJS staged service: camera@1.0.0`))
	if filesIndex < 0 || cameraIndex < 0 || filesIndex >= cameraIndex {
		t.Fatalf("service order files=%d camera=%d, want files before camera", filesIndex, cameraIndex)
	}

	legacy, err := composer.ComposeHTML([]byte(`<main data-kit-component="app@1.3.0" data-kit-as="$app"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy.JavaScript, []byte(`services["camera"]`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`kitjs:files:adopt:v1`)) {
		t.Fatal("app@1.3.0 changed when app@1.4.0 introduced camera")
	}
	if strings.Contains(string(bundle.JavaScript), `$app.camera`) {
		t.Fatal("camera package invented an authored action alias")
	}
}

func TestCameraNativeNodeContract(t *testing.T) {
	files := readVanillaFile(t, "service", "files", "1.3.0.js")
	camera := readVanillaFile(t, "service", "camera", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var ADOPTER = Symbol.for("kitjs:files:adopt:v1");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
async function turns(count) { while (count-- > 0) await Promise.resolve(); }

var nextTimer = 0;
var deadlines = new Map();
globalThis.setTimeout = function (callback, delay) {
  var id = ++nextTimer;
  if (delay === 250) queueMicrotask(callback);
  else if (delay === 10 * 60 * 1000) deadlines.set(id, callback);
  else throw new Error("unexpected timer delay " + delay);
  return id;
};
globalThis.clearTimeout = function (id) { deadlines.delete(id); };
function fireDeadlines() {
  var callbacks = Array.from(deadlines.values());
  deadlines.clear();
  callbacks.forEach(function (callback) { callback(); });
}

function install(host, support) {
  var document = {};
  var graph = { services: { capabilities: "1.0.0", files: "1.3.0", camera: "1.0.0" } };
  var core = { graph: graph, nativeHost: host, nativeFiles: null };
  Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
  globalThis.document = document;
  var namespaces = Object.create(null);
  var capabilities = Object.freeze({
    supports: function (id) { return support(id); }
  });
  var kit = {
    capabilities: capabilities,
    service: function (name, namespace) {
      assert(!Object.prototype.hasOwnProperty.call(namespaces, name), "duplicate service " + name);
      namespaces[name] = Object.freeze(namespace);
      Object.defineProperty(kit, name, { value: namespaces[name], enumerable: true });
    }
  };
` + string(files) + string(camera) + `
  return { document: document, core: core, kit: kit, services: namespaces };
}

(async function () {
  var calls = [];
  var released = [];
  var reported = 0;
  var supportMode = "yes";
  var mode = "success";
  var failedCode = "FAILED";
  var pollCount = 0;
  var delayedBeginResolve = null;
  var operation = "O".repeat(32);
  var metadata = {
    handle: "H".repeat(32), name: "photo.jpg", type: "image/jpeg", size: 5, sha256: "a".repeat(64)
  };
  globalThis.reportError = function () { reported++; };
  function support(id) {
    assert(id === "camera.capture", "available queried a noncanonical capability");
    if (supportMode === "error") return Promise.reject({ code: "DENIED", message: "raw support secret" });
    return Promise.resolve(supportMode === "yes");
  }
  var host = {
    call: function (action, params) {
      calls.push({ action: action, params: params });
      if (action === "camera.beginCapture") {
        pollCount = 0;
        if (mode === "denied") return Promise.reject({ code: "DENIED", message: "raw camera secret" });
        if (mode === "conflict") return Promise.reject({ code: "CONFLICT", message: "raw active operation" });
        if (mode === "badOperation") return { operation: "short" };
        if (mode === "delayedBegin") return new Promise(function (resolve) { delayedBeginResolve = resolve; });
        return { operation: operation };
      }
      if (action === "camera.pollCapture") {
        assert(params.operation === operation, "poll operation changed");
        pollCount++;
        if (mode === "hold") return new Promise(function () {});
        if (mode === "foreverPending") return { status: "pending" };
        if (mode === "cancelled") return { status: "cancelled" };
        if (mode === "failed") return { status: "failed", code: failedCode };
        if (mode === "failedMissingCode") return { status: "failed" };
        if (mode === "failedInvalidCode") return { status: "failed", code: "CANCELLED" };
        if (mode === "malformed") return { status: "ready", file: metadata, secret: true };
        if (mode === "badFile") return { status: "ready", file: {
          handle: "short", name: metadata.name, type: metadata.type, size: metadata.size, sha256: metadata.sha256
        } };
        if (pollCount === 1) return { status: "pending" };
        return { status: "ready", file: metadata };
      }
      if (action === "camera.releaseCapture") {
        released.push(params.operation);
        if (mode === "releaseReject") return Promise.reject({ code: "FAILED", message: "raw release secret" });
        return true;
      }
      if (action === "files.stat") return metadata;
      if (action === "files.release") return true;
      throw new Error("unexpected native action " + action);
    }
  };

  var installed = install(host, support);
  var service = installed.services.camera;
  var filesService = installed.services.files;
  assert(!Object.prototype.hasOwnProperty.call(installed.core, ADOPTER),
    "private FileRef adopter survived camera registration");
  assert(Object.keys(service).sort().join(",") === "available,capture" &&
    service.beginCapture === undefined && service.pollCapture === undefined && service.releaseCapture === undefined,
    "camera public surface widened");
  assert(await service.available() === true, "available did not negotiate camera.capture");
  supportMode = "no";
  assert(await service.available() === false, "available changed a false capability result");
  supportMode = "error";
  var failure = await rejected(service.available());
  assert(failure.name === "KitCameraError" && failure.code === "DENIED" &&
    failure.operation === "available" && failure.message.indexOf("raw support secret") < 0 && Object.isFrozen(failure),
    "available failure escaped stable normalization");
  supportMode = "yes";

  var reference = await service.capture();
  assert(Object.isFrozen(reference) && Object.keys(reference).sort().join(",") === "name,sha256,size,type" &&
    reference.handle === undefined && reference.name === metadata.name && reference.sha256 === metadata.sha256,
    "capture did not mint the existing opaque FileRef shape");
  var begin = calls.find(function (entry) { return entry.action === "camera.beginCapture"; });
  assert(Object.keys(begin.params).join(",") === "facing" && begin.params.facing === "environment",
    "capture default facing was not canonical");
  assert(released.length === 1 && released[0] === operation, "successful capture did not release its operation");
  var stat = await filesService.stat(reference);
  assert(Object.isFrozen(stat) && stat !== reference && stat.sha256 === reference.sha256,
    "captured FileRef was not recognized by the exact files service");
  assert(await filesService.release(reference) === true && await filesService.release(stat) === true,
    "captured FileRef did not share normal release state");

  mode = "success";
  reference = await service.capture({ facing: "user" });
  assert(calls.filter(function (entry) { return entry.action === "camera.beginCapture"; }).slice(-1)[0]
    .params.facing === "user", "user-facing capture was not canonical");
  await filesService.release(reference);

  mode = "cancelled";
  var beforeRelease = released.length;
  assert(await service.capture() === null && released.length === beforeRelease + 1,
    "native camera cancellation did not resolve null and release its operation");

  for (var failureIndex = 0; failureIndex < 4; failureIndex++) {
    failedCode = ["DENIED", "UNAVAILABLE", "TOO_LARGE", "FAILED"][failureIndex];
    mode = "failed";
    beforeRelease = released.length;
    failure = await rejected(service.capture());
    assert(failure.name === "KitCameraError" && failure.code === failedCode &&
      failure.operation === "capture" && Object.isFrozen(failure) &&
      released.length === beforeRelease + 1,
      "failed poll code " + failedCode + " was not normalized and released");
    if (failedCode === "TOO_LARGE") {
      assert(failure.message === "Captured photo is too large", "TOO_LARGE public message drifted");
    }
  }

  for (var invalidFailureIndex = 0; invalidFailureIndex < 2; invalidFailureIndex++) {
    mode = invalidFailureIndex === 0 ? "failedMissingCode" : "failedInvalidCode";
    beforeRelease = released.length;
    failure = await rejected(service.capture());
    assert(failure.code === "FAILED" && released.length === beforeRelease + 1,
      "noncanonical failed envelope escaped validation or cleanup");
  }

  mode = "malformed";
  beforeRelease = released.length;
  failure = await rejected(service.capture());
  assert(failure.code === "FAILED" && released.length === beforeRelease + 1,
    "non-exact ready envelope escaped validation or cleanup");

  mode = "badFile";
  beforeRelease = released.length;
  failure = await rejected(service.capture());
  assert(failure.code === "FAILED" && released.length === beforeRelease + 1,
    "malformed file metadata escaped private adoption or cleanup");

  mode = "denied";
  beforeRelease = released.length;
  failure = await rejected(service.capture());
  assert(failure.name === "KitCameraError" && failure.code === "DENIED" &&
    failure.message.indexOf("raw camera secret") < 0 && released.length === beforeRelease,
    "begin denial escaped normalization or released an unpublished operation");

  mode = "conflict";
  failure = await rejected(service.capture());
  assert(failure.name === "KitCameraError" && failure.code === "OVERLOADED" &&
    failure.message.indexOf("raw active operation") < 0 && released.length === beforeRelease,
    "native begin conflict was not defensively normalized to OVERLOADED");

  mode = "badOperation";
  failure = await rejected(service.capture());
  assert(failure.code === "FAILED" && released.length === beforeRelease,
    "malformed operation token escaped validation or cleanup authority");

  mode = "hold";
  var controller = new AbortController();
  var pending = service.capture({ signal: controller.signal });
  await turns(8);
  failure = await rejected(service.capture());
  assert(failure.code === "OVERLOADED", "camera allowed two active capture operations");
  beforeRelease = released.length;
  controller.abort();
  failure = await rejected(pending);
  assert(failure.code === "CANCELLED", "in-flight abort did not reject with CANCELLED");
  await turns(8);
  assert(released.length === beforeRelease + 1, "in-flight abort did not release its operation");

  mode = "delayedBegin";
  controller = new AbortController();
  pending = service.capture({ signal: controller.signal });
  await turns(4);
  assert(typeof delayedBeginResolve === "function", "delayed begin was not installed");
  controller.abort();
  failure = await rejected(pending);
  assert(failure.code === "CANCELLED", "delayed begin abort did not reject promptly");
  failure = await rejected(service.capture());
  assert(failure.code === "OVERLOADED", "delayed begin abort released the one-operation slot too early");
  beforeRelease = released.length;
  delayedBeginResolve({ operation: operation });
  await turns(12);
  assert(released.length === beforeRelease + 1, "late operation publication leaked after abort");

  mode = "hold";
  pending = service.capture();
  await turns(8);
  beforeRelease = released.length;
  fireDeadlines();
  failure = await rejected(pending);
  assert(failure.code === "TIMEOUT" && released.length === beforeRelease + 1,
    "ten-minute deadline did not reject and release the operation");
  await turns(8);

  mode = "foreverPending";
  beforeRelease = released.length;
  failure = await rejected(service.capture());
  assert(failure.code === "TIMEOUT" && pollCount === 2400 && released.length === beforeRelease + 1 &&
    deadlines.size === 0, "bounded 250ms polling exceeded its 2400-poll ceiling or leaked its deadline");
  await turns(8);

  mode = "releaseReject";
  beforeRelease = released.length;
  var beforeReported = reported;
  reference = await service.capture();
  assert(reference.name === metadata.name && released.length === beforeRelease + 1 && reported === beforeReported + 1,
    "best-effort operation release changed a valid captured FileRef");

  var beforeCalls = calls.length;
  var typeFailures = [];
  try { service.capture({ facing: "back" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.capture(null); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.capture({ unknown: true }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.capture({ signal: {} }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  var getterCalls = 0;
  var accessor = {};
  Object.defineProperty(accessor, "facing", { enumerable: true, get: function () { getterCalls++; return "user"; } });
  try { service.capture(accessor); } catch (error) { typeFailures.push(error instanceof TypeError); }
  controller = new AbortController();
  controller.abort();
  failure = await rejected(service.capture({ signal: controller.signal }));
  assert(typeFailures.length === 5 && typeFailures.every(Boolean) && getterCalls === 0 &&
    failure.code === "CANCELLED" && calls.length === beforeCalls,
    "invalid or pre-aborted capture options reached the native host");

  var noHost = install(null, function () { throw new Error("support must not run without a host"); });
  assert(await noHost.services.camera.available() === false,
    "camera.available without a native host did not resolve false");
  failure = await rejected(noHost.services.camera.capture());
  assert(failure.name === "KitCameraError" && failure.code === "UNAVAILABLE",
    "camera.capture without a native host did not fail closed");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
