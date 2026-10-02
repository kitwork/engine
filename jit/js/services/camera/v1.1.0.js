;(function (global, kit) {
"use strict";

// KitJS staged service: camera@1.1.0
// Requires capabilities@1.0.0 and files@1.3.0.
var OWN = Object.prototype.hasOwnProperty;
var OPTIONS = Object.freeze({ facing: true, signal: true });
var OPERATION = /^[A-Za-z0-9_-]{32}$/;
var POLL_DELAY_MS = 250;
var CAPTURE_TIMEOUT_MS = 10 * 60 * 1000;
var CAPTURE_MAX_POLLS = 2400;
var FILE_ADOPTER = Symbol.for("kitjs:files:adopt:v1");
var objectFreeze = Object.freeze;
var objectDefineProperties = Object.defineProperties;
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
var adopterDescriptor = assembly && objectGetOwnPropertyDescriptor(assembly, FILE_ADOPTER);
var adoptFile = adopterDescriptor && adopterDescriptor.value;
var activeCapture = null;

if (!assembly || !adopterDescriptor || adopterDescriptor.get || adopterDescriptor.set ||
  typeof adoptFile !== "function" || adopterDescriptor.configurable !== true ||
  typeof supportsCapability !== "function") {
  throw new Error("KitJS: camera requires its exact capabilities and files boundaries");
}
if (!delete assembly[FILE_ADOPTER] || OWN.call(assembly, FILE_ADOPTER)) {
  throw new Error("KitJS: camera could not close its file adoption boundary");
}

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* Cleanup diagnostics cannot change capture settlement. */ }
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

function cameraError(code, operation) {
  var messages = {
    DENIED: "Camera permission was denied",
    CANCELLED: "Camera capture was cancelled",
    UNAVAILABLE: "Camera capture is unavailable",
    TIMEOUT: "Camera capture timed out",
    OVERLOADED: "Camera capture is busy",
    TOO_LARGE: "Captured photo is too large",
    FAILED: "Camera capture failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitCameraError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return objectFreeze(error);
}

function nativeCall(operation, params, validator) {
  if (!nativeHost) return promiseReject(cameraError("UNAVAILABLE", "capture"));
  try {
    return promiseResolve(nativeHost.call("camera." + operation, params)).then(
      function (value) {
        try { return validator(value); }
        catch (_) { throw cameraError("FAILED", "capture"); }
      },
      function (error) { throw cameraError(errorCode(error), "capture"); }
    );
  } catch (error) {
    return promiseReject(cameraError(errorCode(error), "capture"));
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
    throw new TypeError("invalid camera operation");
  }
  return value;
}

function beginResult(value) {
  var descriptors = exactObject(value, ["operation"]);
  if (!descriptors) throw new TypeError("invalid camera begin result");
  return safeOperation(descriptors.operation.value);
}

function pollResult(value) {
  var descriptors = exactObject(value, ["status"]);
  if (descriptors) {
    var status = descriptors.status.value;
    if (status === "pending" || status === "cancelled") {
      return objectFreeze({ status: status });
    }
    throw new TypeError("invalid camera status");
  }
  descriptors = exactObject(value, ["status", "code"]);
  if (descriptors && descriptors.status.value === "failed") {
    var code = descriptors.code.value;
    if (code === "DENIED" || code === "UNAVAILABLE" || code === "TOO_LARGE" || code === "FAILED") {
      return objectFreeze({ status: "failed", code: code });
    }
    throw new TypeError("invalid camera failure code");
  }
  descriptors = exactObject(value, ["status", "file"]);
  if (!descriptors || descriptors.status.value !== "ready") {
    throw new TypeError("invalid camera poll result");
  }
  return objectFreeze({ status: "ready", file: descriptors.file.value });
}

function releaseResult(value) {
  if (typeof value !== "boolean") throw new TypeError("invalid camera release result");
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
    throw new TypeError("Camera options must be a plain object");
  }
  if (!value || prototype !== Object.prototype && prototype !== null || symbols.length) {
    throw new TypeError("Camera options must be a plain object");
  }
  var output = Object.create(null);
  objectKeys(descriptors).forEach(function (name) {
    if (!OWN.call(OPTIONS, name)) throw new TypeError("Unknown camera option: " + name);
    var descriptor = descriptors[name];
    if (!OWN.call(descriptor, "value") || descriptor.get || descriptor.set) {
      throw new TypeError("Camera options must not contain accessors");
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
    catch (_) { /* A settled capture no longer depends on listener cleanup. */ }
  };
}

function available() {
  if (arguments.length !== 0) throw new TypeError("camera.available does not accept parameters");
  if (!nativeHost) return promiseResolve(false);
  try {
    return promiseResolve(supportsCapability.call(capabilities, "camera.capture")).then(
      function (value) {
        if (typeof value !== "boolean") throw cameraError("FAILED", "available");
        return value;
      },
      function (error) { throw cameraError(errorCode(error), "available"); }
    );
  } catch (error) {
    return promiseReject(cameraError(errorCode(error), "available"));
  }
}

function capture(input) {
  if (arguments.length > 1) throw new TypeError("camera.capture accepts at most one options object");
  var options = plainOptions(input);
  var facing = options.facing === undefined ? "environment" : options.facing;
  if (facing !== "user" && facing !== "environment") {
    throw new TypeError("Camera facing must be user or environment");
  }
  var signal = checkedSignal(options.signal);
  if (isAborted(signal)) return promiseReject(cameraError("CANCELLED", "capture"));
  if (!nativeHost || !setTimer || !clearTimer) {
    return promiseReject(cameraError("UNAVAILABLE", "capture"));
  }
  if (activeCapture) return promiseReject(cameraError("OVERLOADED", "capture"));

  var state = {
    operation: null,
    polls: 0,
    pollTimer: null,
    deadlineTimer: null,
    terminal: false,
    publicSettled: false,
    releasePending: null,
    beginPending: false,
    removeAbort: function () {},
    resolve: null,
    reject: null
  };
  activeCapture = state;

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
    state.removeAbort();
    state.removeAbort = function () {};
    if (activeCapture === state) activeCapture = null;
  }

  function releaseOperation() {
    if (!state.operation) return promiseResolve(undefined);
    if (state.releasePending) return state.releasePending;
    state.releasePending = nativeCall("releaseCapture", { operation: state.operation }, releaseResult).then(
      function () { return undefined; },
      function (error) { report(error); return undefined; }
    );
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
    finish(false, cameraError("CANCELLED", "capture"), true);
  }

  function schedulePoll() {
    if (state.terminal) return;
    if (state.polls >= CAPTURE_MAX_POLLS) {
      finish(false, cameraError("TIMEOUT", "capture"), true);
      return;
    }
    try {
      state.pollTimer = setTimer(function () {
        state.pollTimer = null;
        poll();
      }, POLL_DELAY_MS);
    } catch (_) {
      finish(false, cameraError("UNAVAILABLE", "capture"), true);
    }
  }

  function poll() {
    if (state.terminal) return;
    state.polls++;
    nativeCall("pollCapture", { operation: state.operation }, pollResult).then(function (value) {
      if (state.terminal) return;
      if (value.status === "pending") {
        schedulePoll();
        return;
      }
      if (value.status === "cancelled") {
        finish(true, null, false);
        return;
      }
      if (value.status === "failed") {
        finish(false, cameraError(value.code, "capture"), false);
        return;
      }
      var reference;
      try { reference = adoptFile(value.file); }
      catch (_) {
        finish(false, cameraError("FAILED", "capture"), false);
        return;
      }
      finish(true, reference, false);
    }, function (error) {
      if (!state.terminal) finish(false, error, false);
    });
  }

  state.removeAbort = listenForAbort(signal, abort);
  if (isAborted(signal)) {
    abort();
    complete();
    return result;
  }

  state.beginPending = true;
  nativeCall("beginCapture", { facing: facing }, beginResult).then(function (operation) {
    state.beginPending = false;
    state.operation = operation;
    if (state.terminal) {
      releaseOperation().then(complete);
      return;
    }
    try {
      state.deadlineTimer = setTimer(function () {
        state.deadlineTimer = null;
        finish(false, cameraError("TIMEOUT", "capture"), true);
      }, CAPTURE_TIMEOUT_MS);
    } catch (_) {
      finish(false, cameraError("UNAVAILABLE", "capture"), true);
      return;
    }
    poll();
  }, function (error) {
    state.beginPending = false;
    if (!state.terminal) publish(false, error);
    complete();
  });

  return result;
}

kit.service("camera", {
  available: available,
  capture: capture
});
})(globalThis, kit);
