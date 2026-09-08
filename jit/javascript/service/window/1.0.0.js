;(function (global, kit) {
"use strict";

// KitJS service: window@1.0.0
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;

function errorCode(value) {
  var code = "";
  var name = "";
  try {
    code = value && typeof value.code === "string" ? value.code : "";
    name = value && typeof value.name === "string" ? value.name : "";
  } catch (_) { /* An untrusted adapter error never escapes normalization. */ }
  if (code === "DENIED" || name === "NotAllowedError" || name === "SecurityError") return "DENIED";
  if (code === "CANCELLED" || name === "AbortError") return "CANCELLED";
  if (code === "UNAVAILABLE" || code === "UNSUPPORTED" ||
    name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
  return "FAILED";
}

function windowError(code, operation) {
  var messages = {
    UNAVAILABLE: "Window control is unavailable",
    DENIED: "Window control was denied",
    CANCELLED: "Window control was cancelled",
    FAILED: "Window control failed"
  };
  var error = new Error(messages[code]);
  Object.defineProperties(error, {
    name: { value: "KitWindowError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function command(operation) {
  if (!nativeHost) return Promise.resolve(false);
  try {
    return Promise.resolve(nativeHost.call("window." + operation, {})).then(
      function (value) {
        if (value !== true) throw windowError("FAILED", operation);
        return true;
      },
      function (error) { throw windowError(errorCode(error), operation); }
    );
  } catch (error) {
    return Promise.reject(windowError(errorCode(error), operation));
  }
}

function query(operation) {
  if (!nativeHost) return Promise.resolve(false);
  try {
    return Promise.resolve(nativeHost.call("window." + operation, {})).then(
      function (value) {
        if (value !== true && value !== false) throw windowError("FAILED", operation);
        return value;
      },
      function (error) { throw windowError(errorCode(error), operation); }
    );
  } catch (error) {
    return Promise.reject(windowError(errorCode(error), operation));
  }
}

function minimize() { return command("minimize"); }
function maximize() { return command("maximize"); }
function restore() { return command("restore"); }
function close() { return command("close"); }
function drag() { return command("drag"); }
function isMaximized() { return query("isMaximized"); }

kit.service("window", {
  drag: drag,
  isMaximized: isMaximized,
  minimize: minimize,
  maximize: maximize,
  restore: restore,
  close: close
});
})(globalThis, kit);
