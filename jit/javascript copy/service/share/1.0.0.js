// ============================================================================
// Kitwork Client Runtime Service: Share (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.share) return;

  // Dịch vụ Share tường minh (Explicit Plain Object)
  kit.share = {
    // 1. Mở menu chia sẻ (Web Share API với Fallback sang Clipboard)
    open: function (data) {
      if (!data) data = {};
      if (typeof data === "string") {
        data = { url: data };
      }

      var payload = {
        title: String(data.title || (typeof document !== "undefined" ? document.title : "") || ""),
        text: String(data.text || ""),
        url: String(data.url || (typeof window !== "undefined" && window.location ? window.location.href : "") || "")
      };

      // Web Share API native
      if (typeof navigator !== "undefined" && navigator.share) {
        return navigator.share(payload).then(function () { return true; });
      }

      // Fallback copy URL vào bộ nhớ tạm
      if (kit.clipboard && kit.clipboard.writeText) {
        return kit.clipboard.writeText(payload.url);
      }

      return Promise.reject("Share API and Clipboard fallbacks unavailable");
    },

    // 2. Kiểm tra tính khả dụng của Native Share
    canShare: function (data) {
      if (typeof navigator !== "undefined" && navigator.canShare) {
        return navigator.canShare(data);
      }
      return typeof navigator !== "undefined" && !!navigator.share;
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
