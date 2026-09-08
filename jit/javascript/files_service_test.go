package javascript

import (
	"bytes"
	"testing"
)

func TestFilesServiceStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "files", "1.1.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("files@1.1.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("files"`)); got != 1 {
		t.Fatalf("files@1.1.0 registration count = %d, want one", got)
	}
	for _, member := range []string{"pick", "importBlob", "stat", "toBlob", "share", "release"} {
		if !bytes.Contains(source, []byte(member+": "+member)) {
			t.Errorf("files@1.1.0 omitted sealed member %s", member)
		}
	}
	for _, contract := range [][]byte{
		[]byte(`var MAX_IMPORT_BYTES = 256 * 1024 * 1024;`),
		[]byte(`var MAX_MATERIALIZE_BYTES = 32 * 1024 * 1024;`),
		[]byte(`var MAX_CHUNK_BYTES = 256 * 1024;`),
		[]byte(`var PICK_TIMEOUT_MS = 10 * 60 * 1000;`),
		[]byte(`var MIME_TYPE = /^[a-z0-9]`),
		[]byte(`bytes[0] = 0x4b;`),
		[]byte(`dataViewSetUint32(new DataViewType(frame), 28, sequence, false);`),
		[]byte(`nativeCall("ack", { handle: transport.handle, sequence: sent }`),
		[]byte(`descriptors.sequence.value !== sentSequence + 1`),
		[]byte(`xhrSend(xhr, blobSlice(blob, 0, size, "application/octet-stream"))`),
		[]byte(`if (declaredType === "") declaredType = "application/octet-stream";`),
	} {
		if !bytes.Contains(source, contract) {
			t.Errorf("files@1.1.0 lost transfer contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`globalThis.kit`), []byte(`window.kit`), []byte(`kit.component(`),
		[]byte(`data-kit-action`), []byte(`dispatch:`), []byte(`nativeHost:`), []byte(`nativeFiles:`),
	} {
		if bytes.Contains(source, forbidden) {
			t.Errorf("files@1.1.0 exposes forbidden coupling %q", forbidden)
		}
	}
}

func TestFilesServiceLegacyStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "files", "1.0.0.js")
	for _, member := range []string{"importBlob", "stat", "toBlob", "share", "release"} {
		if !bytes.Contains(source, []byte(member+": "+member)) {
			t.Errorf("files@1.0.0 omitted legacy member %s", member)
		}
	}
	if bytes.Contains(source, []byte("pick: pick")) {
		t.Fatal("files@1.0.0 changed after files@1.1.0 introduced pick")
	}
}

func TestFilesServiceNativeNodeContract(t *testing.T) {
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	files := readVanillaFile(t, "service", "files", "1.1.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var HOST = Symbol.for("kitwork:native:host:v1");
var FILES = Symbol.for("kitwork:native:files:v1");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
var xhrConstructed = 0;
var xhrMode = "success";
var currentUploadHandle = "";
var uploaded = new Map();
function FakeXHR() {
  xhrConstructed++;
  this.upload = {};
  this.status = 0;
  this.responseURL = "";
}
FakeXHR.prototype.open = function (method, url, async) {
  assert(method === "POST" && async === true, "HTTP import did not use asynchronous POST");
  this.url = url;
};
FakeXHR.prototype.send = function (body) {
  var self = this;
  assert(body instanceof Blob && body.type === "application/octet-stream",
    "HTTP import body was not an octet-stream Blob slice");
  if (xhrMode === "hang") return;
  body.arrayBuffer().then(function (buffer) {
    uploaded.set(currentUploadHandle, new Uint8Array(buffer));
    if (self.upload.onprogress) self.upload.onprogress({ loaded: body.size, total: body.size });
    self.status = 204;
    self.responseURL = self.url;
    if (self.onload) self.onload();
  });
};
FakeXHR.prototype.abort = function () { if (this.onabort) this.onabort(); };
globalThis.XMLHttpRequest = FakeXHR;
globalThis.location = { origin: "https://app.test", href: "https://app.test/home" };

var pickerMode = "select";
var pickerInputs = 0;
var pickerRemoved = 0;
var lastPicker = null;
class FakeFile extends Blob {
  constructor(parts, name, options) {
    super(parts, options);
    this._name = name;
  }
  get name() { return this._name; }
}
class FakeInput extends EventTarget {
  constructor() {
    super();
    this.files = [];
    this.parentNode = null;
    this.type = "";
    this.accept = "";
    this.multiple = true;
    this.hidden = false;
  }
  click() {
    var input = this;
    if (pickerMode === "hold") return;
    queueMicrotask(function () {
      if (pickerMode === "cancel") {
        input.dispatchEvent(new Event("cancel"));
        return;
      }
      input.files = [new FakeFile(["picked database"], "sample.db", { type: "application/x-sqlite3" })];
      input.dispatchEvent(new Event("change"));
    });
  }
  remove() {
    if (this.parentNode) pickerRemoved++;
    this.parentNode = null;
  }
}
globalThis.File = FakeFile;
var document = {
  createElement: function (name) {
    assert(name === "input", "files.pick created a non-input element");
    pickerInputs++;
    lastPicker = new FakeInput();
    return lastPicker;
  },
  documentElement: {
    appendChild: function (input) { input.parentNode = this; return input; }
  }
};
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
var mode = "http";
var counter = 0;
var calls = [];
var records = new Map();
var binaryParts = [];
var binaryReceived = 0;
var corruptRead = false;
var rejectRelease = false;
var delayBegin = false;
var delayedBeginResolve = null;
var abortOnCommit = null;
var lastFrame = null;
var host = {
  version: "1.0.0",
  call: function (action, params) {
    calls.push({ action: action, params: params, receiver: this === host });
    if (action === "files.beginImport") {
      counter++;
      var handle = String.fromCharCode(65 + counter).repeat(32);
      currentUploadHandle = handle;
      records.set(handle, { name: params.name, type: params.type, size: params.size });
      binaryParts = [];
      binaryReceived = 0;
      var selected = mode === "http" ?
        { handle: handle, mode: "http", uploadURL: "/_kitwork/files/" + handle } :
        { handle: handle, mode: "binary", token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", chunkBytes: 262144 };
      if (delayBegin) return new Promise(function (resolve) {
        delayedBeginResolve = function () { delayedBeginResolve = null; resolve(selected); };
      });
      return selected;
    }
    if (action === "files.ack") {
      assert(lastFrame && params.sequence === lastFrame.sequence, "ACK did not name the sent sequence");
      binaryReceived += lastFrame.payload.byteLength;
      binaryParts.push(lastFrame.payload);
      return { sequence: params.sequence + 1, received: binaryReceived };
    }
    if (action === "files.commitImport") {
      var record = records.get(params.handle);
      if (mode === "binary") {
        var combined = new Uint8Array(binaryReceived);
        var at = 0;
        binaryParts.forEach(function (part) { combined.set(part, at); at += part.byteLength; });
        uploaded.set(params.handle, combined);
      }
      var committed = { handle: params.handle, name: record.name, type: record.type, size: record.size,
        sha256: "a".repeat(64) };
      if (abortOnCommit) {
        var controller = abortOnCommit;
        abortOnCommit = null;
        controller.abort();
      }
      return committed;
    }
    if (action === "files.abortImport") return true;
    if (action === "files.stat") {
      var stat = records.get(params.handle);
      return { handle: params.handle, name: stat.name, type: stat.type, size: stat.size,
        sha256: "a".repeat(64) };
    }
    if (action === "files.readChunk") {
      var bytes = uploaded.get(params.handle);
      var end = Math.min(params.offset + params.length, bytes.byteLength);
      var data = Buffer.from(bytes.slice(params.offset, end)).toString("base64");
      if (corruptRead) data = "A===";
      return { data: data, offset: params.offset, size: bytes.byteLength, done: end === bytes.byteLength };
    }
    if (action === "files.share") return true;
    if (action === "files.release") {
      if (rejectRelease) {
        rejectRelease = false;
        return Promise.reject({ code: "DENIED", message: "raw vault secret" });
      }
      return true;
    }
    throw new Error("unexpected native action " + action);
  }
};
var fileHost = {
  version: "1.0.0",
  send: function (frame) {
    var bytes = new Uint8Array(frame);
    assert(this === fileHost && bytes[0] === 0x4b && bytes[1] === 0x57 && bytes[2] === 0x46 && bytes[3] === 0x31,
      "binary import lost its KWF1 frame or receiver");
    for (var index = 4; index < 28; index++) assert(bytes[index] === 0, "binary token decoded incorrectly");
    var sequence = new DataView(frame).getUint32(28, false);
    lastFrame = { sequence: sequence, payload: bytes.slice(32) };
    return true;
  }
};
Object.defineProperty(document, HOST, { value: host, configurable: true });
Object.defineProperty(document, FILES, { value: fileHost, configurable: true });
globalThis.document = document;
` + string(nativeSource) + `
var services = Object.create(null);
var kit = { service: function (name, namespace) { services[name] = Object.freeze(namespace); } };
` + string(files) + `
(async function () {
  var service = services.files;
  assert(calls.length === 0 && xhrConstructed === 0, "files package performed work during registration");
  assert(Object.keys(service).sort().join(",") === "importBlob,pick,release,share,stat,toBlob" &&
    service.bridge === undefined && service.dispatch === undefined && service.nativeHost === undefined,
    "files namespace leaked private transport controls");
  assert(!Object.prototype.hasOwnProperty.call(document, HOST) &&
    !Object.prototype.hasOwnProperty.call(document, FILES), "raw native file bootstrap slots survived capture");

  var before = calls.length;
  var threw = [];
  ["Text/Plain", "text/plain; charset=utf-8", "text/*", "text /plain"].forEach(function (type) {
    try { service.importBlob(new Blob(["bad"]), { type: type }); }
    catch (error) { threw.push(error instanceof TypeError); }
  });
  assert(threw.length === 4 && threw.every(Boolean) && calls.length === before,
    "non-canonical custom MIME type reached the native host");

  var progress = [];
  var httpRef = await service.importBlob(new Blob(["hello"]), {
    name: "hello.bin",
    onProgress: function (value) { assert(Object.isFrozen(value), "progress snapshot was mutable"); progress.push(value); }
  });
  assert(Object.isFrozen(httpRef) && Object.keys(httpRef).sort().join(",") === "name,sha256,size,type" &&
    httpRef.name === "hello.bin" && httpRef.type === "application/octet-stream" && httpRef.size === 5 &&
    httpRef.sha256 === "a".repeat(64) && httpRef.handle === undefined,
    "HTTP import did not return an opaque frozen FileRef");
  var begin = calls.find(function (entry) { return entry.action === "files.beginImport"; });
  assert(begin && begin.params.type === "application/octet-stream" && Object.isFrozen(begin.params),
    "empty Blob type was not canonicalized on the control plane");
  assert(progress[0].loaded === 0 && progress[progress.length - 1].loaded === 5,
    "HTTP progress was not monotonic and complete");
  assert(new TextDecoder().decode(uploaded.get("B".repeat(32))) === "hello",
    "HTTP Blob bytes changed in transit");
  var statRef = await service.stat(httpRef);
  assert(statRef !== httpRef && Object.isFrozen(statRef) && statRef.sha256 === httpRef.sha256,
    "stat did not return a frozen FileRef sharing immutable metadata");
  assert(await service.share(httpRef, { title: "Hello" }) === true,
    "file share did not reach its dedicated native action");
  var restored = await service.toBlob(httpRef);
  assert(restored instanceof Blob && restored.type === "application/octet-stream" &&
    await restored.text() === "hello", "bounded readChunk did not materialize the original Blob");

  corruptRead = true;
  var failure = await rejected(service.toBlob(httpRef));
  assert(failure.name === "KitFilesError" && failure.code === "FAILED",
    "non-canonical base64 file data escaped validation");
  corruptRead = false;

  rejectRelease = true;
  failure = await rejected(service.release(statRef));
  assert(failure.name === "KitFilesError" && failure.code === "DENIED" &&
    failure.message.indexOf("raw vault secret") < 0, "release failure escaped normalization");
  assert((await service.stat(httpRef)).size === 5,
    "failed release invalidated a retryable FileRef");
  assert(await service.release(httpRef) === true, "release retry did not succeed");
  assert(await service.release(httpRef) === true && await service.release(statRef) === true,
    "release was not idempotent across genuine FileRefs sharing one handle state");
  var typeFailure = false;
  try { service.stat(statRef); } catch (error) { typeFailure = error instanceof TypeError; }
  assert(typeFailure, "successful release did not invalidate every FileRef sharing the handle state");

  mode = "binary";
  var payload = new Uint8Array(300000);
  for (var index = 0; index < payload.length; index++) payload[index] = index & 255;
  progress = [];
  var binaryRef = await service.importBlob(new Blob([payload], { type: "application/x-test" }), {
    name: "large.bin",
    onProgress: function (value) { progress.push(value.loaded); }
  });
  assert(binaryRef.size === payload.length && binaryRef.type === "application/x-test" &&
    binaryParts.length === 2 && progress[0] === 0 && progress[progress.length - 1] === payload.length,
    "binary import did not use two bounded ACK-gated chunks");
  assert(calls.filter(function (entry) { return entry.action === "files.ack"; }).slice(-2)
    .map(function (entry) { return entry.params.sequence; }).join(",") === "0,1",
    "binary import sequence or one-frame ACK backpressure drifted");
  var binaryBlob = await service.toBlob(binaryRef);
  var roundTrip = new Uint8Array(await binaryBlob.arrayBuffer());
  assert(roundTrip.length === payload.length && roundTrip[0] === 0 && roundTrip[299999] === (299999 & 255),
    "binary import/read round trip changed bytes");

  before = calls.length;
  var controller = new AbortController();
  controller.abort();
  failure = await rejected(service.importBlob(new Blob(["cancel"]), { signal: controller.signal }));
  assert(failure.code === "CANCELLED" && calls.length === before,
    "pre-aborted import reached the native host");

  mode = "http";
  xhrMode = "hang";
  controller = new AbortController();
  var pending = service.importBlob(new Blob(["cancel in flight"]), {
    name: "cancel.bin", signal: controller.signal
  });
  for (var turn = 0; turn < 8; turn++) await Promise.resolve();
  controller.abort();
  failure = await rejected(pending);
  assert(failure.code === "CANCELLED" && calls.some(function (entry) {
    return entry.action === "files.abortImport" && entry.params.handle === "D".repeat(32);
  }) && calls.some(function (entry) {
    return entry.action === "files.release" && entry.params.handle === "D".repeat(32);
  }), "in-flight HTTP cancellation did not abort and release its pending native import");

  xhrMode = "success";
  delayBegin = true;
  controller = new AbortController();
  before = calls.length;
  pending = service.importBlob(new Blob(["delayed begin"]), {
    name: "delayed.bin", signal: controller.signal
  });
  for (turn = 0; turn < 4 && typeof delayedBeginResolve !== "function"; turn++) await Promise.resolve();
  assert(typeof delayedBeginResolve === "function", "delayed beginImport was not installed deterministically");
  controller.abort();
  failure = await rejected(pending);
  assert(failure.code === "CANCELLED", "delayed begin cancellation did not reject promptly");
  var delayedHandle = "E".repeat(32);
  assert(!calls.slice(before).some(function (entry) {
    return (entry.action === "files.abortImport" || entry.action === "files.release") &&
      entry.params.handle === delayedHandle;
  }), "delayed begin cleaned a handle before native published it");
  delayBegin = false;
  delayedBeginResolve();
  for (turn = 0; turn < 8; turn++) await Promise.resolve();
  assert(calls.filter(function (entry) {
    return entry.action === "files.abortImport" && entry.params.handle === delayedHandle;
  }).length === 1 && calls.filter(function (entry) {
    return entry.action === "files.release" && entry.params.handle === delayedHandle;
  }).length === 1 && !calls.some(function (entry) {
    return entry.action === "files.commitImport" && entry.params.handle === delayedHandle;
  }), "a handle published after begin cancellation leaked or continued importing");

  controller = new AbortController();
  abortOnCommit = controller;
  pending = service.importBlob(new Blob(["commit race"]), {
    name: "commit-race.bin", signal: controller.signal
  });
  failure = await rejected(pending);
  var committedHandle = "F".repeat(32);
  assert(failure.code === "CANCELLED" && calls.some(function (entry) {
    return entry.action === "files.commitImport" && entry.params.handle === committedHandle;
  }) && calls.filter(function (entry) {
    return entry.action === "files.abortImport" && entry.params.handle === committedHandle;
  }).length === 1 && calls.filter(function (entry) {
    return entry.action === "files.release" && entry.params.handle === committedHandle;
  }).length === 1, "cancellation at commit left a ready native file without a FileRef");

  mode = "http";
  pickerMode = "select";
  progress = [];
  var picked = await service.pick({
    accept: ".db,application/x-sqlite3",
    onProgress: function (value) { progress.push(value.loaded); }
  });
  assert(picked && Object.isFrozen(picked) && picked.name === "sample.db" &&
    picked.type === "application/x-sqlite3" && picked.size === 15 &&
    progress[0] === 0 && progress[progress.length - 1] === 15 && pickerRemoved === 1,
    "files.pick did not import one user-selected File into an opaque FileRef");
  assert(await (await service.toBlob(picked)).text() === "picked database",
    "files.pick changed the selected file bytes");
  await service.release(picked);

  pickerMode = "cancel";
  assert(await service.pick() === null && pickerRemoved === 2,
    "files.pick cancellation did not resolve null and remove its transient input");

  pickerMode = "hold";
  var firstPick = service.pick();
  failure = await rejected(service.pick());
  assert(failure.code === "OVERLOADED" && pickerInputs === 3,
    "files.pick allowed more than one visible picker");
  lastPicker.dispatchEvent(new Event("cancel"));
  assert(await firstPick === null && pickerRemoved === 3,
    "files.pick did not clean its held picker after cancellation");

  before = pickerInputs;
  controller = new AbortController();
  controller.abort();
  failure = await rejected(service.pick({ signal: controller.signal }));
  assert(failure.code === "CANCELLED" && pickerInputs === before,
    "pre-aborted files.pick opened a platform picker");
  threw = [];
  ["Text/Plain", "text/*,text/*", ".", ".db, text/plain", "application/json; charset=utf-8"].forEach(function (accept) {
    try { service.pick({ accept: accept }); }
    catch (error) { threw.push(error instanceof TypeError); }
  });
  assert(threw.length === 5 && threw.every(Boolean) && pickerInputs === before,
    "invalid files.pick accept filters reached the platform picker");

  var getterCalls = 0;
  var options = {};
  Object.defineProperty(options, "name", { enumerable: true, get: function () { getterCalls++; return "secret"; } });
  typeFailure = false;
  try { service.importBlob(new Blob(["x"]), options); } catch (error) { typeFailure = error instanceof TypeError; }
  assert(typeFailure && getterCalls === 0, "file options accessor executed during rejection");
  before = calls.length;
  typeFailure = false;
  try { service.release({ name: "fake", type: "text/plain", size: 1, sha256: "a".repeat(64) }); }
  catch (error) { typeFailure = error instanceof TypeError; }
  assert(typeFailure && calls.length === before, "forged FileRef reached the native host");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
