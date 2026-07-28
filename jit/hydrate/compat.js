// Compatibility surface for existing jit/js verb modules.
(function (window) {
  "use strict";

  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("compat")) return;

  var components = kitwork.components;
  Object.defineProperty(components, "action", {
    value: function (name, handler) {
      kitwork.action(name, handler);
      return this;
    },
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "actions", {
    value: kitwork.actions,
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "target", {
    value: kitwork.target,
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "state", {
    value: kitwork.state,
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "fire", {
    value: kitwork.fire,
    configurable: true,
    writable: true,
    enumerable: false
  });

  window.hydrate = kitwork;
  kitwork.module("compat", components);
})(window);
