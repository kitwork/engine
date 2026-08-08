// ============================================================================
// Kitwork Client Runtime Core: Boot (1.0.0-draft 0.5)
// ============================================================================
// Location: engine/jit/javascript/core/boot.js
// ============================================================================
// Khởi tạo namespace toàn cục duy nhất `window.kit`.
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.version = "1.0.0-draft.0.5";
  kit.specVersion = "0.5.0";

})(typeof window !== "undefined" ? window : globalThis);
