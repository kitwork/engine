// Final composition step. All modules are registered before the kernel starts.
(function (window, document) {
  "use strict";

  var kitwork = window.kitwork;
  if (!kitwork || typeof kitwork.start !== "function") return;

  if (document.readyState === "loading") {
    kitwork.internal.listen(document, "DOMContentLoaded", kitwork.start, { once: true });
  } else {
    kitwork.start();
  }
})(window, document);
