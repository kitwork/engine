// KitJS service: fullscreen@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:fullscreen");
  }
  if (OWN.call(kit, "fullscreen")) {
    if (kit.fullscreen.version === version) return;
    throw new Error("KitJS service conflict: fullscreen");
  }

  function request(element, options) {
    var target = element || global.document && global.document.documentElement;
    if (!target || typeof target.requestFullscreen !== "function") {
      return Promise.reject(new Error("Fullscreen API is unavailable"));
    }
    try { return Promise.resolve(target.requestFullscreen(options)); }
    catch (error) { return Promise.reject(error); }
  }

  function exit() {
    var document = global.document;
    if (!document || typeof document.exitFullscreen !== "function") {
      return Promise.reject(new Error("Fullscreen API is unavailable"));
    }
    try { return Promise.resolve(document.exitFullscreen()); }
    catch (error) { return Promise.reject(error); }
  }

  function active() {
    return Boolean(global.document && global.document.fullscreenElement);
  }

  var service = { request: request, exit: exit, active: active };
  Object.defineProperty(service, "version", { value: version, enumerable: false });
  Object.freeze(service);
  Object.defineProperty(kit, "fullscreen", {
    value: service,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
