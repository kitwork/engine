// ============================================================================
// Kitwork Official Headless Component: Tabs (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/tabs/1.0.0.js
// ============================================================================
// Headless Tabs Component với Keyboard Navigation (Arrow Left/Right, Home, End):
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("tabs", {
    activeTab: "overview",

    select: function (tabId) {
      this.activeTab = tabId;
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
