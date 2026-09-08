;(function (global, document, kit) {
"use strict";

// KitJS service: wakeLock@1.0.0
var PromiseType = global.Promise;
var SetType = global.Set;
var call = Function.prototype.call;
var promiseReject = call.bind(PromiseType.reject, PromiseType);
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var assembly = document && document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var owners = new SetType();
var activeLock = null;
var acquiring = null;
var releasing = null;
var pageHidden = false;

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") {
      global.console.error(error);
    }
  } catch (_) { /* Best-effort lifecycle cleanup must stay best effort. */ }
}

function errorCode(value) {
  var code = "";
  var name = "";
  try {
    code = value && typeof value.code === "string" ? value.code : "";
    name = value && typeof value.name === "string" ? value.name : "";
  } catch (_) { /* Raw adapter and browser errors never cross this boundary. */ }
  if (code === "BRIDGE_BUSY") return "OVERLOADED";
  if (code === "BRIDGE_TIMEOUT") return "TIMEOUT";
  if (code === "BRIDGE_UNAVAILABLE") return "UNAVAILABLE";
  if (code === "DENIED" || name === "NotAllowedError" || name === "SecurityError") return "DENIED";
  if (code === "CANCELLED" || name === "AbortError") return "CANCELLED";
  if (code === "UNAVAILABLE" || code === "UNSUPPORTED" ||
    name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
  if (code === "TIMEOUT" || code === "OVERLOADED" || name === "QuotaExceededError") return code || "OVERLOADED";
  return "FAILED";
}

function wakeLockError(code, operation) {
  var messages = {
    UNAVAILABLE: "Screen wake lock is unavailable",
    DENIED: "Screen wake lock permission was denied",
    CANCELLED: "Screen wake lock request was cancelled",
    TIMEOUT: "Screen wake lock operation timed out",
    OVERLOADED: "Screen wake lock is busy",
    FAILED: "Screen wake lock operation failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitWakeLockError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function normalizedError(value, operation) {
  return wakeLockError(errorCode(value), operation);
}

function listen(target, type, listener, options) {
  var method;
  try { method = target && target.addEventListener; }
  catch (_) { return false; }
  if (typeof method !== "function") return false;
  try {
    method.call(target, type, listener, options);
    return true;
  } catch (_) { return false; }
}

function unlisten(target, type, listener) {
  var method;
  try { method = target && target.removeEventListener; }
  catch (_) { return; }
  if (typeof method !== "function") return;
  try { method.call(target, type, listener); }
  catch (_) { /* A stale callback is guarded by lock identity too. */ }
}

function documentHidden() {
  try {
    if (document.visibilityState === "hidden") return true;
    return document.visibilityState === undefined && document.hidden === true;
  } catch (_) { return false; }
}

function unavailableRequest() {
  return wakeLockError(pageHidden ? "CANCELLED" : "DENIED", "request");
}

function settleRecord(record, ok, value) {
  if (record.status !== "pending") return;
  record.operation = null;
  if (ok) {
    record.status = "active";
    record.resolve(record.sentinel);
  } else {
    record.status = "released";
    owners.delete(record);
    record.reject(value);
  }
  record.resolve = null;
  record.reject = null;
}

function releaseRecordState(record, pendingError) {
  if (record.status === "released") return;
  if (record.status === "pending") {
    settleRecord(record, false, pendingError || wakeLockError("CANCELLED", "request"));
    return;
  }
  record.status = "released";
  owners.delete(record);
}

function hasPending(operation) {
  var found = false;
  operation.records.forEach(function (record) {
    if (record.operation === operation && record.status === "pending") found = true;
  });
  return found;
}

function settleOperation(operation, ok, value) {
  operation.records.forEach(function (record) {
    if (record.operation === operation) settleRecord(record, ok, value);
  });
}

function nativeAcquire() {
  try {
    return promiseResolve(nativeHost.call("screen.keepAwake", { enabled: true })).then(
      function (value) {
        if (value !== true) throw wakeLockError("FAILED", "request");
        return { kind: "native" };
      },
      function (error) { throw normalizedError(error, "request"); }
    );
  } catch (error) {
    return promiseReject(normalizedError(error, "request"));
  }
}

function browserAcquire() {
  var navigator;
  var wakeLock;
  var requestMethod;
  try {
    navigator = global.navigator;
    wakeLock = navigator && navigator.wakeLock;
    requestMethod = wakeLock && wakeLock.request;
  } catch (error) {
    return promiseReject(normalizedError(error, "request"));
  }
  if (!wakeLock || typeof requestMethod !== "function") {
    return promiseReject(wakeLockError("UNAVAILABLE", "request"));
  }
  try {
    return promiseResolve(requestMethod.call(wakeLock, "screen")).then(function (sentinel) {
      var released;
      var releaseMethod;
      try {
        released = sentinel && sentinel.released;
        releaseMethod = sentinel && sentinel.release;
      } catch (error) {
        throw normalizedError(error, "request");
      }
      if (!sentinel || typeof released !== "boolean" || typeof releaseMethod !== "function") {
        throw wakeLockError("FAILED", "request");
      }
      if (released) throw wakeLockError("CANCELLED", "request");
      return {
        kind: "browser",
        sentinel: sentinel,
        releaseMethod: releaseMethod,
        releaseListener: null,
        listening: false
      };
    }, function (error) {
      throw normalizedError(error, "request");
    });
  } catch (error) {
    return promiseReject(normalizedError(error, "request"));
  }
}

function acquirePlatform() {
  // Host presence fixes the authority boundary for the whole service lifetime.
  // A rejection from that boundary must never turn into a browser permission bypass.
  return nativeHost ? nativeAcquire() : browserAcquire();
}

function browserReleased(platform) {
  if (activeLock !== platform) return;
  if (platform.listening) {
    platform.listening = false;
    unlisten(platform.sentinel, "release", platform.releaseListener);
  }
  activeLock = null;
  var cancelled = wakeLockError("CANCELLED", "request");
  Array.from(owners).forEach(function (record) {
    releaseRecordState(record, cancelled);
  });
  pump();
}

function attachBrowserRelease(platform) {
  platform.releaseListener = function () { browserReleased(platform); };
  if (!listen(platform.sentinel, "release", platform.releaseListener)) return false;
  platform.listening = true;
  return true;
}

function detachBrowserRelease(platform) {
  if (platform.kind !== "browser" || !platform.listening) return;
  platform.listening = false;
  unlisten(platform.sentinel, "release", platform.releaseListener);
}

function browserAlreadyReleased(platform) {
  try { return platform.sentinel.released === true; }
  catch (_) { return false; }
}

function releasePlatform(platform) {
  if (platform.kind === "native") {
    try {
      return promiseResolve(nativeHost.call("screen.keepAwake", { enabled: false })).then(
        function (value) {
          if (value !== true) throw wakeLockError("FAILED", "release");
          return undefined;
        },
        function (error) { throw normalizedError(error, "release"); }
      );
    } catch (error) {
      return promiseReject(normalizedError(error, "release"));
    }
  }
  if (browserAlreadyReleased(platform)) return promiseResolve(undefined);
  try {
    return promiseResolve(platform.releaseMethod.call(platform.sentinel)).then(
      function () { return undefined; },
      function (error) { throw normalizedError(error, "release"); }
    );
  } catch (error) {
    return promiseReject(normalizedError(error, "release"));
  }
}

function completeRelease(operation) {
  if (releasing === operation) releasing = null;
  pump();
}

function beginRelease() {
  if (!activeLock) {
    return releasing ? releasing.promise : promiseResolve(undefined);
  }
  var platform = activeLock;
  activeLock = null;
  detachBrowserRelease(platform);
  var operation = { platform: platform, promise: null };
  releasing = operation;
  operation.promise = releasePlatform(platform).then(function () {
    completeRelease(operation);
    return undefined;
  }, function (error) {
    completeRelease(operation);
    throw normalizedError(error, "release");
  });
  return operation.promise;
}

function finishDiscard(operation, error) {
  if (error) report(normalizedError(error, "release"));
  if (acquiring === operation) acquiring = null;
  pump();
}

function discardAcquired(operation, platform, releaseNeeded) {
  detachBrowserRelease(platform);
  if (!releaseNeeded) {
    finishDiscard(operation, null);
    return;
  }
  releasePlatform(platform).then(
    function () { finishDiscard(operation, null); },
    function (error) { finishDiscard(operation, error); }
  );
}

function acquired(operation, platform) {
  if (acquiring !== operation) {
    discardAcquired(operation, platform, platform.kind === "native" || !browserAlreadyReleased(platform));
    return;
  }
  if (operation.cancelled || documentHidden() || pageHidden || !hasPending(operation)) {
    if (!operation.cancelled) {
      operation.cancelled = true;
      settleOperation(operation, false, unavailableRequest());
    }
    discardAcquired(operation, platform, platform.kind === "native" || !browserAlreadyReleased(platform));
    return;
  }
  if (platform.kind === "browser") {
    if (!attachBrowserRelease(platform)) {
      settleOperation(operation, false, wakeLockError("FAILED", "request"));
      discardAcquired(operation, platform, true);
      return;
    }
    if (browserAlreadyReleased(platform)) {
      detachBrowserRelease(platform);
      settleOperation(operation, false, wakeLockError("CANCELLED", "request"));
      discardAcquired(operation, platform, false);
      return;
    }
  }
  activeLock = platform;
  acquiring = null;
  settleOperation(operation, true, null);
  pump();
}

function acquireFailed(operation, error) {
  if (acquiring !== operation) return;
  acquiring = null;
  if (!operation.cancelled) {
    settleOperation(operation, false, normalizedError(error, "request"));
  }
  pump();
}

function startAcquire() {
  var operation = { cancelled: false, records: new SetType() };
  owners.forEach(function (record) {
    if (record.status === "pending" && record.operation === null) {
      record.operation = operation;
      operation.records.add(record);
    }
  });
  if (!operation.records.size) return;
  acquiring = operation;
  acquirePlatform().then(
    function (platform) { acquired(operation, platform); },
    function (error) { acquireFailed(operation, error); }
  );
}

function pump() {
  if (activeLock) {
    owners.forEach(function (record) {
      if (record.status === "pending") settleRecord(record, true, null);
    });
    return;
  }
  if (acquiring) {
    if (!acquiring.cancelled) {
      owners.forEach(function (record) {
        if (record.status === "pending" && record.operation === null) {
          record.operation = acquiring;
          acquiring.records.add(record);
        }
      });
    }
    return;
  }
  if (releasing) return;
  startAcquire();
}

function releaseOwner(record) {
  if (record.releasePromise) return record.releasePromise;
  if (record.status === "released") return promiseResolve(undefined);
  releaseRecordState(record, null);
  if (owners.size || !activeLock) {
    record.releasePromise = promiseResolve(undefined);
    return record.releasePromise;
  }
  record.releasePromise = beginRelease();
  return record.releasePromise;
}

function createRequest() {
  var record = {
    status: "pending",
    operation: null,
    sentinel: null,
    resolve: null,
    reject: null,
    releasePromise: null
  };
  var promise = new PromiseType(function (resolve, reject) {
    record.resolve = resolve;
    record.reject = reject;
  });
  var sentinel = Object.create(null);
  Object.defineProperties(sentinel, {
    released: {
      enumerable: true,
      get: function () { return record.status === "released"; }
    },
    release: {
      enumerable: true,
      value: function () {
        if (arguments.length !== 0) throw new TypeError("Wake lock release does not accept parameters");
        return releaseOwner(record);
      }
    }
  });
  record.sentinel = Object.freeze(sentinel);
  owners.add(record);
  pump();
  return promise;
}

function request(type) {
  if (arguments.length !== 1 || type !== "screen") {
    throw new TypeError("wakeLock.request expects the exact type screen");
  }
  if (pageHidden || documentHidden()) return promiseReject(unavailableRequest());
  return createRequest();
}

function releaseAll() {
  var cancelled = wakeLockError("CANCELLED", "request");
  if (acquiring) acquiring.cancelled = true;
  Array.from(owners).forEach(function (record) {
    releaseRecordState(record, cancelled);
  });
  if (activeLock) beginRelease().catch(report);
}

function visibilityChange() {
  if (documentHidden()) releaseAll();
}

function pageHide() {
  pageHidden = true;
  releaseAll();
}

function pageShow() {
  pageHidden = false;
}

kit.service("wakeLock", { request: request });
listen(document, "visibilitychange", visibilityChange);
listen(global, "pagehide", pageHide);
listen(global, "pageshow", pageShow);
})(globalThis, document, kit);
