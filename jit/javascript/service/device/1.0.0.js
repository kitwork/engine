;(function (global, kit) {
"use strict";

// KitJS staged service: device@1.0.0
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var OWN = Object.prototype.hasOwnProperty;
var MAX_PATTERN_PARTS = 32;
var MAX_PATTERN_PART_MS = 10000;
var MAX_PATTERN_TOTAL_MS = 60000;

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host errors never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function deviceError(code, operation) {
  var messages = {
    DENIED: "Device capability was denied",
    CANCELLED: "Device operation was cancelled",
    UNAVAILABLE: "Device capability is unavailable",
    TIMEOUT: "Device operation timed out",
    OVERLOADED: "Device capability is busy",
    FAILED: "Device operation failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitDeviceError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function call(operation, params, validate) {
  if (!nativeHost) return Promise.reject(deviceError("UNAVAILABLE", operation));
  try {
    return Promise.resolve(nativeHost.call("device." + operation, params)).then(
      function (value) {
        try { return validate(value); }
        catch (_) { throw deviceError("FAILED", operation); }
      },
      function (error) { throw deviceError(errorCode(error), operation); }
    );
  } catch (error) {
    return Promise.reject(deviceError(errorCode(error), operation));
  }
}

function boundedText(value, maximum) {
  return typeof value === "string" && value.length > 0 && value.length <= maximum && value.indexOf("\0") < 0;
}

function info() {
  if (arguments.length !== 0) throw new TypeError("device.info does not accept parameters");
  return call("info", {}, function (value) {
    var prototype = value && Object.getPrototypeOf(value);
    if (!value || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(value).length ||
      Object.keys(value).slice().sort().join(",") !== "model,osVersion,platform" ||
      !OWN.call(value, "platform") || !OWN.call(value, "osVersion") || !OWN.call(value, "model") ||
      value.platform !== "android" && value.platform !== "ios" ||
      !boundedText(value.osVersion, 128) || !boundedText(value.model, 256)) {
      throw new TypeError("invalid device info");
    }
    return Object.freeze({
      platform: value.platform,
      osVersion: value.osVersion,
      model: value.model
    });
  });
}

function vibrationPattern(value) {
  if (typeof value === "number") value = [value];
  if (!Array.isArray(value) || value.length < 1 || value.length > MAX_PATTERN_PARTS) {
    throw new TypeError("Vibration pattern must contain between 1 and 32 durations");
  }
  var total = 0;
  var output = value.map(function (part) {
    if (!Number.isInteger(part) || part < 0 || part > MAX_PATTERN_PART_MS) {
      throw new TypeError("Vibration durations must be integer milliseconds from 0 to 10000");
    }
    total += part;
    if (total > MAX_PATTERN_TOTAL_MS) throw new TypeError("Vibration pattern cannot exceed 60000 milliseconds");
    return part;
  });
  return Object.freeze(output);
}

function vibrate(pattern) {
  if (arguments.length !== 1) throw new TypeError("device.vibrate expects one pattern");
  pattern = vibrationPattern(pattern);
  return call("vibrate", { pattern: pattern }, function (value) {
    if (typeof value !== "boolean") throw new TypeError("invalid vibration result");
    return value;
  });
}

kit.service("device", { info: info, vibrate: vibrate });
})(globalThis, kit);
