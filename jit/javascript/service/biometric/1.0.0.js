;(function (global, kit) {
"use strict";

// KitJS staged service: biometric@1.0.0
// Requires capabilities@1.0.0.
var OWN = Object.prototype.hasOwnProperty;
var OPTIONS = Object.freeze({ signal: true, reason: true });
var OPERATION = /^[A-Za-z0-9_-]{32}$/;
var MAX_VALUE_BYTES = 4096;
var POLL_DELAY_MS = 250;
var SCAN_TIMEOUT_MS = 65 * 1000;
var SCAN_MAX_POLLS = 260;
var TOO_LARGE_VALUE = Object.freeze({});
var objectDefineProperties = Object.defineProperties;
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
var PromiseType = global.Promise;
var AbortSignalType = global.AbortSignal;
var EventTargetType = global.EventTarget;
var call = Function.prototype.call;
var regExpTest = call.bind(RegExp.prototype.test);
var stringCharCodeAt = call.bind(String.prototype.charCodeAt);
var setTimer = typeof global.setTimeout === "function" && global.setTimeout.bind(global);
var clearTimer = typeof global.clearTimeout === "function" && global.clearTimeout.bind(global);
var signalAborted = AbortSignalType &&
  objectGetOwnPropertyDescriptor(AbortSignalType.prototype, "aborted").get;
var addEvent = EventTargetType && call.bind(EventTargetType.prototype.addEventListener);
var removeEvent = EventTargetType && call.bind(EventTargetType.prototype.removeEventListener);
var promiseReject = call.bind(PromiseType.reject, PromiseType);
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var capabilities = kit.capabilities;
var supportsCapability = capabilities && capabilities.supports;
var activeScan = null;
var BEGIN_TIMEOUT_MS = 15000;
var RELEASE_TIMEOUT_MS = 3000;
var numberIsFinite = Number.isFinite;
var numberIsInteger = Number.isInteger;
var numberIsSafeInteger = Number.isSafeInteger;
var arrayIsArray = Array.isArray;
var arrayPrototype = Array.prototype;

if (typeof supportsCapability !== "function") {
  throw new Error("KitJS: biometric requires its exact capabilities boundary");
}

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* Cleanup diagnostics cannot change scan settlement. */ }
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host failures never escape this namespace. */ }
  if (code === "CONFLICT") return "OVERLOADED";
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED" || code === "TOO_LARGE") return code;
  return "FAILED";
}

function serviceError(code, operation) {
  var messages = {
    DENIED: "Biometric authentication was denied",
    CANCELLED: "Biometric authentication was cancelled",
    UNAVAILABLE: "Biometric authentication is unavailable",
    TIMEOUT: "Biometric authentication timed out",
    OVERLOADED: "Biometric authentication is busy",
    TOO_LARGE: "Biometric result is too large",
    FAILED: "Biometric authentication failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitBiometricError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return objectFreeze(error);
}

function nativeCall(operation, params, validator) {
  if (!nativeHost) return promiseReject(serviceError("UNAVAILABLE", "authenticate"));
  try {
    return promiseResolve(nativeHost.call("biometric." + operation, params)).then(
      function (value) {
        try { return validator(value); }
        catch (error) {
          if (error === TOO_LARGE_VALUE) throw serviceError("TOO_LARGE", "authenticate");
          throw serviceError("FAILED", "authenticate");
        }
      },
      function (error) { throw serviceError(errorCode(error), "authenticate"); }
    );
  } catch (error) {
    return promiseReject(serviceError(errorCode(error), "authenticate"));
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
    if (!descriptor || !descriptor.enumerable || !OWN.call(descriptor, "value") ||
      descriptor.get || descriptor.set) return null;
  }
  return descriptors;
}

function safeOperation(value) {
  if (typeof value !== "string" || !regExpTest(OPERATION, value)) {
    throw new TypeError("invalid Biometric operation");
  }
  return value;
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
    if (bytes > MAX_VALUE_BYTES) return bytes;
  }
  return bytes;
}

function safeValue(value) {
  if (value !== true) throw new TypeError("Invalid biometric result");
  return true;
}

function beginResult(value) {
  var descriptors = exactObject(value, ["operation"]);
  if (!descriptors) throw new TypeError("invalid Biometric begin result");
  return safeOperation(descriptors.operation.value);
}

