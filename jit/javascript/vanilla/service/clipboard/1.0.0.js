;(function (global, kit) {
"use strict";

// KitJS service: clipboard@1.0.0
var MAX_TEXT_LENGTH = 1024 * 1024;

function inputText(value) {
  if (typeof value !== "string") throw new TypeError("Clipboard text must be a string");
  if (value.length > MAX_TEXT_LENGTH) {
    throw new TypeError("Clipboard text cannot exceed 1048576 characters");
  }
  return value;
}

function errorCode(value) {
  var name = "";
  try { name = value && typeof value.name === "string" ? value.name : ""; }
  catch (_) { /* An untrusted adapter error never escapes normalization. */ }
  if (name === "NotAllowedError" || name === "SecurityError") return "DENIED";
  if (name === "AbortError") return "CANCELLED";
  if (name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
  return "FAILED";
}

function clipboardError(code, operation) {
  var messages = {
    UNAVAILABLE: "Clipboard is unavailable",
    DENIED: "Clipboard permission was denied",
    CANCELLED: "Clipboard operation was cancelled",
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
    if (typeof value !== "string" || value.length > MAX_TEXT_LENGTH) {
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
