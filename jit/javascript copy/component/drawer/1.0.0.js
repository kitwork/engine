// ============================================================================
// Kitwork Component: Drawer / Sidebar (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/drawer/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("drawer", {
    open: false,
    title: "Danh mục quản trị",

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
