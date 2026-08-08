// ============================================================================
// Kitwork Component: Dropdown Menu (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/dropdown/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("dropdown", {
    open: false,

    toggle: function () {
      this.open = !this.open;
    },

    show: function () {
      this.open = true;
    },

    close: function () {
      this.open = false;
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
