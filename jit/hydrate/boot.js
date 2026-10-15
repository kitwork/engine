// Final composition step. All modules are registered before the kernel starts.
//
// The kernel can arrive two ways: composed into one script with every module before this file
// (the browser test harnesses), or as the first of several deferred files — kernel, Drive, then one
// file per component (/_kitwork/…, every real page). The second way is why this waits for DOMContentLoaded rather than for the parser to
// finish: deferred scripts run AFTER parsing (readyState is already "interactive") and BEFORE
// DOMContentLoaded, in document order. Starting at "interactive" would start the kernel before the
// component files that follow it had registered, and every component on the page would first be
// reported missing.
//
// Whether DOMContentLoaded has already happened cannot be read from readyState — it stays
// "interactive" on both sides of the event. The navigation timing entry can say it:
// domContentLoadedEventStart is 0 until the event begins. A kernel injected after that (by a script,
// long after load) starts at once; otherwise it waits for the event, with `load` as a fallback for a
// browser without the timing entry. kit.start is idempotent, so both firing is harmless.
(function (window, document) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || typeof kit.start !== "function") return;

  function contentLoaded() {
    if (document.readyState === "loading") return false;
    var performance = window.performance;
    var entry = performance && typeof performance.getEntriesByType === "function"
      ? performance.getEntriesByType("navigation")[0] : null;
    if (entry && typeof entry.domContentLoadedEventStart === "number") return entry.domContentLoadedEventStart > 0;
    return document.readyState === "complete";
  }

  if (contentLoaded()) {
    kit.start();
  } else {
    kit.internal.listen(document, "DOMContentLoaded", kit.start, { once: true });
    kit.internal.listen(window, "load", kit.start, { once: true });
  }
})(window, document);
