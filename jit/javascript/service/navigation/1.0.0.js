// ============================================================================
// Kitwork Client Runtime Service: Navigation (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.navigation) return;

  kit.navigation = {
    // 1. Quay lại trang trước đó trong lịch sử
    back: function () {
      if (typeof window !== "undefined" && window.history && window.history.back) {
        window.history.back();
        return Promise.resolve(true);
      }
      return Promise.reject("history.back unavailable");
    },

    // 2. Đi tới trang kế tiếp trong lịch sử
    forward: function () {
      if (typeof window !== "undefined" && window.history && window.history.forward) {
        window.history.forward();
        return Promise.resolve(true);
      }
      return Promise.reject("history.forward unavailable");
    },

    // 3. Tải lại trang hiện tại
    reload: function () {
      if (typeof window !== "undefined" && window.location && window.location.reload) {
        window.location.reload();
        return Promise.resolve(true);
      }
      return Promise.reject("location.reload unavailable");
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
