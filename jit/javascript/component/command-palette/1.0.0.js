// ============================================================================
// Kitwork Official Headless Component: Command Palette (1.0.0 - Draft 0.5)
// ============================================================================
// Location: engine/jit/javascript/component/command-palette/1.0.0.js
// ============================================================================
// Headless Command Palette Modal với Cmd+K / Ctrl+K Global Shortcut keydown!
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("command-palette", {
    open: false,
    query: "",

    toggle: function () {
      this.open = !this.open;
      if (!this.open) this.query = "";
    },

    show: function () {
      this.open = true;
    },

    close: function () {
      this.open = false;
      this.query = "";
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