function pollResult(value) {
  var descriptors = exactObject(value, ["status"]);
  if (descriptors) {
    var status = descriptors.status.value;
    if (status === "pending" || status === "cancelled") {
      return objectFreeze({ status: status });
    }
    throw new TypeError("invalid Biometric status");
  }
  descriptors = exactObject(value, ["status", "code"]);
  if (descriptors && descriptors.status.value === "failed") {
    var code = descriptors.code.value;
    if (code === "DENIED" || code === "UNAVAILABLE" || code === "TIMEOUT" || code === "OVERLOADED" || code === "FAILED") {
      return objectFreeze({ status: "failed", code: code });
    }
    throw new TypeError("invalid Biometric failure code");
  }
  descriptors = exactObject(value, ["status", "value"]);
  if (!descriptors || descriptors.status.value !== "ready") {
    throw new TypeError("invalid Biometric poll result");
  }
  return objectFreeze({ status: "ready", value: safeValue(descriptors.value.value) });
}

function releaseResult(value) {
  if (value !== true) throw new TypeError("invalid Biometric release result");
  return value;
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
    throw new TypeError("Biometric scan options must be a plain object");
  }
  if (!value || prototype !== Object.prototype && prototype !== null || symbols.length) {
    throw new TypeError("Biometric scan options must be a plain object");
  }
  var output = Object.create(null);
  objectKeys(descriptors).forEach(function (name) {
    if (!OWN.call(OPTIONS, name)) throw new TypeError("Unknown Biometric scan option: " + name);
    var descriptor = descriptors[name];
    if (!descriptor.enumerable || !OWN.call(descriptor, "value") || descriptor.get || descriptor.set) {
      throw new TypeError("Biometric scan options must not contain accessors");
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
    catch (_) { /* A settled scan no longer depends on listener cleanup. */ }
  };
}

function available() {
  if (arguments.length !== 0) throw new TypeError("biometric.available does not accept parameters");
  if (!nativeHost) return promiseResolve(false);
  try {
    return promiseResolve(supportsCapability.call(capabilities, "biometric.authenticate")).then(
      function (value) {
        if (typeof value !== "boolean") throw serviceError("FAILED", "available");
        return value;
      },
      function (error) { throw serviceError(errorCode(error), "available"); }
    );
  } catch (error) {
    return promiseReject(serviceError(errorCode(error), "available"));
  }
}

function scan(input) {
  if (arguments.length > 1) throw new TypeError("biometric.authenticate accepts at most one options object");
  var options = plainOptions(input);
  var reason = options.reason === undefined ? "Confirm your identity" : options.reason;
  if (typeof reason !== "string" || !reason || reason.length > 256 || !wellFormed(reason) || /[\x00-\x1f\x7f-\x9f]/.test(reason)) throw new TypeError("Invalid biometric reason");
  var params = { reason: reason };
  var signal = checkedSignal(options.signal);
  if (isAborted(signal)) return promiseReject(serviceError("CANCELLED", "authenticate"));
  if (!nativeHost || !setTimer || !clearTimer) {
    return promiseReject(serviceError("UNAVAILABLE", "authenticate"));
  }
  if (global.document && global.document.visibilityState === "hidden") return promiseReject(serviceError("CANCELLED", "authenticate"));
  if (activeScan) return promiseReject(serviceError("OVERLOADED", "authenticate"));

  var state = {
    operation: null,
    polls: 0,
    pollTimer: null,
    deadlineTimer: null,
    terminal: false,
    publicSettled: false,
    releasePending: null,
    beginPending: false,
    beginTimer: null,
    removeAbort: function () {},
    removeLifecycle: function () {},
    resolve: null,
    reject: null
  };
  activeScan = state;

  var result = new PromiseType(function (resolve, reject) {
    state.resolve = resolve;
    state.reject = reject;
  });

  function publish(ok, value) {
    if (state.publicSettled) return;
    state.publicSettled = true;
    if (ok) state.resolve(value);
    else state.reject(value);
  }

  function clearClocks() {
    if (state.pollTimer !== null) {
      clearTimer(state.pollTimer);
      state.pollTimer = null;
    }
    if (state.deadlineTimer !== null) {
      clearTimer(state.deadlineTimer);
      state.deadlineTimer = null;
    }
  }

  function complete() {
    clearClocks();
    state.removeLifecycle();
    state.removeAbort();
    state.removeAbort = function () {};
    if (activeScan === state) activeScan = null;
  }

  function releaseOperation() {
    if (!state.operation) return promiseResolve(undefined);
    if (state.releasePending) return state.releasePending;
    state.releasePending = new PromiseType(function (resolve) {
      var settled = false, timer = null;
      function done(error) {
        if (settled) return;
        settled = true;
        if (timer !== null) clearTimer(timer);
        if (error) report(error);
        resolve(undefined);
      }
      try {
        timer = setTimer(function () { done(serviceError("TIMEOUT", "authenticate")); }, RELEASE_TIMEOUT_MS);
      } catch (_) { done(serviceError("UNAVAILABLE", "authenticate")); }
      nativeCall("release", { operation: state.operation }, releaseResult).then(
        function () { done(); }, function (error) { done(error); }
      );
    });
    return state.releasePending;
  }

  function finish(ok, value, prompt) {
    if (state.terminal) return;
    state.terminal = true;
    clearClocks();
    state.removeAbort();
    state.removeAbort = function () {};
    if (prompt) publish(ok, value);
    if (state.beginPending && !state.operation) return;
    releaseOperation().then(function () {
      if (!prompt) publish(ok, value);
      complete();
    });
  }

  function abort() {
    finish(false, serviceError("CANCELLED", "authenticate"), true);
  }

  function schedulePoll() {
    if (state.terminal) return;
    if (state.polls >= SCAN_MAX_POLLS) {
      finish(false, serviceError("TIMEOUT", "authenticate"), true);
      return;
    }
    try {
      state.pollTimer = setTimer(function () {
        state.pollTimer = null;
        poll();
      }, POLL_DELAY_MS);
    } catch (_) {
      finish(false, serviceError("UNAVAILABLE", "authenticate"), true);
    }
  }

  function poll() {
    if (state.terminal) return;
    state.polls++;
    nativeCall("poll", { operation: state.operation }, pollResult).then(function (value) {
      if (state.terminal) return;
      if (value.status === "pending") {
        schedulePoll();
        return;
      }
      if (value.status === "cancelled") {
        finish(false, serviceError("CANCELLED", "authenticate"), false);
        return;
      }
      if (value.status === "failed") {
        finish(false, serviceError(value.code, "authenticate"), false);
        return;
      }
      finish(true, value.value, false);
    }, function (error) {
      if (!state.terminal) finish(false, error, false);
    });
  }

  state.removeAbort = listenForAbort(signal, abort);
  if (addEvent && removeEvent && global.document) {
    var hidden = function () { if (global.document.visibilityState === "hidden") abort(); };
    addEvent(global, "pagehide", abort);
    addEvent(global.document, "visibilitychange", hidden);
    state.removeLifecycle = function () {
      removeEvent(global, "pagehide", abort);
      removeEvent(global.document, "visibilitychange", hidden);
    };
  }
  if (isAborted(signal)) {
    abort();
    complete();
    return result;
  }

  state.beginPending = true;
  try {
    state.beginTimer = setTimer(function () {
      state.beginTimer = null;
      state.beginPending = false;
      if (!state.terminal) finish(false, serviceError("TIMEOUT", "authenticate"), true);
      else complete();
    }, BEGIN_TIMEOUT_MS);
  } catch (_) {
    state.beginPending = false;
    finish(false, serviceError("UNAVAILABLE", "authenticate"), true);
    return result;
  }
  nativeCall("begin", params, function (value) {
    // A malformed envelope can still own a native resource. Recover only an
    // own data-property token, never execute an accessor while cleaning it up.
    var descriptor;
    try { descriptor = value && objectGetOwnPropertyDescriptor(value, "operation"); }
    catch (_) { /* No safely recoverable resource identity. */ }
    if (descriptor && OWN.call(descriptor, "value") && typeof descriptor.value === "string" &&
        regExpTest(OPERATION, descriptor.value)) state.operation = descriptor.value;
    return beginResult(value);
  }).then(function (operation) {
    if (state.beginTimer !== null) { clearTimer(state.beginTimer); state.beginTimer = null; }
    state.beginPending = false;
    state.operation = operation;
    if (state.terminal) {
      releaseOperation().then(complete);
      return;
    }
    try {
      state.deadlineTimer = setTimer(function () {
        state.deadlineTimer = null;
        finish(false, serviceError("TIMEOUT", "authenticate"), true);
      }, SCAN_TIMEOUT_MS);
    } catch (_) {
      finish(false, serviceError("UNAVAILABLE", "authenticate"), true);
      return;
    }
    poll();
  }, function (error) {
    if (state.beginTimer !== null) { clearTimer(state.beginTimer); state.beginTimer = null; }
    state.beginPending = false;
    if (!state.terminal) finish(false, error, false);
    else releaseOperation().then(complete);
  });

  return result;
}

function status() {
  if (arguments.length) throw new TypeError("biometric.status accepts no arguments");
  return nativeCall("status", {}, function (value) {
    var d = exactObject(value, ["available", "enrolled"]);
    if (!d || typeof d.available.value !== "boolean" || typeof d.enrolled.value !== "boolean") throw new TypeError("Invalid biometric status");
    return objectFreeze({ available: d.available.value, enrolled: d.enrolled.value });
  });
}

kit.service("biometric", {
  available: available,
  status: status,
  authenticate: scan
});
})(globalThis, kit);
