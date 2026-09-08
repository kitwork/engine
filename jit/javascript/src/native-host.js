; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var HOST = Symbol.for("kitwork:native:host:v1");
  var FILES = Symbol.for("kitwork:native:files:v1");
  var CONTRACT_VERSION = "1.0.0";
  var FILES_CONTRACT_VERSION = "1.0.0";
  var MAX_ACTION_LENGTH = 128;
  var MAX_JSON_LENGTH = 8 * 1024 * 1024;
  var MAX_TREE_DEPTH = 32;
  var MAX_TREE_NODES = 4096;
  // A 100-row, 128-column edit can echo original and changed BLOB values in
  // both conflict-check and mutation traces; the JSON byte bound still applies.
  var MAX_STUDIO_COMMIT_TREE_NODES = 262144;
  var MAX_PENDING = 64;
  var TIMEOUT_MS = 15000;
  var MAX_FILE_FRAME_BYTES = 4 + 24 + 4 + 256 * 1024;
  var OWN = Object.prototype.hasOwnProperty;
  var ArrayBufferType = global.ArrayBuffer;
  var Uint8ArrayType = global.Uint8Array;
  var getPrototypeOf = Object.getPrototypeOf;
  var arrayBufferByteLength = ArrayBufferType &&
    Object.getOwnPropertyDescriptor(ArrayBufferType.prototype, "byteLength").get;
  var arrayBufferSlice = ArrayBufferType &&
    Function.prototype.call.bind(ArrayBufferType.prototype.slice);
  var core = document[ASSEMBLY];

  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: native host transport loaded out of order");
  }
  if (core.reuse) {
    if (OWN.call(document, HOST)) {
      try { delete document[HOST]; }
      catch (_) { throw transportError("INVALID_HOST", "install"); }
    }
    if (OWN.call(document, FILES)) {
      try { delete document[FILES]; }
      catch (_) { throw transportError("INVALID_HOST", "files.install"); }
    }
    return;
  }
  if (OWN.call(core, "nativeHost") || OWN.call(core, "nativeFiles")) {
    throw new Error("KitJS: native host transport is already installed");
  }
  if (typeof core.validServiceName !== "function" || typeof core.blocked !== "function" ||
    typeof core.installComponentGraph !== "function" ||
    typeof core.installStagedDelivery !== "function" ||
    typeof core.beginComponentHandoff !== "function") {
    throw new Error("KitJS: native host transport requires the staged service graph");
  }

  var validServiceName = core.validServiceName;
  core.validServiceName = function (name) {
    return name === "window" || validServiceName(name);
  };

  var blocked = core.blocked;
  var beginComponentHandoff = core.beginComponentHandoff;

  function withWindowServiceName(callback) {
    if (typeof callback !== "function") {
      throw new TypeError("KitJS: native service validation expects a function");
    }
    var previous = core.blocked;
    core.blocked = function (name) {
      return name === "window" ? false : blocked(name);
    };
    try { return callback(); }
    finally { core.blocked = previous; }
  }

  Object.defineProperty(core, "withNativeServiceValidation", {
    value: withWindowServiceName
  });
  core.beginComponentHandoff = function () {
    var receiver = this;
    var args = arguments;
    return withWindowServiceName(function () {
      return beginComponentHandoff.apply(receiver, args);
    });
  };

  function transportError(code, action) {
    var messages = {
      INVALID_HOST: "Native host contract is invalid",
      INVALID_ACTION: "Native host action is invalid",
      INVALID_PAYLOAD: "Native host payload is invalid",
      INVALID_RESULT: "Native host result is invalid",
      OVERLOADED: "Native host is busy",
      BUSY: "Native host is busy",
      NOT_FOUND: "Native host resource was not found",
      INVALID_REQUEST: "Native host request is invalid",
      INVALID_DATABASE: "Native host database is invalid",
      DATABASE_CORRUPT: "Native host database is corrupt",
      SCHEMA_CHANGED: "Native host database schema changed",
      READ_ONLY_REQUIRED: "Native host operation must be read-only",
      SQL_ERROR: "Native host could not execute the query",
      WRITE_UNAVAILABLE: "Native host database writes are unavailable",
      READ_ONLY_TABLE: "Native host table does not support editing",
      INVALID_CHANGE: "Native host database change is invalid",
      ROW_CONFLICT: "Native host database row changed",
      CONSTRAINT_FAILED: "Native host database constraint failed",
      LIMIT: "Native host operation exceeds the supported limit",
      UNSUPPORTED: "Native host operation is not supported",
      TIMEOUT: "Native host call timed out",
      DENIED: "Native host call was denied",
      CANCELLED: "Native host call was cancelled",
      UNAVAILABLE: "Native host capability is unavailable",
      FAILED: "Native host call failed"
    };
    var error = new Error(messages[code] || messages.FAILED);
    Object.defineProperties(error, {
      name: { value: "KitNativeHostError" },
      code: { value: code || "FAILED", enumerable: true },
      action: { value: action || "unknown", enumerable: true }
    });
    return Object.freeze(error);
  }

  function installHost(value) {
    Object.defineProperty(core, "nativeHost", {
      value: value,
      enumerable: false,
      configurable: true
    });
  }

  function installFiles(value) {
    Object.defineProperty(core, "nativeFiles", {
      value: value,
      enumerable: false,
      configurable: true
    });
  }

  var seeded = null;
  var seededFiles = null;
  if (OWN.call(document, HOST)) {
    try {
      seeded = document[HOST];
      delete document[HOST];
    }
    catch (_) { throw transportError("INVALID_HOST", "install"); }
    if (OWN.call(document, HOST)) throw transportError("INVALID_HOST", "install");
  }
  if (OWN.call(document, FILES)) {
    try {
      seededFiles = document[FILES];
      delete document[FILES];
    }
    catch (_) { throw transportError("INVALID_HOST", "files.install"); }
    if (OWN.call(document, FILES)) throw transportError("INVALID_HOST", "files.install");
  }
  if (seeded === null || seeded === undefined) {
    if (seededFiles !== null && seededFiles !== undefined) {
      throw transportError("INVALID_HOST", "files.install");
    }
    installHost(null);
    installFiles(null);
    return;
  }

  var version;
  var callMethod;
  try {
    version = seeded.version;
    callMethod = seeded.call;
  } catch (_) {
    throw transportError("INVALID_HOST", "install");
  }
  if (version !== CONTRACT_VERSION || typeof callMethod !== "function") {
    throw transportError("INVALID_HOST", "install");
  }

  var filesSendMethod = null;
  if (seededFiles !== null && seededFiles !== undefined) {
    var filesVersion;
    try {
      filesVersion = seededFiles.version;
      filesSendMethod = seededFiles.send;
    } catch (_) {
      throw transportError("INVALID_HOST", "files.install");
    }
    if (filesVersion !== FILES_CONTRACT_VERSION || typeof filesSendMethod !== "function" ||
      !ArrayBufferType || !Uint8ArrayType || !arrayBufferByteLength || !arrayBufferSlice) {
      throw transportError("INVALID_HOST", "files.install");
    }
  }

  function checkedAction(action) {
    if (typeof action !== "string" || action.length > MAX_ACTION_LENGTH ||
      !/^[a-z][A-Za-z0-9]*\.[a-z][A-Za-z0-9]*$/.test(action)) {
      throw transportError("INVALID_ACTION", "call");
    }
    return action;
  }

  function checkedJSON(value, action, code, objectRoot) {
    var seen = new WeakSet();
    var nodes = 0;
    var units = 0;
    var maximumNodes = action === "studioSqlite.commit" ? MAX_STUDIO_COMMIT_TREE_NODES : MAX_TREE_NODES;

    function invalid() { throw transportError(code, action); }
    function account(length) {
      units += length;
      if (units > MAX_JSON_LENGTH) invalid();
    }
    function clone(input, depth) {
      nodes++;
      if (nodes > maximumNodes || depth > MAX_TREE_DEPTH) invalid();
      if (input === null) return null;
      if (typeof input === "string") {
        account(input.length);
        return input;
      }
      if (typeof input === "boolean") return input;
      if (typeof input === "number") {
        if (!Number.isFinite(input)) invalid();
        return input;
      }
      if (typeof input !== "object") invalid();
      if (seen.has(input)) invalid();
      seen.add(input);

      var array;
      var prototype;
      var symbols;
      var descriptors;
      try {
        array = Array.isArray(input);
        prototype = Object.getPrototypeOf(input);
        symbols = Object.getOwnPropertySymbols(input);
        descriptors = Object.getOwnPropertyDescriptors(input);
      } catch (_) { invalid(); }
      if (symbols.length || (array ? prototype !== Array.prototype :
        prototype !== Object.prototype && prototype !== null)) invalid();

      if (array) {
        if (Object.getOwnPropertyDescriptor(Array.prototype, "toJSON") ||
          Object.getOwnPropertyDescriptor(Object.prototype, "toJSON")) invalid();
        var lengthDescriptor = descriptors.length;
        var length = lengthDescriptor && lengthDescriptor.value;
        if (!lengthDescriptor || OWN.call(lengthDescriptor, "get") ||
          typeof length !== "number" || length < 0 || length > MAX_TREE_NODES) invalid();
        var names = Object.keys(descriptors);
        if (names.length !== length + 1) invalid();
        var list = new Array(length);
        for (var index = 0; index < length; index++) {
          var item = descriptors[String(index)];
          if (!item || !item.enumerable || !OWN.call(item, "value") || item.get || item.set) invalid();
          list[index] = clone(item.value, depth + 1);
        }
        return Object.freeze(list);
      }

      var output = Object.create(null);
      Object.keys(descriptors).forEach(function (key) {
        if (key === "toJSON" || key === "__proto__" || key === "prototype" || key === "constructor") invalid();
        account(key.length);
        var descriptor = descriptors[key];
        if (!descriptor.enumerable || !OWN.call(descriptor, "value") || descriptor.get || descriptor.set) invalid();
        output[key] = clone(descriptor.value, depth + 1);
      });
      return Object.freeze(output);
    }

    var cloned = clone(value, 0);
    if (objectRoot && (!cloned || typeof cloned !== "object" || Array.isArray(cloned))) invalid();
    var encoded;
    try { encoded = JSON.stringify(cloned); }
    catch (_) { invalid(); }
    if (typeof encoded !== "string" || encoded.length > MAX_JSON_LENGTH) invalid();
    return cloned;
  }

  function checkedPayload(value, action) {
    if (value === undefined) value = {};
    return checkedJSON(value, action, "INVALID_PAYLOAD", true);
  }

  function checkedResult(value, action) {
    if (value === undefined) return undefined;
    return checkedJSON(value, action, "INVALID_RESULT", false);
  }

  function rejectionCode(value) {
    var code = "";
    var name = "";
    try {
      code = value && typeof value.code === "string" ? value.code : "";
      name = value && typeof value.name === "string" ? value.name : "";
    } catch (_) { /* Raw host failures never escape the transport. */ }
    if (code === "BRIDGE_BUSY") return "OVERLOADED";
    if (code === "BRIDGE_TIMEOUT") return "TIMEOUT";
    if (code === "BRIDGE_UNAVAILABLE") return "UNAVAILABLE";
    if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
      code === "OVERLOADED" || code === "BUSY" || code === "NOT_FOUND" ||
      code === "INVALID_REQUEST" || code === "INVALID_DATABASE" || code === "DATABASE_CORRUPT" ||
      code === "SCHEMA_CHANGED" || code === "READ_ONLY_REQUIRED" || code === "SQL_ERROR" ||
      code === "WRITE_UNAVAILABLE" || code === "READ_ONLY_TABLE" || code === "INVALID_CHANGE" ||
      code === "ROW_CONFLICT" || code === "CONSTRAINT_FAILED" ||
      code === "LIMIT" || code === "UNSUPPORTED" || code === "TIMEOUT" || code === "FAILED") return code;
    if (name === "NotAllowedError" || name === "SecurityError") return "DENIED";
    if (name === "AbortError") return "CANCELLED";
    if (name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
    return "FAILED";
  }

  var pending = 0;
  function call(action, params) {
    try {
      action = checkedAction(action);
      params = checkedPayload(params, action);
    } catch (error) {
      return Promise.reject(error);
    }
    if (pending >= MAX_PENDING) {
      return Promise.reject(transportError("OVERLOADED", action));
    }
    pending++;
    return new Promise(function (resolve, reject) {
      var settled = false;
      var timer = null;
      function finish(ok, value) {
        if (settled) return;
        settled = true;
        pending--;
        if (timer !== null) {
          try { global.clearTimeout(timer); } catch (_) { /* Settlement remains authoritative. */ }
        }
        if (ok) resolve(value);
        else reject(value);
      }
      try {
        timer = global.setTimeout(function () {
          finish(false, transportError("TIMEOUT", action));
        }, TIMEOUT_MS);
        Promise.resolve(callMethod.call(seeded, action, params)).then(function (value) {
          try { finish(true, checkedResult(value, action)); }
          catch (error) { finish(false, error); }
        }, function (error) {
          finish(false, transportError(rejectionCode(error), action));
        });
      } catch (error) {
        finish(false, transportError(rejectionCode(error), action));
      }
    });
  }

  function sendFileFrame(frame) {
    var length;
    try {
      if (!frame || getPrototypeOf(frame) !== ArrayBufferType.prototype) {
        throw transportError("INVALID_PAYLOAD", "files.send");
      }
      length = arrayBufferByteLength.call(frame);
    } catch (_) {
      throw transportError("INVALID_PAYLOAD", "files.send");
    }
    if (length < 33 || length > MAX_FILE_FRAME_BYTES) {
      throw transportError("INVALID_PAYLOAD", "files.send");
    }
    var bytes = new Uint8ArrayType(frame);
    if (bytes[0] !== 0x4b || bytes[1] !== 0x57 || bytes[2] !== 0x46 || bytes[3] !== 0x31) {
      throw transportError("INVALID_PAYLOAD", "files.send");
    }
    var admitted = arrayBufferSlice(frame, 0);
    try {
      if (filesSendMethod.call(seededFiles, admitted) !== true) {
        throw transportError("INVALID_RESULT", "files.send");
      }
    } catch (error) {
      if (error && error.name === "KitNativeHostError") throw error;
      throw transportError(rejectionCode(error), "files.send");
    }
    return true;
  }

  installHost(Object.freeze({
    version: CONTRACT_VERSION,
    call: call
  }));
  installFiles(filesSendMethod === null ? null : Object.freeze({
    version: FILES_CONTRACT_VERSION,
    send: sendFileFrame
  }));
})(globalThis, document);
