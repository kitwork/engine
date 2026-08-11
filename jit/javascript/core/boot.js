// KitJS auto boot. Compose this file after core fragments, optional modules,
// services and components.
(function (window, document) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!kit || typeof kit.component !== "function" || !core) {
    throw new Error("KitJS core must be loaded before core/boot.js");
  }
  if (core.reuse) {
    delete kit.__kitwork_core__;
    return;
  }
  if (core.phase !== "lifecycle") {
    throw new Error("KitJS core fragments are incomplete before core/boot.js");
  }
  var startRuntime = core.startRuntime;
  if (typeof startRuntime !== "function") {
    throw new Error("KitJS private lifecycle is incomplete before core/boot.js");
  }

  // Every optional module has already captured the private hooks it needs.
  // Do not leave an internal control surface on the public KitJS namespace.
  delete kit.__kitwork_core__;

  function start() { startRuntime(document.documentElement); }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else start();

})(window, document);
