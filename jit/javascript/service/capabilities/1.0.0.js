;(function (global, kit) {
"use strict";

// KitJS staged service: capabilities@1.0.0
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var ID = /^[a-z][A-Za-z0-9]*\.[a-z][A-Za-z0-9]*$/;

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host errors never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function capabilityError(code) {
  var messages = {
    DENIED: "Capability query was denied",
    CANCELLED: "Capability query was cancelled",
    UNAVAILABLE: "Native capabilities are unavailable",
    TIMEOUT: "Capability query timed out",
    OVERLOADED: "Native capabilities are busy",
    FAILED: "Capability query failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitCapabilitiesError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: "supports", enumerable: true }
  });
  return Object.freeze(error);
}

function supports(id) {
  if (arguments.length !== 1 || typeof id !== "string" || id.length > 128 || !ID.test(id)) {
    throw new TypeError("Capability id must be a valid module.action string up to 128 characters");
  }
  if (!nativeHost) return Promise.resolve(false);
  try {
    return Promise.resolve(nativeHost.call("capabilities.supports", { id: id })).then(
      function (value) {
        if (typeof value !== "boolean") throw capabilityError("FAILED");
        return value;
      },
      function (error) { throw capabilityError(errorCode(error)); }
    );
  } catch (error) {
    return Promise.reject(capabilityError(errorCode(error)));
  }
}

kit.service("capabilities", { supports: supports });
})(globalThis, kit);
