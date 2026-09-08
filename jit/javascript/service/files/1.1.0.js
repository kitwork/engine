;(function (global, kit) {
"use strict";

// KitJS staged service: files@1.1.0
// The JSON native host is the bounded control plane. Android file bytes use the
// independent, private KWF1 adapter captured by the staged runtime.
var OWN = Object.prototype.hasOwnProperty;
var MAX_NAME_LENGTH = 255;
var MAX_TYPE_LENGTH = 255;
var MAX_TITLE_LENGTH = 512;
var MAX_IMPORT_BYTES = 256 * 1024 * 1024;
var MAX_MATERIALIZE_BYTES = 32 * 1024 * 1024;
var MAX_CHUNK_BYTES = 256 * 1024;
var MAX_PROGRESS_EVENTS = 1024;
var MAX_ACCEPT_LENGTH = 1024;
var MAX_ACCEPT_PARTS = 32;
var PICK_TIMEOUT_MS = 10 * 60 * 1000;
var FILE_HANDLE = /^[A-Za-z0-9_-]{32}$/;
var SHA256 = /^[a-f0-9]{64}$/;
var MIME_TYPE = /^[a-z0-9][a-z0-9!#&^_.+\-]{0,126}\/[a-z0-9][a-z0-9!#&^_.+\-]{0,126}$/;
var ACCEPT_TYPE = /^(?:\.[a-z0-9][a-z0-9._+\-]{0,31}|[a-z0-9][a-z0-9!#&^_.+\-]{0,126}\/(?:\*|[a-z0-9][a-z0-9!#&^_.+\-]{0,126}))$/;
var BASE64URL = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
var BASE64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
var IMPORT_KEYS = Object.freeze({ name: true, type: true, signal: true, onProgress: true });
var PICK_KEYS = Object.freeze({ accept: true, signal: true, onProgress: true });
var TRANSFER_KEYS = Object.freeze({ signal: true, onProgress: true });
var SHARE_KEYS = Object.freeze({ title: true });
var objectCreate = Object.create;
var objectDefineProperties = Object.defineProperties;
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
var isSafeInteger = Number.isSafeInteger;
var mathCeil = Math.ceil;
var mathMin = Math.min;
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var nativeFiles = assembly && assembly.nativeFiles;
var references = new WeakMap();
var BlobType = global.Blob;
var FileType = global.File;
var XHRType = global.XMLHttpRequest;
var URLType = global.URL;
var AbortSignalType = global.AbortSignal;
var EventTargetType = global.EventTarget;
var ArrayBufferType = global.ArrayBuffer;
var Uint8ArrayType = global.Uint8Array;
var DataViewType = global.DataView;
var PromiseType = global.Promise;
var setTimer = typeof global.setTimeout === "function" && global.setTimeout.bind(global);
var clearTimer = typeof global.clearTimeout === "function" && global.clearTimeout.bind(global);
var call = Function.prototype.call;
var regExpTest = call.bind(RegExp.prototype.test);
var promiseReject = call.bind(PromiseType.reject, PromiseType);
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var stringCharCodeAt = call.bind(String.prototype.charCodeAt);
var stringIndexOf = call.bind(String.prototype.indexOf);
var stringSplit = call.bind(String.prototype.split);
var stringTrim = call.bind(String.prototype.trim);
var arrayPush = call.bind(Array.prototype.push);
var arrayJoin = call.bind(Array.prototype.join);
var arrayBufferByteLength = ArrayBufferType &&
  objectGetOwnPropertyDescriptor(ArrayBufferType.prototype, "byteLength").get;
var uint8ArraySet = Uint8ArrayType && call.bind(Uint8ArrayType.prototype.set);
var dataViewSetUint32 = DataViewType && call.bind(DataViewType.prototype.setUint32);
var xhrAbort = XHRType && call.bind(XHRType.prototype.abort);
var xhrOpen = XHRType && call.bind(XHRType.prototype.open);
var xhrSend = XHRType && call.bind(XHRType.prototype.send);
var blobSize = BlobType && objectGetOwnPropertyDescriptor(BlobType.prototype, "size").get;
var blobType = BlobType && objectGetOwnPropertyDescriptor(BlobType.prototype, "type").get;
var blobSlice = BlobType && call.bind(BlobType.prototype.slice);
var blobArrayBuffer = BlobType && call.bind(BlobType.prototype.arrayBuffer);
var fileName = FileType && objectGetOwnPropertyDescriptor(FileType.prototype, "name");
fileName = fileName && fileName.get;
var signalAborted = AbortSignalType &&
  objectGetOwnPropertyDescriptor(AbortSignalType.prototype, "aborted").get;
var addEvent = EventTargetType && call.bind(EventTargetType.prototype.addEventListener);
var removeEvent = EventTargetType && call.bind(EventTargetType.prototype.removeEventListener);
var activePicker = null;

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* User progress callbacks cannot break a transfer. */ }
}

function wellFormed(value) {
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit >= 0xD800 && unit <= 0xDBFF) {
      if (index + 1 >= value.length) return false;
      var next = stringCharCodeAt(value, index + 1);
      if (next < 0xDC00 || next > 0xDFFF) return false;
      index++;
    } else if (unit >= 0xDC00 && unit <= 0xDFFF) return false;
  }
  return true;
}

function utf8Length(value) {
  var bytes = 0;
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit < 0x80) bytes++;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xD800 && unit <= 0xDBFF) {
      bytes += 4;
      index++;
    } else bytes += 3;
    if (bytes > MAX_NAME_LENGTH) return bytes;
  }
  return bytes;
}

