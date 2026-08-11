// KitJS service: navigation@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:navigation");
  }
  if (OWN.call(kit, "navigation")) {
    if (kit.navigation.version === version) return;
    throw new Error("KitJS service conflict: navigation");
  }

  function back() {
    if (!global.history || typeof global.history.back !== "function") throw new Error("History API is unavailable");
    global.history.back();
  }

  function forward() {
    if (!global.history || typeof global.history.forward !== "function") throw new Error("History API is unavailable");
    global.history.forward();
  }

  function reload() {
    if (!global.location || typeof global.location.reload !== "function") throw new Error("Location API is unavailable");
    global.location.reload();
  }

  var service = { back: back, forward: forward, reload: reload };
  Object.defineProperty(service, "version", { value: version, enumerable: false });
  Object.freeze(service);
  Object.defineProperty(kit, "navigation", {
    value: service,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
