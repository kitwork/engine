package javascript

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestQRMediaServiceStaticContract(t *testing.T) {
	qr := readVanillaFile(t, "service", "qr", "1.0.0.js")
	if len(qr) == 0 || qr[0] != ';' || qr[len(qr)-1] != '\n' || bytes.Contains(qr, []byte{'\r'}) {
		t.Fatal("qr@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(qr, []byte(`kit.service("qr"`)); got != 1 {
		t.Fatalf("qr@1.0.0 registration count = %d, want one", got)
	}
	for _, contract := range [][]byte{
		[]byte(`available: available`),
		[]byte(`scan: scan`),
		[]byte(`supportsCapability.call(capabilities, "qr.scan")`),
		[]byte(`nativeCall("beginScan", {}, beginResult)`),
		[]byte(`nativeCall("pollScan", { operation: state.operation }, pollResult)`),
		[]byte(`nativeCall("releaseScan", { operation: state.operation }, releaseResult)`),
		[]byte(`var MAX_VALUE_BYTES = 4096;`),
		[]byte(`var POLL_DELAY_MS = 250;`),
		[]byte(`var SCAN_TIMEOUT_MS = 10 * 60 * 1000;`),
		[]byte(`var SCAN_MAX_POLLS = 2400;`),
		[]byte(`name: { value: "KitQRError" }`),
		[]byte(`TOO_LARGE: "QR code value is too large"`),
		[]byte(`exactObject(value, ["status", "value"])`),
	} {
		if !bytes.Contains(qr, contract) {
			t.Errorf("qr@1.0.0 lost contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`globalThis.kit`), []byte(`global.kit`), []byte(`window.kit`),
		[]byte(`kit.component(`), []byte(`beginScan: `), []byte(`pollScan: `),
		[]byte(`releaseScan: `),
	} {
		if bytes.Contains(qr, forbidden) {
			t.Errorf("qr@1.0.0 exposes forbidden coupling %q", forbidden)
		}
	}

	media := readVanillaFile(t, "service", "media", "1.0.0.js")
	if len(media) == 0 || media[0] != ';' || media[len(media)-1] != '\n' || bytes.Contains(media, []byte{'\r'}) {
		t.Fatal("media@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(media, []byte(`kit.service("media"`)); got != 1 {
		t.Fatalf("media@1.0.0 registration count = %d, want one", got)
	}
	for _, contract := range [][]byte{
		[]byte(`pickImage: pickImage`),
		[]byte(`graph.services.files !== "1.3.0"`),
		[]byte(`var request = { accept: "image/*" };`),
		[]byte(`pickFile.call(files, objectFreeze(request))`),
		[]byte(`return discard(reference).then(function ()`),
		[]byte(`UNSUPPORTED: "Selected file is not an image"`),
		[]byte(`name: { value: "KitMediaError" }`),
		[]byte(`operation: { value: operation, enumerable: true }`),
	} {
		if !bytes.Contains(media, contract) {
			t.Errorf("media@1.0.0 lost contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`globalThis.kit`), []byte(`global.kit`), []byte(`window.kit`),
		[]byte(`kit.component(`), []byte(`nativeHost`), []byte(`camera.`),
	} {
		if bytes.Contains(media, forbidden) {
			t.Errorf("media@1.0.0 contains forbidden native coupling %q", forbidden)
		}
	}

	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.4.0.js"),
		readVanillaFile(t, "component", "app", "1.5.0.js")) {
		t.Fatal("app@1.5.0 changed component behavior instead of only closing a newer service graph")
	}
}

func TestQRMediaCatalogClosesExactApp150Graph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	app150, err := composer.catalog.component("app", "1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	requires := make(map[string]string, len(app150.requires))
	for _, service := range app150.requires {
		requires[service.Name] = service.Version
	}
	for name, version := range map[string]string{
		"camera": "1.0.0", "capabilities": "1.0.0", "files": "1.3.0",
		"media": "1.0.0", "qr": "1.0.0",
	} {
		if requires[name] != version {
			t.Fatalf("app@1.5.0 requirement %s = %q, want %q; graph=%#v", name, requires[name], version, requires)
		}
		if appGrantsAuthoredService("1.5.0", name) || validAuthoredServiceAction(name, "scan") ||
			validAuthoredServiceAction(name, "pickImage") {
			t.Fatalf("sealed app@1.5.0 service %s escaped into authored actions", name)
		}
	}

	media, err := composer.catalog.service(ServiceVersion{Name: "media", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(media.actions) != 0 || len(media.requires) != 1 ||
		media.requires[0] != (ServiceVersion{Name: "files", Version: "1.3.0"}) {
		t.Fatalf("media catalog definition = %#v", media)
	}
	qr, err := composer.catalog.service(ServiceVersion{Name: "qr", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(qr.actions) != 0 || len(qr.requires) != 1 ||
		qr.requires[0] != (ServiceVersion{Name: "capabilities", Version: "1.0.0"}) {
		t.Fatalf("qr catalog definition = %#v", qr)
	}
	for _, identity := range []ServiceVersion{{Name: "media", Version: "1.0.1"}, {Name: "qr", Version: "1.0.1"}} {
		if _, err := composer.catalog.service(identity); !errors.Is(err, ErrModuleNotFound) {
			t.Fatalf("unknown %s version error = %v", identity.Name, err)
		}
	}

	bundle, err := composer.ComposeHTML([]byte(`<main data-kit-component="app@1.5.0" data-kit-as="$app"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`components["app"] = "1.5.0"`),
		[]byte(`services["camera"] = "1.0.0"`),
		[]byte(`services["files"] = "1.3.0"`),
		[]byte(`services["media"] = "1.0.0"`),
		[]byte(`services["qr"] = "1.0.0"`),
		[]byte(`grants["app"]["media"] = "1.0.0"`),
		[]byte(`grants["app"]["qr"] = "1.0.0"`),
	} {
		if !bytes.Contains(bundle.JavaScript, marker) {
			t.Fatalf("app@1.5.0 artifact omitted %q", marker)
		}
	}
	for _, service := range []string{"camera", "files", "media", "qr"} {
		if bytes.Contains(bundle.JavaScript, []byte(`actions["`+service+`"]["`)) {
			t.Fatalf("app@1.5.0 exposed %s through authored actions", service)
		}
	}
	filesIndex := bytes.Index(bundle.JavaScript, []byte(`KitJS staged service: files@1.3.0`))
	mediaIndex := bytes.Index(bundle.JavaScript, []byte(`KitJS staged service: media@1.0.0`))
	capabilitiesIndex := bytes.Index(bundle.JavaScript, []byte(`KitJS staged service: capabilities@1.0.0`))
	qrIndex := bytes.Index(bundle.JavaScript, []byte(`KitJS staged service: qr@1.0.0`))
	if filesIndex < 0 || mediaIndex < 0 || filesIndex >= mediaIndex ||
		capabilitiesIndex < 0 || qrIndex < 0 || capabilitiesIndex >= qrIndex {
		t.Fatalf("dependency order files=%d media=%d capabilities=%d qr=%d", filesIndex, mediaIndex, capabilitiesIndex, qrIndex)
	}

	legacy, err := composer.ComposeHTML([]byte(`<main data-kit-component="app@1.4.0" data-kit-as="$app"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy.JavaScript, []byte(`services["media"]`)) ||
		bytes.Contains(legacy.JavaScript, []byte(`services["qr"]`)) {
		t.Fatal("app@1.4.0 changed when app@1.5.0 introduced QR and media")
	}
	if strings.Contains(string(bundle.JavaScript), `$app.qr`) || strings.Contains(string(bundle.JavaScript), `$app.media`) {
		t.Fatal("QR or media invented an authored action alias")
	}
}

func TestQRServiceNativeNodeContract(t *testing.T) {
	qr := readVanillaFile(t, "service", "qr", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
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
  Object.defineProperty(document, ASSEMBLY, { value: {
    graph: { services: { capabilities: "1.0.0", qr: "1.0.0" } }, nativeHost: host
  }, configurable: true });
  globalThis.document = document;
  var namespaces = Object.create(null);
  var capabilities = Object.freeze({ supports: function (id) { return support(id); } });
  var kit = {
    capabilities: capabilities,
    service: function (name, namespace) {
      assert(!Object.prototype.hasOwnProperty.call(namespaces, name), "duplicate service " + name);
      namespaces[name] = Object.freeze(namespace);
      Object.defineProperty(kit, name, { value: namespaces[name], enumerable: true });
    }
  };
` + string(qr) + `
  return { kit: kit, services: namespaces };
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
  var operation = "Q".repeat(32);
  globalThis.reportError = function () { reported++; };
  function support(id) {
    assert(id === "qr.scan", "available queried a noncanonical capability");
    if (supportMode === "error") return Promise.reject({ code: "DENIED", message: "raw support secret" });
    return Promise.resolve(supportMode === "yes");
  }
  var host = {
    call: function (action, params) {
      calls.push({ action: action, params: params });
      if (action === "qr.beginScan") {
        pollCount = 0;
        assert(Object.keys(params).length === 0, "beginScan params were not exact");
        if (mode === "denied") return Promise.reject({ code: "DENIED", message: "raw scanner secret" });
        if (mode === "conflict") return Promise.reject({ code: "CONFLICT", message: "raw active operation" });
        if (mode === "badOperation") return { operation: "short" };
        if (mode === "extraBegin") return { operation: operation, secret: true };
        if (mode === "delayedBegin") return new Promise(function (resolve) { delayedBeginResolve = resolve; });
        return { operation: operation };
      }
      if (action === "qr.pollScan") {
        assert(params.operation === operation && Object.keys(params).join(",") === "operation",
          "poll operation changed");
        pollCount++;
        if (mode === "hold") return new Promise(function () {});
        if (mode === "foreverPending") return { status: "pending" };
        if (mode === "cancelled") return { status: "cancelled" };
        if (mode === "failed") return { status: "failed", code: failedCode };
        if (mode === "invalidFailed") return { status: "failed", code: "CANCELLED" };
        if (mode === "extraReady") return { status: "ready", value: "ok", secret: true };
        if (mode === "empty") return { status: "ready", value: "" };
        if (mode === "malformedText") return { status: "ready", value: "\uD800" };
        if (mode === "tooLarge") return { status: "ready", value: "x".repeat(4097) };
        if (mode === "maxASCII") return { status: "ready", value: "x".repeat(4096) };
        if (mode === "maxUnicode") return { status: "ready", value: "😀".repeat(1024) };
        if (pollCount === 1) return { status: "pending" };
        return { status: "ready", value: "https://kitwork.test/qr" };
      }
      if (action === "qr.releaseScan") {
        assert(params.operation === operation && Object.keys(params).join(",") === "operation",
          "release operation changed");
        released.push(params.operation);
        if (mode === "releaseReject") return Promise.reject({ code: "FAILED", message: "raw release secret" });
        if (mode === "releaseMalformed") return { released: true };
        return true;
      }
      throw new Error("unexpected native action " + action);
    }
  };

  var installed = install(host, support);
  var service = installed.services.qr;
  assert(Object.keys(service).sort().join(",") === "available,scan" &&
    service.beginScan === undefined && service.pollScan === undefined && service.releaseScan === undefined,
    "QR public surface widened");
  assert(await service.available() === true, "available did not negotiate qr.scan");
  supportMode = "no";
  assert(await service.available() === false, "available changed a false capability result");
  supportMode = "error";
  var failure = await rejected(service.available());
  assert(failure.name === "KitQRError" && failure.code === "DENIED" &&
    failure.operation === "available" && failure.message.indexOf("raw support secret") < 0 && Object.isFrozen(failure),
    "available failure escaped stable normalization");
  supportMode = "yes";

  var value = await service.scan();
  assert(value === "https://kitwork.test/qr" && released.length === 1,
    "successful QR scan changed its value or leaked its operation");

  mode = "maxASCII";
  assert((await service.scan()).length === 4096, "4096-byte ASCII QR value was rejected");
  mode = "maxUnicode";
  assert((await service.scan()).length === 2048, "4096-byte Unicode QR value was rejected");

  mode = "cancelled";
  var beforeRelease = released.length;
  assert(await service.scan() === null && released.length === beforeRelease + 1,
    "native QR cancellation did not resolve null and release its operation");

  for (var failureIndex = 0; failureIndex < 4; failureIndex++) {
    failedCode = ["DENIED", "UNAVAILABLE", "TOO_LARGE", "FAILED"][failureIndex];
    mode = "failed";
    beforeRelease = released.length;
    failure = await rejected(service.scan());
    assert(failure.name === "KitQRError" && failure.code === failedCode &&
      failure.operation === "scan" && Object.isFrozen(failure) && released.length === beforeRelease + 1,
      "failed poll code " + failedCode + " was not normalized and released");
  }

  for (var invalidIndex = 0; invalidIndex < 4; invalidIndex++) {
    mode = ["invalidFailed", "extraReady", "empty", "malformedText"][invalidIndex];
    beforeRelease = released.length;
    failure = await rejected(service.scan());
    assert(failure.code === "FAILED" && released.length === beforeRelease + 1,
      "malformed poll envelope " + mode + " escaped validation or cleanup");
  }

  mode = "tooLarge";
  beforeRelease = released.length;
  failure = await rejected(service.scan());
  assert(failure.code === "TOO_LARGE" && failure.message === "QR code value is too large" &&
    released.length === beforeRelease + 1, "oversized ready value was not bounded and released");

  mode = "denied";
  beforeRelease = released.length;
  failure = await rejected(service.scan());
  assert(failure.code === "DENIED" && failure.message.indexOf("raw scanner secret") < 0 &&
    released.length === beforeRelease, "begin denial escaped normalization or released an unknown operation");
  mode = "conflict";
  failure = await rejected(service.scan());
  assert(failure.code === "OVERLOADED" && released.length === beforeRelease,
    "native begin conflict was not normalized to OVERLOADED");
  for (var beginIndex = 0; beginIndex < 2; beginIndex++) {
    mode = beginIndex === 0 ? "badOperation" : "extraBegin";
    failure = await rejected(service.scan());
    assert(failure.code === "FAILED" && released.length === beforeRelease,
      "malformed begin envelope escaped validation or cleanup authority");
  }

  mode = "hold";
  var controller = new AbortController();
  var pending = service.scan({ signal: controller.signal });
  await turns(8);
  failure = await rejected(service.scan());
  assert(failure.code === "OVERLOADED", "QR allowed two active scan operations");
  beforeRelease = released.length;
  controller.abort();
  failure = await rejected(pending);
  assert(failure.code === "CANCELLED", "in-flight abort did not reject with CANCELLED");
  await turns(8);
  assert(released.length === beforeRelease + 1, "in-flight abort did not release its operation");

  mode = "delayedBegin";
  controller = new AbortController();
  pending = service.scan({ signal: controller.signal });
  await turns(4);
  controller.abort();
  failure = await rejected(pending);
  assert(failure.code === "CANCELLED" && typeof delayedBeginResolve === "function",
    "delayed begin abort did not reject promptly");
  failure = await rejected(service.scan());
  assert(failure.code === "OVERLOADED", "delayed begin abort released the one-operation slot too early");
  beforeRelease = released.length;
  delayedBeginResolve({ operation: operation });
  await turns(12);
  assert(released.length === beforeRelease + 1, "late operation publication leaked after abort");

  mode = "hold";
  pending = service.scan();
  await turns(8);
  beforeRelease = released.length;
  fireDeadlines();
  failure = await rejected(pending);
  assert(failure.code === "TIMEOUT", "ten-minute deadline did not reject with TIMEOUT");
  await turns(8);
  assert(released.length === beforeRelease + 1, "ten-minute deadline did not release its operation");

  mode = "foreverPending";
  beforeRelease = released.length;
  failure = await rejected(service.scan());
  await turns(8);
  assert(failure.code === "TIMEOUT" && pollCount === 2400 && released.length === beforeRelease + 1 &&
    deadlines.size === 0, "bounded polling exceeded 2400 polls or leaked its deadline/operation");

  mode = "releaseReject";
  beforeRelease = released.length;
  var beforeReported = reported;
  value = await service.scan();
  assert(value === "https://kitwork.test/qr" && released.length === beforeRelease + 1 &&
    reported === beforeReported + 1, "best-effort release changed a successful QR result");
  mode = "releaseMalformed";
  beforeReported = reported;
  value = await service.scan();
  assert(value === "https://kitwork.test/qr" && reported === beforeReported + 1,
    "malformed release acknowledgement changed a successful QR result");

  var beforeCalls = calls.length;
  controller = new AbortController();
  controller.abort();
  failure = await rejected(service.scan({ signal: controller.signal }));
  assert(failure.code === "CANCELLED" && calls.length === beforeCalls,
    "pre-aborted scan reached the native host");
  var getterCalls = 0;
  var options = {};
  Object.defineProperty(options, "signal", { enumerable: true, get: function () { getterCalls++; } });
  var threw = false;
  try { service.scan(options); } catch (error) { threw = error instanceof TypeError; }
  assert(threw && getterCalls === 0, "QR options accessor executed during rejection");
  threw = false;
  try { service.scan({ unknown: true }); } catch (error) { threw = error instanceof TypeError; }
  assert(threw, "unknown QR option was accepted");

  var noHost = install(null, function () { return true; });
  assert(await noHost.services.qr.available() === false, "available without a native host did not resolve false");
  failure = await rejected(noHost.services.qr.scan());
  assert(failure.code === "UNAVAILABLE", "scan without a native host did not fail closed");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}

func TestMediaServiceNodeContract(t *testing.T) {
	media := readVanillaFile(t, "service", "media", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
async function turns(count) { while (count-- > 0) await Promise.resolve(); }

var document = {};
Object.defineProperty(document, ASSEMBLY, { value: {
  graph: { services: { files: "1.3.0", media: "1.0.0" } }
}, configurable: true });
globalThis.document = document;
var image = Object.freeze({ name: "photo.jpg", type: "image/jpeg", size: 4, sha256: "a".repeat(64) });
var text = Object.freeze({ name: "note.txt", type: "text/plain", size: 4, sha256: "b".repeat(64) });
var mode = "image";
var calls = [];
var releases = [];
var reported = 0;
var releaseResolve = null;
globalThis.reportError = function (error) {
  reported++;
  assert(error && error.name === "KitMediaError" && error.code === "FAILED" &&
    JSON.stringify(error).indexOf("photo.jpg") < 0 && JSON.stringify(error).indexOf("note.txt") < 0,
    "cleanup diagnostic exposed selected FileRef data");
};
var files = Object.freeze({
  version: "1.3.0",
  pick: function (options) {
    calls.push(options);
    if (mode === "throwType") throw new TypeError("bad signal");
    if (mode === "cancel") return Promise.resolve(null);
    if (mode === "denied") return Promise.reject({ code: "DENIED", message: "raw file secret" });
    if (mode === "text" || mode === "textReleaseReject" || mode === "textReleaseDelay") return Promise.resolve(text);
    if (mode === "malformed") return Promise.resolve("not a FileRef");
    return Promise.resolve(image);
  },
  release: function (reference) {
    releases.push(reference);
    if (mode === "textReleaseReject") return Promise.reject({ code: "FAILED", message: "raw release secret" });
    if (mode === "textReleaseDelay") return new Promise(function (resolve) { releaseResolve = resolve; });
    if (mode === "malformed") throw new TypeError("not a FileRef");
    return Promise.resolve(true);
  }
});
var service = null;
var kit = {
  files: files,
  service: function (name, namespace) {
    assert(name === "media" && service === null, "media registration changed");
    service = Object.freeze(namespace);
  }
};
` + string(media) + `

(async function () {
  assert(Object.keys(service).join(",") === "pickImage" && service.pick === undefined &&
    service.release === undefined, "media public surface widened");
  var reference = await service.pickImage();
  assert(reference === image && calls.length === 1 && Object.isFrozen(calls[0]) &&
    Object.keys(calls[0]).join(",") === "accept" && calls[0].accept === "image/*" && releases.length === 0,
    "image selection did not reuse exact files.pick or changed its FileRef");

  var controller = new AbortController();
  reference = await service.pickImage({ signal: controller.signal });
  assert(reference === image && calls[1].signal === controller.signal &&
    Object.keys(calls[1]).join(",") === "accept,signal", "media did not forward the exact optional signal");

  mode = "cancel";
  assert(await service.pickImage() === null && releases.length === 0,
    "file picker cancellation did not stay null or attempted release");

  mode = "denied";
  var failure = await rejected(service.pickImage());
  assert(failure.name === "KitMediaError" && failure.code === "DENIED" &&
    failure.operation === "pickImage" && failure.message.indexOf("raw file secret") < 0 && Object.isFrozen(failure),
    "files failure escaped media normalization");

  mode = "text";
  var beforeRelease = releases.length;
  failure = await rejected(service.pickImage());
  assert(failure.name === "KitMediaError" && failure.code === "UNSUPPORTED" &&
    failure.operation === "pickImage" && failure.message === "Selected file is not an image" &&
    releases.length === beforeRelease + 1 && releases[releases.length - 1] === text,
    "non-image FileRef was not released before stable rejection");

  mode = "textReleaseDelay";
  var pending = service.pickImage();
  var settled = false;
  pending.then(function () { settled = true; }, function () { settled = true; });
  await turns(8);
  assert(!settled && typeof releaseResolve === "function",
    "non-image selection rejected before its FileRef release settled");
  releaseResolve(true);
  failure = await rejected(pending);
  assert(failure.code === "UNSUPPORTED", "delayed non-image release changed public rejection");

  mode = "textReleaseReject";
  var beforeReported = reported;
  failure = await rejected(service.pickImage());
  assert(failure.code === "UNSUPPORTED" && reported === beforeReported + 1,
    "release failure leaked or changed the stable non-image rejection");

  mode = "malformed";
  beforeReported = reported;
  failure = await rejected(service.pickImage());
  assert(failure.code === "FAILED" && reported === beforeReported + 1,
    "malformed FileRef escaped cleanup and stable failure normalization");

  mode = "throwType";
  var threw = false;
  try { service.pickImage({ signal: "bad" }); } catch (error) { threw = error instanceof TypeError; }
  assert(threw, "synchronous files option TypeError was converted to an asynchronous media failure");
  var getterCalls = 0;
  var options = {};
  Object.defineProperty(options, "signal", { enumerable: true, get: function () { getterCalls++; } });
  threw = false;
  try { service.pickImage(options); } catch (error) { threw = error instanceof TypeError; }
  assert(threw && getterCalls === 0, "media options accessor executed during rejection");
  threw = false;
  try { service.pickImage({ accept: "text/plain" }); } catch (error) { threw = error instanceof TypeError; }
  assert(threw, "media accepted a caller-controlled file filter");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
