;(function (global, document, kit) {
"use strict";

// KitJS staged service: deepLinks@1.1.0
// Native hosts retain only the latest normalized same-app route. The browser
// receives no raw external URL and every wake signal must re-enter the
// permission-checked request/response bridge.
var MAX_PATH_BYTES = 2048;
var ID = /^[0-9a-f]{32}$/;
var SetType = global.Set;
var PromiseType = global.Promise;
var call = Function.prototype.call;
var promiseReject = call.bind(PromiseType.reject, PromiseType);
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var decode = global.decodeURIComponent;
var objectPrototype = Object.prototype;
var hasOwn = call.bind(objectPrototype.hasOwnProperty);
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
var regExpTest = call.bind(RegExp.prototype.test);
var stringCharCodeAt = call.bind(String.prototype.charCodeAt);
var assembly = document && document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var listeners = new SetType();
var attached = [];
var inFlight = null;
var trailing = false;

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* Diagnostics never break another deep-link subscriber. */ }
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw native errors never cross this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  if (code === "BRIDGE_BUSY") return "OVERLOADED";
  if (code === "BRIDGE_TIMEOUT") return "TIMEOUT";
  if (code === "BRIDGE_UNAVAILABLE" || code === "UNSUPPORTED") return "UNAVAILABLE";
  return "FAILED";
}

function deepLinkError(code) {
  var messages = {
    DENIED: "Deep-link receipt permission was denied",
    CANCELLED: "Deep-link snapshot was cancelled",
    UNAVAILABLE: "Deep links are unavailable",
    TIMEOUT: "Deep-link snapshot timed out",
    OVERLOADED: "Deep-link service is busy",
    FAILED: "Deep-link snapshot failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitDeepLinkError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: "snapshot", enumerable: true }
  });
  return objectFreeze(error);
}

function upperHex(unit) {
  return unit >= 48 && unit <= 57 || unit >= 65 && unit <= 70;
}

function hexValue(unit) {
  return unit <= 57 ? unit - 48 : unit - 65 + 10;
}

function unreserved(unit) {
  return unit >= 48 && unit <= 57 || unit >= 65 && unit <= 90 || unit >= 97 && unit <= 122 ||
    unit === 45 || unit === 46 || unit === 95 || unit === 126;
}

function canonicalEscapes(value) {
  for (var index = 0; index < value.length; index++) {
    if (stringCharCodeAt(value, index) !== 37) continue;
    if (index + 2 >= value.length) return false;
    var high = stringCharCodeAt(value, index + 1);
    var low = stringCharCodeAt(value, index + 2);
    if (!upperHex(high) || !upperHex(low) || unreserved(hexValue(high) * 16 + hexValue(low))) return false;
    index += 2;
  }
  return true;
}

function controlOrBackslash(value) {
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit === 92 || unit <= 31 || unit >= 127 && unit <= 159) return true;
  }
  return false;
}

function dotSegments(path) {
  var segments = path.split("/");
  for (var index = 0; index < segments.length; index++) {
    if (segments[index] === "." || segments[index] === "..") return true;
  }
  return false;
}

