// ============================================================================
// Kitwork Client Runtime Service: Window (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.window) return;

  kit.window = {

    drag: function () {
      // Native implementation
    },

    // 1. Thu nhỏ cửa sổ ứng dụng (Desktop App Shell)
    minimize: function () {
      if (typeof window !== "undefined" && window.close && typeof window.minimize === "function") {
        window.minimize();
        return Promise.resolve(true);
      }
      return Promise.reject("window.minimize unavailable in browser context");
    },

    // 2. Phóng to toàn màn hình cửa sổ ứng dụng
    maximize: function () {
      if (typeof document !== "undefined" && document.documentElement && document.documentElement.requestFullscreen) {
        return document.documentElement.requestFullscreen().then(function () { return true; });
      }
      return Promise.reject("Fullscreen API unavailable");
    },

    // 3. Khôi phục kích thước cửa sổ bình thường
    restore: function () {
      if (typeof document !== "undefined" && document.exitFullscreen && document.fullscreenElement) {
        return document.exitFullscreen().then(function () { return true; });
      }
      return Promise.resolve(true);
    },

    // 4. Đóng cửa sổ ứng dụng
    close: function () {
      if (typeof window !== "undefined" && window.close) {
        window.close();
        return Promise.resolve(true);
      }
      return Promise.reject("window.close unavailable");
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
