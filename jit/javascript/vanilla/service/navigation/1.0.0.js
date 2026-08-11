;(function (global, kit) {
"use strict";

// KitJS service: navigation@1.0.0
function failure(code, operation) {
  var error = new Error("Navigation " + operation + " failed");
  Object.defineProperties(error, {
    name: { value: "KitNavigationError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return Object.freeze(error);
}

function invoke(owner, member, operation) {
  var method = owner && owner[member];
  if (typeof method !== "function") {
    throw failure("UNAVAILABLE", operation);
  }
  try {
    method.call(owner);
  } catch (_) {
    throw failure("FAILED", operation);
  }
}

function back() {
  invoke(global.history, "back", "back");
}

function forward() {
  invoke(global.history, "forward", "forward");
}

function reload() {
  invoke(global.location, "reload", "reload");
}

kit.service("navigation", {
  back: back,
  forward: forward,
  reload: reload
});
})(globalThis, kit);
