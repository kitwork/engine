// ============================================================================
// Kitwork Official Headless Component: Combobox (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/combobox/1.0.0.js
// ============================================================================
// Headless Auto-complete Search Input với Keyboard Filtering & ARIA Combobox Role.
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("combobox", {
    query: "",
    open: false,
    selected: null,

    onInput: function (val) {
      this.query = val;
      this.open = true;
    },

    select: function (item) {
      this.selected = item;
      this.query = typeof item === "string" ? item : (item ? item.name : "");
      this.open = false;
    },

    close: function () {
      this.open = false;
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
