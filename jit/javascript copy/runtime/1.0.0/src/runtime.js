"use strict";

var core = require("./core/runtime.js");

function install(globalObject, options) {
  var global = globalObject || (typeof window !== "undefined" ? window : globalThis);
  var kit = global.kit = global.kit || {};
  if (kit.internal && kit.internal.runtime && kit.internal.runtime.options) return kit.internal.runtime;
  return core.createRuntime(global, options || global.KITWORK_RUNTIME_OPTIONS || {});
}

module.exports = {
  createRuntime: core.createRuntime,
  install: install
};

if (typeof window !== "undefined" && window.document) install(window);