function validPath(value) {
  if (typeof value !== "string" || !value || value.length > MAX_PATH_BYTES ||
    value.charAt(0) !== "/" || value.slice(0, 2) === "//" ||
    value.charAt(value.length - 1) === "?" || value.charAt(value.length - 1) === "#" ||
    value.indexOf("?#") >= 0) return false;
  var fragments = 0;
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit <= 32 || unit >= 127 || unit === 92 ||
      unit === 34 || unit === 60 || unit === 62 || unit === 91 || unit === 93 ||
      unit === 94 || unit === 96 || unit === 123 || unit === 124 || unit === 125) return false;
    if (unit === 35 && ++fragments > 1) return false;
    if (unit === 37) {
      if (index + 2 >= value.length) return false;
      var high = stringCharCodeAt(value, index + 1);
      var low = stringCharCodeAt(value, index + 2);
      if (!upperHex(high) || !upperHex(low) || unreserved(hexValue(high) * 16 + hexValue(low))) return false;
      index += 2;
    }
  }
  var delimiter = value.search(/[?#]/);
  var path = delimiter < 0 ? value : value.slice(0, delimiter);
  var decodedPath = path;
  for (var depth = 0; depth < 8; depth++) {
    if (!canonicalEscapes(decodedPath) || /%2F|%5C/i.test(decodedPath)) return false;
    var nextPath;
    try { nextPath = decode(decodedPath); }
    catch (_) { return false; }
    if (nextPath.charAt(0) !== "/" || nextPath.slice(0, 2) === "//" ||
      controlOrBackslash(nextPath) || dotSegments(nextPath)) return false;
    if (nextPath === decodedPath) break;
    decodedPath = nextPath;
    if (depth === 7) {
      if (decodedPath.indexOf("%") >= 0) return false;
      break;
    }
  }
  var decoded = value;
  for (var round = 0; round < 8; round++) {
    if (!canonicalEscapes(decoded)) return false;
    var next;
    try { next = decode(decoded); }
    catch (_) { return false; }
    if (controlOrBackslash(next)) return false;
    if (next === decoded) return true;
    decoded = next;
    if (round === 7) return decoded.indexOf("%") < 0;
  }
  return false;
}

function validatedSnapshot(value) {
  if (value === null) return null;
  var prototype;
  var symbols;
  var descriptors;
  try {
    prototype = value && objectGetPrototypeOf(value);
    symbols = value && objectGetOwnPropertySymbols(value);
    descriptors = value && objectGetOwnPropertyDescriptors(value);
  } catch (_) { throw deepLinkError("FAILED"); }
  if (!value || prototype !== objectPrototype && prototype !== null || symbols.length) {
    throw deepLinkError("FAILED");
  }
  var names = objectKeys(descriptors);
  if (names.length !== 2 || !hasOwn(descriptors, "id") || !hasOwn(descriptors, "path")) {
    throw deepLinkError("FAILED");
  }
  var id = descriptors.id;
  var path = descriptors.path;
  if (!id.enumerable || !path.enumerable || !hasOwn(id, "value") || !hasOwn(path, "value") ||
    id.get || id.set || path.get || path.set || typeof id.value !== "string" ||
    !regExpTest(ID, id.value) || !validPath(path.value)) {
    throw deepLinkError("FAILED");
  }
  return objectFreeze({ id: id.value, path: path.value });
}

function requestSnapshot() {
  if (!nativeHost) return promiseResolve(null);
  try {
    return promiseResolve(nativeHost.call("deepLinks.snapshot", {})).then(
      function (value) { return validatedSnapshot(value); },
      function (error) { throw deepLinkError(errorCode(error)); }
    );
  } catch (error) {
    return promiseReject(deepLinkError(errorCode(error)));
  }
}

function snapshot() {
  if (arguments.length !== 0) throw new TypeError("deepLinks.snapshot does not accept parameters");
  return requestSnapshot();
}

function deliver(value) {
  if (!value) return;
  Array.from(listeners).forEach(function (subscription) {
    if (!subscription.listener || subscription.lastID === value.id) return;
    subscription.lastID = value.id;
    try { subscription.listener(value); }
    catch (error) { report(error); }
  });
}

function refresh() {
  if (!nativeHost || !listeners.size) return;
  if (inFlight) {
    trailing = true;
    return;
  }
  var operation = { promise: null };
  inFlight = operation;
  operation.promise = requestSnapshot();
  operation.promise.then(function (value) {
    if (inFlight !== operation) return;
    var rerun = trailing;
    trailing = false;
    inFlight = null;
    // A wake that arrived during the request proves this result may already be
    // stale. Latest-wins means it is discarded before querying again.
    if (!rerun) deliver(value);
    if (rerun && listeners.size) refresh();
  }, function (error) {
    if (inFlight !== operation) return;
    var rerun = trailing;
    trailing = false;
    inFlight = null;
    if (!rerun) report(error);
    if (rerun && listeners.size) refresh();
  });
}

function add(target, type) {
  var method;
  try { method = target && target.addEventListener; }
  catch (error) { throw error; }
  if (typeof method !== "function") return;
  method.call(target, type, refresh);
  attached.push({ target: target, type: type });
}

function detach() {
  var records = attached;
  attached = [];
  for (var index = records.length - 1; index >= 0; index--) {
    var record = records[index];
    try {
      var method = record.target && record.target.removeEventListener;
      if (typeof method === "function") method.call(record.target, record.type, refresh);
    } catch (error) { report(error); }
  }
  trailing = false;
  // Promise cancellation is not portable, so abandon the operation identity.
  // Its attached handlers will ignore any late value or rejection.
  inFlight = null;
}

function attach() {
  if (!nativeHost || attached.length) return;
  try {
    add(global, "focus");
    add(global, "pageshow");
    add(global, "kitwork:deep-link");
    add(document, "visibilitychange");
  } catch (error) {
    detach();
    throw error;
  }
}

function subscribe(listener) {
  if (arguments.length !== 1 || typeof listener !== "function") {
    throw new TypeError("Deep-link subscriber must be one function");
  }
  var subscription = { listener: listener, lastID: null };
  listeners.add(subscription);
  if (listeners.size === 1) {
    try { attach(); }
    catch (error) {
      listeners.delete(subscription);
      subscription.listener = null;
      throw error;
    }
  }
  refresh();
  var subscribed = true;
  return function () {
    if (!subscribed) return;
    subscribed = false;
    listeners.delete(subscription);
    subscription.listener = null;
    listener = null;
    if (listeners.size === 0) detach();
  };
}

kit.service("deepLinks", { snapshot: snapshot, subscribe: subscribe });
})(globalThis, document, kit);
