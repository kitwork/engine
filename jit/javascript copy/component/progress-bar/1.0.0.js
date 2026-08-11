// ============================================================================
// Kitwork Client Runtime Component: ProgressBar (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/component/progressbar/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (!kit.component) return;

  kit.component("progress-bar", {
    get value() {
      return kit.progress ? kit.progress.value : 0;
    },

    get status() {
      return kit.progress ? kit.progress.status : "idle";
    },

    get hidden() {
      return this.status === "idle";
    },

    get width() {
      return this.value + "%";
    },

    start: function () {
      if (kit.progress) kit.progress.start();
    },

    set: function (val) {
      if (kit.progress) kit.progress.set(val);
    },

    inc: function (amount) {
      if (kit.progress) kit.progress.inc(amount);
    },

    done: function () {
      if (kit.progress) kit.progress.done();
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
