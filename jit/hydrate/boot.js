// Final composition step. All modules are registered before the kernel starts.
(function (window, document) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || typeof kit.start !== "function") return;

  if (document.readyState === "loading") {
    kit.internal.listen(document, "DOMContentLoaded", kit.start, { once: true });
  } else {
    kit.start();
  }
})(window, document);
