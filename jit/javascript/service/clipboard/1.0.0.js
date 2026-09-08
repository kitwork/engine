;(function (global, kit) {
"use strict";

// KitJS service: clipboard@1.0.0
var MAX_TEXT_LENGTH = 1024 * 1024;
var stringCharCodeAt = Function.prototype.call.bind(String.prototype.charCodeAt);
var stringIndexOf = Function.prototype.call.bind(String.prototype.indexOf);
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;

function wellFormedText(value) {
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

function inputText(value) {
  if (typeof value !== "string") throw new TypeError("Clipboard text must be a string");
  if (value.length > MAX_TEXT_LENGTH || stringIndexOf(value, "\0") >= 0 || !wellFormedText(value)) {
    throw new TypeError("Clipboard text must be well-formed, NUL-free text up to 1048576 characters");
  }
  return value;
}

function errorCode(value) {
  var code = "";
  var name = "";
  try {
    code = value && typeof value.code === "string" ? value.code : "";
    name = value && typeof value.name === "string" ? value.name : "";
  }
  catch (_) { /* An untrusted adapter error never escapes normalization. */ }
  if (code === "DENIED" || name === "NotAllowedError" || name === "SecurityError") return "DENIED";
  if (code === "CANCELLED" || name === "AbortError") return "CANCELLED";
  if (code === "UNAVAILABLE" || code === "UNSUPPORTED" ||
    name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
  if (code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function clipboardError(code, operation) {
  var messages = {
    UNAVAILABLE: "Clipboard is unavailable",
    DENIED: "Clipboard permission was denied",
    CANCELLED: "Clipboard operation was cancelled",
    TIMEOUT: "Clipboard operation timed out",
    OVERLOADED: "Clipboard is busy",
    FAILED: "Clipboard operation failed"
  };
  var error = new Error(messages[code]);
  Object.defineProperties(error, {
    name: { value: "KitClipboardError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function capability(operation) {
  var navigator;
  var clipboard;
  var method;
  try {
    navigator = global.navigator;
    clipboard = navigator && navigator.clipboard;
    method = clipboard && clipboard[operation];
  } catch (error) {
    return { error: clipboardError(errorCode(error), operation) };
  }
  if (!clipboard || typeof method !== "function") {
    return { error: clipboardError("UNAVAILABLE", operation) };
  }
  return { target: clipboard, method: method };
}

function invoke(operation, value) {
  if (nativeHost) {
    var params = operation === "writeText" ? { text: value } : {};
    try {
      return Promise.resolve(nativeHost.call("clipboard." + operation, params)).then(
        function (result) {
          if (operation === "writeText" && typeof result !== "boolean") {
            throw clipboardError("FAILED", operation);
          }
          return result;
        },
        function (error) { throw clipboardError(errorCode(error), operation); }
      );
    } catch (error) {
      return Promise.reject(clipboardError(errorCode(error), operation));
    }
  }
  var selected = capability(operation);
  if (selected.error) return Promise.reject(selected.error);
  try {
    return Promise.resolve(selected.method.call(selected.target, value)).then(null, function (error) {
      throw clipboardError(errorCode(error), operation);
    });
  } catch (error) {
    return Promise.reject(clipboardError(errorCode(error), operation));
  }
}

function writeText(value) {
  value = inputText(value);
  return invoke("writeText", value).then(function () { return undefined; });
}

function readText() {
  return invoke("readText").then(function (value) {
    if (typeof value !== "string" || value.length > MAX_TEXT_LENGTH ||
      stringIndexOf(value, "\0") >= 0 || !wellFormedText(value)) {
      throw clipboardError("FAILED", "readText");
    }
    return value;
  });
}

kit.service("clipboard", {
  writeText: writeText,
  readText: readText
});
})(globalThis, kit);
