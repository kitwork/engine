// ============================================================================
// Kitwork Official Headless Component: Popover (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/popover/1.0.0.js
// ============================================================================
// Pure Behavior & Accessibility Contract for Popover Panels:
// - Keyboard focus management.
// - Click-outside dismiss behavior (`:outside`).
// - Dynamic position toggle & ARIA expanded state.
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("popover", {
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
