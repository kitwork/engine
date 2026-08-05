// Compatibility surface for existing jit/js verb modules.
(function (window) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("compat")) return;

  var components = kit.components;
  Object.defineProperty(components, "action", {
    value: function (name, handler) {
      kit.action(name, handler);
      return this;
    },
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "actions", {
    value: kit.actions,
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "target", {
    value: kit.target,
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "state", {
    value: kit.state,
    configurable: true,
    writable: true,
    enumerable: false
  });
  Object.defineProperty(components, "fire", {
    value: kit.fire,
    configurable: true,
    writable: true,
    enumerable: false
  });

  window.hydrate = kitwork;
  kit.module("compat", components);
})(window);
