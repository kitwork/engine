;(function (global, kit) {
"use strict";

// KitJS staged service: secureStorage@1.0.0
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var KEY = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
var MAX_VALUE_BYTES = 1024 * 1024;

function utf8Length(value) {
  var bytes = 0;
  for (var index = 0; index < value.length; index++) {
    var unit = value.charCodeAt(index);
    if (unit < 0x80) bytes++;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xD800 && unit <= 0xDFFF) {
      if (unit > 0xDBFF || index + 1 >= value.length ||
        value.charCodeAt(index + 1) < 0xDC00 || value.charCodeAt(index + 1) > 0xDFFF) {
        return MAX_VALUE_BYTES + 1;
      }
      bytes += 4;
      index++;
    } else bytes += 3;
    if (bytes > MAX_VALUE_BYTES) return bytes;
  }
  return bytes;
}

function safeKey(value) {
  if (typeof value !== "string" || !KEY.test(value)) {
    throw new TypeError("Secure storage key must use 1-128 ASCII letters, digits, dot, underscore, colon, or dash");
  }
  return value;
}

function safeValue(value) {
  if (typeof value !== "string" || value.indexOf("\0") >= 0 || utf8Length(value) > MAX_VALUE_BYTES) {
    throw new TypeError("Secure storage value must be a NUL-free UTF-8 string up to 1 MiB");
  }
  return value;
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host errors never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function storageError(code, operation) {
  var messages = {
    DENIED: "Secure storage permission was denied",
    CANCELLED: "Secure storage operation was cancelled",
    UNAVAILABLE: "Secure storage is unavailable",
    TIMEOUT: "Secure storage operation timed out",
    OVERLOADED: "Secure storage is busy",
    FAILED: "Secure storage operation failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitSecureStorageError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function call(operation, params, validate) {
  if (!nativeHost) return Promise.reject(storageError("UNAVAILABLE", operation));
  try {
    return Promise.resolve(nativeHost.call("secureStorage." + operation, params)).then(
      function (value) {
        try { return validate(value); }
        catch (_) { throw storageError("FAILED", operation); }
      },
      function (error) { throw storageError(errorCode(error), operation); }
    );
  } catch (error) {
    return Promise.reject(storageError(errorCode(error), operation));
  }
}

function get(key) {
  if (arguments.length !== 1) throw new TypeError("secureStorage.get expects one key");
  key = safeKey(key);
  return call("get", { key: key }, function (value) {
    if (value === null) return null;
    return safeValue(value);
  });
}

function set(key, value) {
  if (arguments.length !== 2) throw new TypeError("secureStorage.set expects key and value");
  key = safeKey(key);
  value = safeValue(value);
  return call("set", { key: key, value: value }, function (result) {
    if (typeof result !== "boolean") throw new TypeError("invalid secure storage result");
    return result;
  });
}

function remove(key) {
  if (arguments.length !== 1) throw new TypeError("secureStorage.remove expects one key");
  key = safeKey(key);
  return call("remove", { key: key }, function (result) {
    if (typeof result !== "boolean") throw new TypeError("invalid secure storage result");
    return result;
  });
}

kit.service("secureStorage", { get: get, set: set, remove: remove });
})(globalThis, kit);
