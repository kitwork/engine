// ============================================================================
// Kitwork Official Headless Component: Tooltip (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/tooltip/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("tooltip", {
    visible: false,

    show: function () {
      this.visible = true;
    },

    hide: function () {
      this.visible = false;
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
