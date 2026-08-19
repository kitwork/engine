;(function (global, document, kit) {
"use strict";

// KitJS service: fullscreen@1.0.0
function failure(code, operation) {
  var error = new Error("Fullscreen " + operation + " failed");
  Object.defineProperties(error, {
    name: { value: "KitFullscreenError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function targetOf(value) {
  var target = value === undefined ? document.documentElement : value;
  if (!target || typeof global.Element !== "function" || !(target instanceof global.Element) ||
    target.ownerDocument !== document) {
    throw new TypeError("Fullscreen target must be an Element in the current document");
  }
  return target;
}

async function request(target) {
  target = targetOf(target);
  var method;
  try { method = target.requestFullscreen; }
  catch (_) { throw failure("REQUEST_FAILED", "request"); }
  if (typeof method !== "function" || document.fullscreenEnabled === false) {
    throw failure("UNAVAILABLE", "request");
  }
  try {
    await method.call(target);
    return true;
  } catch (_) {
    throw failure("REQUEST_FAILED", "request");
  }
}

async function exit() {
  if (!document.fullscreenElement) return false;
  var method;
  try { method = document.exitFullscreen; }
  catch (_) { throw failure("EXIT_FAILED", "exit"); }
  if (typeof method !== "function") {
    throw failure("UNAVAILABLE", "exit");
  }
  try {
    await method.call(document);
    return true;
  } catch (_) {
    throw failure("EXIT_FAILED", "exit");
  }
}

function active() {
  return !!document.fullscreenElement;
}

kit.service("fullscreen", {
  request: request,
  exit: exit,
  active: active
});
})(globalThis, document, kit);
