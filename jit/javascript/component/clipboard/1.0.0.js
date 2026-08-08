// ============================================================================
// Kitwork Client Runtime Component: Clipboard Copy Button (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/component/clipboard/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (!kit.component) return;

  kit.component("clipboard", {
    copied: false,
    _timer: null,

    copy: function (text, duration) {
      text = String(text || "");
      if (!text) return;

      duration = typeof duration === "number" ? duration : 2000;
      var self = this;

      if (kit.clipboard && kit.clipboard.copy) {
        return kit.clipboard.copy(text).then(function () {
          self.copied = true;
          if (self._timer) clearTimeout(self._timer);
          self._timer = setTimeout(function () {
            self.copied = false;
          }, duration);
          return true;
        });
      }
      return Promise.reject("kit.clipboard service unavailable");
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
