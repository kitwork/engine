; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var core = document[ASSEMBLY];
  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: boot loaded out of order");
  }
  if (core.reuse) {
    delete document[ASSEMBLY];
    return;
  }
  if (typeof core.component !== "function" || typeof core.render !== "function" ||
    typeof core.installEvents !== "function" || typeof core.sealKit !== "function" || !core.kit) {
    delete document[ASSEMBLY];
    throw new Error("KitJS: incomplete runtime assembly");
  }
  try {
    if (typeof core.assertComponentGraph === "function") core.assertComponentGraph();
    if (core.serviceRegistry && core.servicesSealed !== true) {
      throw new Error("KitJS: services must be sealed before publication");
    }
    core.sealKit();
  } catch (error) {
    delete document[ASSEMBLY];
    throw error;
  }

  var kit = core.kit;

  function boot() {
    if (core.booted) return;
    core.booted = true;
    if (typeof core.prepareComponentTree === "function") core.prepareComponentTree(document);
    core.render();
    core.resetDirty();
  }

  delete document[ASSEMBLY];
  core.installEvents();
  global.kit = kit;
  core.startHooks.forEach(function (start) {
    try { start(); } catch (error) { core.report(error); }
  });
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot, { once: true });
  } else queueMicrotask(boot);
})(globalThis, document);
