; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var GRAPH = Symbol.for("kitjs:graph");
  var core = document[ASSEMBLY];
  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: boot loaded out of order");
  }
  var expectedProfile = core.phase === "drive" ? "hydrate" : "kit";
  if (core.profile !== expectedProfile) {
    delete document[ASSEMBLY];
    throw new Error("KitJS: runtime profile marker is unavailable");
  }
  if (core.reuse) {
    if (global.kit && Object.prototype.hasOwnProperty.call(global.kit, GRAPH) &&
      core.graphValidated !== true) {
      delete document[ASSEMBLY];
      throw new Error("KitJS: installed component graph does not match this artifact");
    }
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
    if (typeof core.prepareStructureTree === "function") core.prepareStructureTree(document);
    if (typeof core.prepareComponentTree === "function") core.prepareComponentTree(document);
    core.booting = true;
    core.render();
    core.resetDirty();
    core.booting = false;
    // The boot render discards the invalidations it caused itself — except a state change made by
    // an error boundary handling a failure of that very render, which must paint.
    if (core.boundaryWrote) {
      core.boundaryWrote = false;
      core.invalidate();
    }
  }

  delete document[ASSEMBLY];
  core.installEvents();
  global.kit = kit;
  core.startHooks.forEach(function (start) {
    try { start(); } catch (error) { core.report(error); }
  });
  var script = document.currentScript;
  var waitingForDeferredPeers = document.readyState === "interactive" && script &&
    script.defer === true && script.async !== true;
  if (document.readyState === "loading" || waitingForDeferredPeers) {
    document.addEventListener("DOMContentLoaded", boot, { once: true, capture: true });
  } else queueMicrotask(boot);
})(globalThis, document);
