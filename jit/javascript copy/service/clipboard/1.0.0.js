// ============================================================================
// Kitwork Client Runtime Service: Clipboard (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.clipboard) return;

  kit.clipboard = {
    // 1. Ghi văn bản vào bộ nhớ tạm (Promise)
    writeText: function (text) {
      text = String(text || "");

      // Web Browser Async Clipboard API
      if (typeof navigator !== "undefined" && navigator.clipboard && navigator.clipboard.writeText) {
        return navigator.clipboard.writeText(text).then(function () { return true; });
      }

      // Fallback cho môi trường bị giới hạn (document.execCommand)
      try {
        var textarea = document.createElement("textarea");
        textarea.value = text;
        textarea.style.position = "fixed";
        textarea.style.opacity = "0";
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand("copy");
        document.body.removeChild(textarea);
        return Promise.resolve(true);
      } catch (err) {
        return Promise.reject(err);
      }
    },

    // 2. Đọc văn bản từ bộ nhớ tạm (Promise)
    readText: function () {
      // Web Browser Async Clipboard API
      if (typeof navigator !== "undefined" && navigator.clipboard && navigator.clipboard.readText) {
        return navigator.clipboard.readText();
      }

      return Promise.reject("Clipboard readText API not supported or permission denied");
    },

    // 3. Alias ngắn gọn cho writeText
    copy: function (text) {
      return this.writeText(text);
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
