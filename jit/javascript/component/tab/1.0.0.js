// ============================================================================
// Kitwork Client Runtime Component: Tab Switcher (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/component/tab/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (!kit.component) return;

  kit.component("tab", {
    // Tên tab đang được chọn (Mặc định là 'tab1')
    active: "tab1",

    select: function (name) {
      this.active = String(name || "");
    },

    is: function (name) {
      return this.active === String(name || "");
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
