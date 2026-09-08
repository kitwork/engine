;(function (global, kit) {
"use strict";

// KitJS staged service: media@1.1.0
// Requires files@1.4.0 and adds no native permission beyond files.import.
var OWN = Object.prototype.hasOwnProperty;
var OPTIONS = Object.freeze({ signal: true });
var objectDefineProperties = Object.defineProperties;
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
var PromiseType = global.Promise;
var call = Function.prototype.call;
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var stringSlice = call.bind(String.prototype.slice);
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var graph = assembly && assembly.graph;
var files = kit.files;
var pickFile = files && files.pick;
var releaseFile = files && files.release;

if (!graph || !graph.services || graph.services.files !== "1.4.0" ||
  typeof pickFile !== "function" || typeof releaseFile !== "function") {
  throw new Error("KitJS: media requires its exact files boundary");
}

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* Cleanup diagnostics cannot change image selection settlement. */ }
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Dependency failures never escape this namespace. */ }
  if (code === "CONFLICT") return "OVERLOADED";
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED" || code === "TOO_LARGE" ||
    code === "UNSUPPORTED") return code;
  return "FAILED";
}

function mediaError(code, operation) {
  var messages = {
    DENIED: "Image library permission was denied",
    CANCELLED: "Image selection was cancelled",
    UNAVAILABLE: "Image library is unavailable",
    TIMEOUT: "Image selection timed out",
    OVERLOADED: "Image picker is busy",
    TOO_LARGE: "Selected image is too large",
    UNSUPPORTED: "Selected file is not an image",
    FAILED: "Image selection failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitMediaError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return objectFreeze(error);
}

function plainOptions(value) {
  if (value === undefined) return Object.create(null);
  var prototype;
  var symbols;
  var descriptors;
  try {
    prototype = objectGetPrototypeOf(value);
    symbols = objectGetOwnPropertySymbols(value);
    descriptors = objectGetOwnPropertyDescriptors(value);
  } catch (_) {
    throw new TypeError("Image picker options must be a plain object");
  }
  if (!value || prototype !== Object.prototype && prototype !== null || symbols.length) {
    throw new TypeError("Image picker options must be a plain object");
  }
  var output = Object.create(null);
  objectKeys(descriptors).forEach(function (name) {
    if (!OWN.call(OPTIONS, name)) throw new TypeError("Unknown image picker option: " + name);
    var descriptor = descriptors[name];
    if (!OWN.call(descriptor, "value") || descriptor.get || descriptor.set) {
      throw new TypeError("Image picker options must not contain accessors");
    }
    output[name] = descriptor.value;
  });
  return output;
}

function imageReference(reference) {
  if (!reference || typeof reference !== "object") return false;
  var descriptor;
  try { descriptor = objectGetOwnPropertyDescriptor(reference, "type"); }
  catch (_) { return false; }
  return Boolean(descriptor && OWN.call(descriptor, "value") && !descriptor.get && !descriptor.set &&
    typeof descriptor.value === "string" && descriptor.value.length > 6 &&
    stringSlice(descriptor.value, 0, 6) === "image/");
}

function discard(reference) {
  var pending;
  try { pending = releaseFile.call(files, reference); }
  catch (_) {
    report(mediaError("FAILED", "pickImage"));
    return promiseResolve(undefined);
  }
  return promiseResolve(pending).then(
    function () { return undefined; },
    function () {
      report(mediaError("FAILED", "pickImage"));
      return undefined;
    }
  );
}

function pickImage(input) {
  if (arguments.length > 1) throw new TypeError("media.pickImage accepts at most one options object");
  var options = plainOptions(input);
  var request = { accept: "image/*" };
  if (OWN.call(options, "signal")) request.signal = options.signal;
  var selected = pickFile.call(files, objectFreeze(request));
  return promiseResolve(selected).then(function (reference) {
    if (reference === null) return null;
    if (imageReference(reference)) return reference;
    var code = reference && typeof reference === "object" ? "UNSUPPORTED" : "FAILED";
    return discard(reference).then(function () {
      throw mediaError(code, "pickImage");
    });
  }, function (error) {
    throw mediaError(errorCode(error), "pickImage");
  });
}

kit.service("media", {
  pickImage: pickImage
});
})(globalThis, kit);
