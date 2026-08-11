// ============================================================================
// Kitwork Official Headless Component: Menu (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/menu/1.0.0.js
// ============================================================================
// Headless Menu Bar / Context Menu với Keyboard Navigation (Arrow Up/Down/Home/End):
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("menu", {
    open: false,
    activeIndex: -1,

    toggle: function () {
      this.open = !this.open;
      if (!this.open) this.activeIndex = -1;
    },

    close: function () {
      this.open = false;
      this.activeIndex = -1;
    },

    navigate: function (dir, itemCount) {
      if (!this.open) this.open = true;
      if (dir === "next") {
        this.activeIndex = (this.activeIndex + 1) % itemCount;
      } else if (dir === "prev") {
        this.activeIndex = (this.activeIndex - 1 + itemCount) % itemCount;
      } else if (dir === "first") {
        this.activeIndex = 0;
      } else if (dir === "last") {
        this.activeIndex = itemCount - 1;
      }
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