function safeName(value) {
  if (typeof value !== "string" || !value || !wellFormed(value) || utf8Length(value) > MAX_NAME_LENGTH ||
    value !== stringTrim(value) || value === "." || value === ".." ||
    stringIndexOf(value, "/") >= 0 || stringIndexOf(value, "\\") >= 0) {
    throw new TypeError("File name must be well-formed UTF-8 text up to 255 bytes without path separators");
  }
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit < 0x20 || unit >= 0x7F && unit <= 0x9F) {
      throw new TypeError("File name must not contain control characters");
    }
  }
  return value;
}

function safeType(value) {
  if (typeof value !== "string" || value.length > MAX_TYPE_LENGTH || !regExpTest(MIME_TYPE, value)) {
    throw new TypeError("File type must be a canonical lowercase type/subtype without parameters");
  }
  return value;
}

function safeTitle(value) {
  if (value === undefined) return undefined;
  if (typeof value !== "string" || value.length > MAX_TITLE_LENGTH ||
    stringIndexOf(value, "\0") >= 0 || !wellFormed(value)) {
    throw new TypeError("Share title must be well-formed, NUL-free text up to 512 characters");
  }
  return value;
}

function safeAccept(value) {
  if (value === undefined || value === "") return "";
  if (typeof value !== "string" || value.length > MAX_ACCEPT_LENGTH || !wellFormed(value) ||
    stringIndexOf(value, "\0") >= 0) {
    throw new TypeError("File accept must be bounded, well-formed text");
  }
  var parts = stringSplit(value, ",");
  if (!parts.length || parts.length > MAX_ACCEPT_PARTS) {
    throw new TypeError("File accept must contain at most 32 entries");
  }
  var seen = objectCreate(null);
  for (var index = 0; index < parts.length; index++) {
    var part = parts[index];
    if (part !== stringTrim(part) || !regExpTest(ACCEPT_TYPE, part) || OWN.call(seen, part)) {
      throw new TypeError("File accept entries must be unique lowercase MIME types, wildcards, or extensions");
    }
    seen[part] = true;
  }
  return arrayJoin(parts, ",");
}

function plainOptions(value, allowed, label) {
  if (value === undefined) return objectCreate(null);
  var prototype;
  var symbols;
  var descriptors;
  try {
    prototype = objectGetPrototypeOf(value);
    symbols = objectGetOwnPropertySymbols(value);
    descriptors = objectGetOwnPropertyDescriptors(value);
  } catch (_) {
    throw new TypeError(label + " options must be a plain object");
  }
  if (!value || prototype !== Object.prototype && prototype !== null || symbols.length) {
    throw new TypeError(label + " options must be a plain object");
  }
  var output = objectCreate(null);
  objectKeys(descriptors).forEach(function (name) {
    if (!OWN.call(allowed, name)) throw new TypeError("Unknown " + label + " option: " + name);
    var descriptor = descriptors[name];
    if (!OWN.call(descriptor, "value") || descriptor.get || descriptor.set) {
      throw new TypeError(label + " options must not contain accessors");
    }
    output[name] = descriptor.value;
  });
  return output;
}

