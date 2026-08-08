// ============================================================================
// Kitwork Client Runtime Component: Theme (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/component/theme/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (!kit.component) return;

  kit.component("theme", {
    // 1. Getter & Setter cho `mode` - Cho phép gán trực tiếp: mode = 'dark'
    get mode() {
      return kit.theme ? kit.theme.mode : "system";
    },

    set mode(m) {
      if (kit.theme) kit.theme.set(m);
    },

    // 2. Read-only Getters
    get resolved() {
      return kit.theme ? kit.theme.resolved : "light";
    },

    get isDark() {
      return this.resolved === "dark";
    },

    get isLight() {
      return this.resolved === "light";
    },

    // 3. Phương thức chuyển đổi
    toggle: function () {
      if (kit.theme) return kit.theme.toggle();
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
