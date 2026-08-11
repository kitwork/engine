// ============================================================================
// Kitwork Official Headless Component: Accordion (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/accordion/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("accordion", {
    activeItem: null,

    toggle: function (itemId) {
      if (this.activeItem === itemId) {
        this.activeItem = null;
      } else {
        this.activeItem = itemId;
      }
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