function checkedSignal(value) {
  if (value === undefined) return undefined;
  if (!signalAborted || !addEvent || !removeEvent) throw new TypeError("signal must be an AbortSignal");
  try { signalAborted.call(value); }
  catch (_) { throw new TypeError("signal must be an AbortSignal"); }
  return value;
}

function isAborted(signal) {
  if (!signal) return false;
  try { return signalAborted.call(signal) === true; }
  catch (_) { return true; }
}

function listenForAbort(signal, listener) {
  if (!signal) return function () {};
  addEvent(signal, "abort", listener, { once: true });
  return function () {
    try { removeEvent(signal, "abort", listener); }
    catch (_) { /* A settled transfer no longer depends on listener cleanup. */ }
  };
}

function abortable(promise, signal, operation) {
  if (!signal) return promise;
  if (isAborted(signal)) return promiseReject(filesError("CANCELLED", operation));
  return new PromiseType(function (resolve, reject) {
    var settled = false;
    var removeAbort = function () {};
    removeAbort = listenForAbort(signal, function () {
      if (settled) return;
      settled = true;
      removeAbort();
      reject(filesError("CANCELLED", operation));
    });
    promiseResolve(promise).then(function (value) {
      if (settled) return;
      settled = true;
      removeAbort();
      resolve(value);
    }, function (error) {
      if (settled) return;
      settled = true;
      removeAbort();
      reject(error);
    });
  });
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host failures never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function filesError(code, operation) {
  var messages = {
    DENIED: "File permission was denied",
    CANCELLED: "File operation was cancelled",
    UNAVAILABLE: "Native files are unavailable",
    TIMEOUT: "File operation timed out",
    OVERLOADED: "Native files are busy",
    TOO_LARGE: "File is too large to materialize safely",
    FAILED: "File operation failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitFilesError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return objectFreeze(error);
}

function nativeCall(operation, params, validator) {
  if (!nativeHost) return promiseReject(filesError("UNAVAILABLE", operation));
  try {
    return promiseResolve(nativeHost.call("files." + operation, params)).then(
      function (value) {
        try { return validator(value); }
        catch (_) { throw filesError("FAILED", operation); }
      },
      function (error) { throw filesError(errorCode(error), operation); }
    );
  } catch (error) {
    return promiseReject(filesError(errorCode(error), operation));
  }
}

function exactObject(value, keys) {
  var prototype;
  var symbols;
  var descriptors;
  try {
    prototype = value && objectGetPrototypeOf(value);
    symbols = value && objectGetOwnPropertySymbols(value);
    descriptors = value && objectGetOwnPropertyDescriptors(value);
  } catch (_) { return null; }
  if (!value || prototype !== Object.prototype && prototype !== null || symbols.length ||
    objectKeys(descriptors).length !== keys.length) return null;
  for (var index = 0; index < keys.length; index++) {
    var descriptor = descriptors[keys[index]];
    if (!descriptor || !descriptor.enumerable || !OWN.call(descriptor, "value") || descriptor.get || descriptor.set) {
      return null;
    }
  }
  return descriptors;
}

function safeHandle(value) {
  if (typeof value !== "string" || !regExpTest(FILE_HANDLE, value)) throw new TypeError("invalid file handle");
  return value;
}

function metadata(value, expectedHandle) {
  var descriptors = exactObject(value, ["handle", "name", "type", "size", "sha256"]);
  if (!descriptors) throw new TypeError("invalid file metadata");
  var handle = safeHandle(descriptors.handle.value);
  if (expectedHandle !== undefined && handle !== expectedHandle) throw new TypeError("mismatched file handle");
  var name = safeName(descriptors.name.value);
  var type = safeType(descriptors.type.value);
  var size = descriptors.size.value;
  var sha256 = descriptors.sha256.value;
  if (!isSafeInteger(size) || size < 0 || size > MAX_IMPORT_BYTES ||
    typeof sha256 !== "string" || !regExpTest(SHA256, sha256)) {
    throw new TypeError("invalid file metadata");
  }
  return objectFreeze({ handle: handle, name: name, type: type, size: size, sha256: sha256 });
}

function makeRef(value, state) {
  if (!state) state = { handle: value.handle, released: false, releasing: null };
  var reference = objectFreeze({
    name: value.name,
    type: value.type,
    size: value.size,
    sha256: value.sha256
  });
  references.set(reference, state);
  return reference;
}

function stateFor(reference) {
  var state = reference && typeof reference === "object" ? references.get(reference) : null;
  if (!state || state.released) throw new TypeError("File reference is invalid or has been released");
  return state;
}

function sameMetadata(reference, value) {
  if (reference.name !== value.name || reference.type !== value.type ||
    reference.size !== value.size || reference.sha256 !== value.sha256) {
    throw new TypeError("file metadata changed");
  }
  return value;
}

function progress(onProgress, total) {
  if (onProgress !== undefined && typeof onProgress !== "function") {
    throw new TypeError("onProgress must be a function");
  }
  var loaded = -1;
  var emitted = 0;
  return function (next, final) {
    if (next < loaded || next < 0 || next > total || !isSafeInteger(next)) return;
    if (next === loaded) return;
    loaded = next;
    if (!onProgress || emitted >= MAX_PROGRESS_EVENTS && !final) return;
    emitted++;
    try { onProgress(objectFreeze({ loaded: next, total: total })); }
    catch (error) { report(error); }
  };
}

function blobSnapshot(value, source) {
  var size;
  var type;
  try {
    size = blobSize.call(value);
    type = blobType.call(value);
  } catch (_) {
    throw new TypeError("files.importBlob expects a Blob");
  }
  if (!isSafeInteger(size) || size < 0 || size > MAX_IMPORT_BYTES) {
    throw new TypeError("Blob size must not exceed 268435456 bytes");
  }
  var name = source.name;
  if (name === undefined && fileName) {
    try { name = fileName.call(value); }
    catch (_) { /* A plain Blob has no File name. */ }
  }
  if (name === undefined || name === "") name = "blob";
  var declaredType = source.type === undefined ? type : source.type;
  if (declaredType === "") declaredType = "application/octet-stream";
  return objectFreeze({
    blob: value,
    name: safeName(name),
    type: safeType(declaredType),
    size: size
  });
}

function beginResult(value) {
  var common = exactObject(value, ["handle", "mode", "uploadURL"]);
  if (common && common.mode.value === "http") {
    return objectFreeze({
      handle: safeHandle(common.handle.value),
      mode: "http",
      uploadURL: safeUploadURL(common.uploadURL.value)
    });
  }
  var binary = exactObject(value, ["handle", "mode", "token"]);
  var withChunk = false;
  if (!binary) {
    binary = exactObject(value, ["handle", "mode", "token", "chunkBytes"]);
    withChunk = Boolean(binary);
  }
  if (!binary || binary.mode.value !== "binary" || typeof binary.token.value !== "string" ||
    !regExpTest(FILE_HANDLE, binary.token.value)) throw new TypeError("invalid import transport");
  var chunkBytes = withChunk ? binary.chunkBytes.value : MAX_CHUNK_BYTES;
  if (!isSafeInteger(chunkBytes) || chunkBytes < 1 || chunkBytes > MAX_CHUNK_BYTES) {
    throw new TypeError("invalid file chunk size");
  }
  return objectFreeze({
    handle: safeHandle(binary.handle.value),
    mode: "binary",
    token: binary.token.value,
    chunkBytes: chunkBytes
  });
}

function safeUploadURL(value) {
  if (typeof value !== "string" || !value || value.length > 4096 || !URLType) {
    throw new TypeError("invalid upload URL");
  }
  var location = global.location;
  var url;
  try { url = new URLType(value, location && location.href); }
  catch (_) { throw new TypeError("invalid upload URL"); }
  if (!location || url.origin !== location.origin || url.username || url.password || url.hash ||
    url.protocol !== "http:" && url.protocol !== "https:") {
    throw new TypeError("upload URL must be same-origin HTTP(S)");
  }
  return url.href;
}

function tokenBytes(value) {
  if (typeof value !== "string" || !regExpTest(FILE_HANDLE, value)) throw new TypeError("invalid file token");
  var output = new Uint8ArrayType(24);
  var offset = 0;
  for (var index = 0; index < value.length; index += 4) {
    var a = stringIndexOf(BASE64URL, value[index]);
    var b = stringIndexOf(BASE64URL, value[index + 1]);
    var c = stringIndexOf(BASE64URL, value[index + 2]);
    var d = stringIndexOf(BASE64URL, value[index + 3]);
    if (a < 0 || b < 0 || c < 0 || d < 0) throw new TypeError("invalid file token");
    output[offset++] = a << 2 | b >> 4;
    output[offset++] = (b & 15) << 4 | c >> 2;
    output[offset++] = (c & 3) << 6 | d;
  }
  return output;
}

function uploadHTTP(blob, size, transport, signal, update) {
  if (typeof XHRType !== "function") return promiseReject(filesError("UNAVAILABLE", "importBlob"));
  return new PromiseType(function (resolve, reject) {
    var xhr;
    var settled = false;
    var removeAbort = function () {};
    function finish(ok, value) {
      if (settled) return;
      settled = true;
      removeAbort();
      if (xhr) {
        xhr.onload = xhr.onerror = xhr.onabort = xhr.ontimeout = null;
        if (xhr.upload) xhr.upload.onprogress = null;
      }
      if (ok) resolve(value);
      else reject(value);
    }
    function cancelled() {
      try { if (xhr) xhrAbort(xhr); }
      catch (_) { /* Cancellation remains authoritative. */ }
      finish(false, filesError("CANCELLED", "importBlob"));
    }
    if (isAborted(signal)) {
      cancelled();
      return;
    }
    try {
      xhr = new XHRType();
      xhrOpen(xhr, "POST", transport.uploadURL, true);
      xhr.timeout = 120000;
      if (xhr.upload) {
        xhr.upload.onprogress = function (event) {
          var loaded = event && isSafeInteger(event.loaded) ? event.loaded : -1;
          if (loaded >= 0) update(mathMin(loaded, size), false);
        };
      }
      xhr.onload = function () {
        var responseURL = "";
        try { responseURL = xhr.responseURL || transport.uploadURL; }
        catch (_) { finish(false, filesError("FAILED", "importBlob")); return; }
        try { safeUploadURL(responseURL); }
        catch (_) { finish(false, filesError("FAILED", "importBlob")); return; }
        if (xhr.status < 200 || xhr.status >= 300) {
          finish(false, filesError("FAILED", "importBlob"));
          return;
        }
        update(size, true);
        finish(true, true);
      };
      xhr.onerror = function () { finish(false, filesError("FAILED", "importBlob")); };
      xhr.onabort = function () { finish(false, filesError("CANCELLED", "importBlob")); };
      xhr.ontimeout = function () { finish(false, filesError("TIMEOUT", "importBlob")); };
      removeAbort = listenForAbort(signal, cancelled);
      // The native upload endpoint owns the byte stream, while name/type remain
      // control-plane metadata. Always use its exact octet-stream contract.
      xhrSend(xhr, blobSlice(blob, 0, size, "application/octet-stream"));
    } catch (error) {
      finish(false, filesError(errorCode(error), "importBlob"));
    }
  });
}

function ackResult(value, sentSequence, received) {
  var descriptors = exactObject(value, ["sequence", "received"]);
  if (!descriptors || descriptors.sequence.value !== sentSequence + 1 ||
    descriptors.received.value !== received || !isSafeInteger(descriptors.received.value)) {
    throw new TypeError("invalid file acknowledgement");
  }
  return value;
}

function uploadBinary(blob, size, transport, signal, update) {
  if (!nativeFiles || nativeFiles.version !== "1.0.0" || typeof nativeFiles.send !== "function" ||
    !ArrayBufferType || !Uint8ArrayType || !DataViewType || !blobArrayBuffer) {
    return promiseReject(filesError("UNAVAILABLE", "importBlob"));
  }
  var token;
  try { token = tokenBytes(transport.token); }
  catch (_) { return promiseReject(filesError("FAILED", "importBlob")); }
  var offset = 0;
  var sequence = 0;

  function sendNext() {
    if (isAborted(signal)) return promiseReject(filesError("CANCELLED", "importBlob"));
    if (offset === size) {
      update(offset, true);
      return promiseResolve(true);
    }
    var end = mathMin(offset + transport.chunkBytes, size);
    var piece;
    try { piece = blobSlice(blob, offset, end); }
    catch (_) { return promiseReject(filesError("FAILED", "importBlob")); }
    return abortable(promiseResolve(blobArrayBuffer(piece)), signal, "importBlob").then(function (payload) {
      if (isAborted(signal)) throw filesError("CANCELLED", "importBlob");
      var payloadLength;
      try { payloadLength = arrayBufferByteLength.call(payload); }
      catch (_) { throw filesError("FAILED", "importBlob"); }
      if (objectGetPrototypeOf(payload) !== ArrayBufferType.prototype ||
        payloadLength !== end - offset || payloadLength < 1 || payloadLength > MAX_CHUNK_BYTES) {
        throw filesError("FAILED", "importBlob");
      }
      var frame = new ArrayBufferType(32 + payloadLength);
      var bytes = new Uint8ArrayType(frame);
      bytes[0] = 0x4b;
      bytes[1] = 0x57;
      bytes[2] = 0x46;
      bytes[3] = 0x31;
      uint8ArraySet(bytes, token, 4);
      dataViewSetUint32(new DataViewType(frame), 28, sequence, false);
      uint8ArraySet(bytes, new Uint8ArrayType(payload), 32);
      try {
        if (nativeFiles.send(frame) !== true) throw filesError("FAILED", "importBlob");
      } catch (error) {
        throw filesError(errorCode(error), "importBlob");
      }
      var sent = sequence;
      var received = end;
      return abortable(nativeCall("ack", { handle: transport.handle, sequence: sent }, function (value) {
        return ackResult(value, sent, received);
      }), signal, "importBlob").then(function () {
        offset = received;
        sequence++;
        update(offset, offset === size);
        return sendNext();
      });
    });
  }
  return sendNext();
}

function bestEffort(operation, handle) {
  if (!nativeHost) return;
  try {
    promiseResolve(nativeHost.call("files." + operation, { handle: handle })).catch(report);
  } catch (error) { report(error); }
}

function cleanupImport(lifecycle) {
  if (!lifecycle.transport || lifecycle.delivered || lifecycle.cleaned) return;
  lifecycle.cleaned = true;
  // abortImport handles pending writers; release is the authoritative
  // idempotent cleanup for both pending and already-committed vault entries.
  bestEffort("abortImport", lifecycle.transport.handle);
  bestEffort("release", lifecycle.transport.handle);
}

function importBlob(blob, options) {
  if (arguments.length < 1 || arguments.length > 2) {
    throw new TypeError("files.importBlob expects a Blob and optional options");
  }
  var source = plainOptions(options, IMPORT_KEYS, "file import");
  var signal = checkedSignal(source.signal);
  var value = blobSnapshot(blob, source);
  var update = progress(source.onProgress, value.size);
  update(0, false);
  if (isAborted(signal)) return promiseReject(filesError("CANCELLED", "importBlob"));
  var lifecycle = {
    transport: null,
    abandoned: false,
    delivered: false,
    cleaned: false
  };
  var beginning = nativeCall("beginImport", {
    name: value.name,
    type: value.type,
    size: value.size
  }, beginResult).then(function (selected) {
    lifecycle.transport = selected;
    if (lifecycle.abandoned || isAborted(signal)) cleanupImport(lifecycle);
    return selected;
  });
  return abortable(beginning, signal, "importBlob").then(function (selected) {
    if (isAborted(signal)) throw filesError("CANCELLED", "importBlob");
    if (selected.mode === "http") return uploadHTTP(value.blob, value.size, selected, signal, update);
    return uploadBinary(value.blob, value.size, selected, signal, update);
  }).then(function () {
    if (isAborted(signal)) throw filesError("CANCELLED", "importBlob");
    return abortable(nativeCall("commitImport", { handle: lifecycle.transport.handle }, function (result) {
      return metadata(result, lifecycle.transport.handle);
    }), signal, "importBlob");
  }).then(function (result) {
    if (isAborted(signal)) {
      throw filesError("CANCELLED", "importBlob");
    }
    var reference = makeRef(result);
    lifecycle.delivered = true;
    return reference;
  }).catch(function (error) {
    lifecycle.abandoned = true;
    cleanupImport(lifecycle);
    if (error && error.name === "KitFilesError") throw error;
    throw filesError(errorCode(error), "importBlob");
  });
}

function statDescriptor(reference, state) {
  return nativeCall("stat", { handle: state.handle }, function (value) {
    return sameMetadata(reference, metadata(value, state.handle));
  });
}

function pick(options) {
  if (arguments.length > 1) throw new TypeError("files.pick expects optional options");
  var source = plainOptions(options, PICK_KEYS, "file picker");
  var accept = safeAccept(source.accept);
  var signal = checkedSignal(source.signal);
  if (isAborted(signal)) return promiseReject(filesError("CANCELLED", "pick"));
  if (!nativeHost || typeof FileType !== "function" || !addEvent || !removeEvent ||
    !setTimer || !clearTimer || !global.document || typeof global.document.createElement !== "function") {
    return promiseReject(filesError("UNAVAILABLE", "pick"));
  }
  if (activePicker) return promiseReject(filesError("OVERLOADED", "pick"));

  var input;
  var root;
  try {
    input = global.document.createElement("input");
    root = global.document.body || global.document.documentElement;
    if (!input || !root || typeof root.appendChild !== "function" || typeof input.click !== "function") {
      return promiseReject(filesError("UNAVAILABLE", "pick"));
    }
    input.type = "file";
    input.multiple = false;
    input.hidden = true;
    if (accept) input.accept = accept;
    root.appendChild(input);
  } catch (_) {
    return promiseReject(filesError("UNAVAILABLE", "pick"));
  }

  var selected = new PromiseType(function (resolve, reject) {
    var settled = false;
    var timeout = null;
    var removeAbort = function () {};

    function detach() {
      if (timeout !== null) clearTimer(timeout);
      removeEvent(input, "change", changed);
      removeEvent(input, "cancel", cancelled);
      removeAbort();
      try {
        if (typeof input.remove === "function") input.remove();
        else if (input.parentNode && typeof input.parentNode.removeChild === "function") {
          input.parentNode.removeChild(input);
        }
      } catch (_) { /* Detached picker controls own no reusable authority. */ }
      if (activePicker === input) activePicker = null;
    }

    function finish(value, error) {
      if (settled) return;
      settled = true;
      detach();
      if (error) reject(error);
      else resolve(value);
    }

    function pickedFile() {
      var files;
      try { files = input.files; }
      catch (_) { return null; }
      if (!files || files.length !== 1) return null;
      return files[0];
    }

    function changed() {
      var file = pickedFile();
      if (!file) {
        finish(null, filesError("FAILED", "pick"));
        return;
      }
      finish(file, null);
    }

    function cancelled() { finish(null, null); }

    activePicker = input;
    addEvent(input, "change", changed);
    addEvent(input, "cancel", cancelled);
    removeAbort = listenForAbort(signal, function () {
      finish(null, filesError("CANCELLED", "pick"));
    });
    if (isAborted(signal)) {
      finish(null, filesError("CANCELLED", "pick"));
      return;
    }
    timeout = setTimer(function () {
      finish(null, filesError("TIMEOUT", "pick"));
    }, PICK_TIMEOUT_MS);
    try { input.click(); }
    catch (_) { finish(null, filesError("DENIED", "pick")); }
  });

  return selected.then(function (file) {
    if (file === null) return null;
    var name;
    try { name = fileName.call(file); }
    catch (_) { throw filesError("FAILED", "pick"); }
    return importBlob(file, {
      name: name,
      signal: signal,
      onProgress: source.onProgress
    }).catch(function (error) {
      throw filesError(errorCode(error), "pick");
    });
  });
}

function stat(reference) {
  if (arguments.length !== 1) throw new TypeError("files.stat expects one FileRef");
  var state = stateFor(reference);
  return statDescriptor(reference, state).then(function (value) {
    return makeRef(value, state);
  });
}

function base64Bytes(value) {
  if (typeof value !== "string" || value.length % 4 !== 0 || value.length > 4 * mathCeil(MAX_CHUNK_BYTES / 3)) {
    throw new TypeError("invalid base64 file chunk");
  }
  var padding = value.length && value[value.length - 1] === "=" ? 1 : 0;
  if (padding && value.length > 1 && value[value.length - 2] === "=") padding = 2;
  var output = new Uint8ArrayType(value.length / 4 * 3 - padding);
  var offset = 0;
  for (var index = 0; index < value.length; index += 4) {
    var last = index + 4 === value.length;
    var a = stringIndexOf(BASE64, value[index]);
    var b = stringIndexOf(BASE64, value[index + 1]);
    var charC = value[index + 2];
    var charD = value[index + 3];
    var c = charC === "=" ? -2 : stringIndexOf(BASE64, charC);
    var d = charD === "=" ? -2 : stringIndexOf(BASE64, charD);
    if (a < 0 || b < 0) throw new TypeError("invalid base64 file chunk");
    if (c === -2) {
      if (!last || d !== -2 || (b & 15) !== 0) throw new TypeError("invalid base64 file chunk");
    } else {
      if (c < 0) throw new TypeError("invalid base64 file chunk");
      if (d === -2) {
        if (!last || (c & 3) !== 0) throw new TypeError("invalid base64 file chunk");
      } else if (d < 0) throw new TypeError("invalid base64 file chunk");
    }
    output[offset++] = a << 2 | b >> 4;
    if (c >= 0) output[offset++] = (b & 15) << 4 | c >> 2;
    if (d >= 0) output[offset++] = (c & 3) << 6 | d;
  }
  if (offset !== output.length) throw new TypeError("invalid base64 file chunk");
  return output;
}

function toBlob(reference, options) {
  if (arguments.length < 1 || arguments.length > 2) {
    throw new TypeError("files.toBlob expects a FileRef and optional options");
  }
  var state = stateFor(reference);
  var source = plainOptions(options, TRANSFER_KEYS, "file read");
  var signal = checkedSignal(source.signal);
  var update = progress(source.onProgress, reference.size);
  update(0, false);
  if (isAborted(signal)) return promiseReject(filesError("CANCELLED", "toBlob"));
  if (typeof BlobType !== "function" || !Uint8ArrayType) {
    return promiseReject(filesError("UNAVAILABLE", "toBlob"));
  }
  return abortable(statDescriptor(reference, state), signal, "toBlob").then(function (value) {
    if (value.size > MAX_MATERIALIZE_BYTES) throw filesError("TOO_LARGE", "toBlob");
    var parts = [];
    function read(offset) {
      if (isAborted(signal)) throw filesError("CANCELLED", "toBlob");
      if (offset === value.size) {
        update(offset, true);
        return new BlobType(parts, { type: value.type });
      }
      var length = mathMin(MAX_CHUNK_BYTES, value.size - offset);
      return abortable(nativeCall("readChunk", {
        handle: state.handle,
        offset: offset,
        length: length
      }, function (chunk) {
        var descriptors = exactObject(chunk, ["data", "offset", "size", "done"]);
        if (!descriptors || descriptors.offset.value !== offset || descriptors.size.value !== value.size ||
          typeof descriptors.done.value !== "boolean") throw new TypeError("invalid file chunk");
        var bytes = base64Bytes(descriptors.data.value);
        var next = offset + bytes.byteLength;
        if (!bytes.byteLength || bytes.byteLength > length || next > value.size ||
          descriptors.done.value !== (next === value.size)) throw new TypeError("invalid file chunk");
        return objectFreeze({ bytes: bytes, next: next });
      }), signal, "toBlob").then(function (chunk) {
        arrayPush(parts, chunk.bytes);
        update(chunk.next, chunk.next === value.size);
        return read(chunk.next);
      });
    }
    return read(0);
  }).catch(function (error) {
    if (error && error.name === "KitFilesError") throw error;
    throw filesError(errorCode(error), "toBlob");
  });
}

function share(reference, options) {
  if (arguments.length < 1 || arguments.length > 2) {
    throw new TypeError("files.share expects a FileRef and optional options");
  }
  var state = stateFor(reference);
  var source = plainOptions(options, SHARE_KEYS, "file share");
  var title = safeTitle(source.title);
  var params = { handle: state.handle };
  if (title !== undefined) params.title = title;
  return nativeCall("share", params, function (value) {
    if (typeof value !== "boolean") throw new TypeError("invalid file share result");
    return value;
  });
}

function release(reference) {
  if (arguments.length !== 1) throw new TypeError("files.release expects one FileRef");
  var state = reference && typeof reference === "object" ? references.get(reference) : null;
  if (!state) throw new TypeError("File reference is invalid");
  if (state.released) return promiseResolve(true);
  if (state.releasing) return state.releasing;
  var pending = nativeCall("release", { handle: state.handle }, function (value) {
    if (typeof value !== "boolean") throw new TypeError("invalid file release result");
    return value;
  }).then(function (value) {
    state.releasing = null;
    if (value === true) state.released = true;
    return value;
  }, function (error) {
    state.releasing = null;
    throw error;
  });
  state.releasing = pending;
  return pending;
}

kit.service("files", {
  pick: pick,
  importBlob: importBlob,
  stat: stat,
  toBlob: toBlob,
  share: share,
  release: release
});
})(globalThis, kit);

