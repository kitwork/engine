// ============================================================================
// Kitwork Official Headless Component: Announce Region (1.0.0 - Draft 0.5)
// ============================================================================
// Location: engine/jit/javascript/component/announce/1.0.0.js
// ============================================================================
// Screen Reader Live Region Component (aria-live="polite" / "assertive")
// Trình thông báo bằng giọng đọc hỗ trợ người khuyết tật (Accessibility A11y).
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("announce", {
    message: "",
    mode: "polite", // "polite" hoặc "assertive"

    speak: function (msg, liveMode) {
      this.message = msg || "";
      this.mode = liveMode || "polite";
    },

    clear: function () {
      this.message = "";
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
