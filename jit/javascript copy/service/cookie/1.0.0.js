// ============================================================================
// Kitwork Client Runtime Service: Cookie (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.cookie) return;

  kit.cookie = {
    // 1. Đọc Cookie theo tên (Promise)
    get: function (name, fallback) {
      if (typeof document === "undefined" || !document.cookie) {
        return Promise.resolve(fallback !== undefined ? fallback : null);
      }

      var encodedName = encodeURIComponent(String(name || "")) + "=";
      var cookies = document.cookie.split(";");

      for (var i = 0; i < cookies.length; i++) {
        var c = cookies[i].trim();
        if (c.indexOf(encodedName) === 0) {
          var val = decodeURIComponent(c.substring(encodedName.length));
          try {
            return Promise.resolve(JSON.parse(val));
          } catch (_) {
            return Promise.resolve(val);
          }
        }
      }

      return Promise.resolve(fallback !== undefined ? fallback : null);
    },

    // 2. Ghi Cookie (Promise) - Cấu hình được: days, path, domain, secure, sameSite
    set: function (name, value, options) {
      if (typeof document === "undefined") {
        return Promise.reject("document unavailable");
      }

      options = options || {};
      var serialized = typeof value === "object" ? JSON.stringify(value) : String(value);
      var cookieStr = encodeURIComponent(String(name || "")) + "=" + encodeURIComponent(serialized);

      // Cấu hình thời gian hết hạn (ngày)
      if (typeof options.days === "number") {
        var d = new Date();
        d.setTime(d.getTime() + options.days * 24 * 60 * 60 * 1000);
        cookieStr += "; expires=" + d.toUTCString();
      } else if (options.expires) {
        cookieStr += "; expires=" + new Date(options.expires).toUTCString();
      }

      // Path mặc định là "/"
      cookieStr += "; path=" + (options.path || "/");

      if (options.domain) {
        cookieStr += "; domain=" + options.domain;
      }

      if (options.secure) {
        cookieStr += "; secure";
      }

      if (options.sameSite) {
        cookieStr += "; samesite=" + options.sameSite;
      }

      document.cookie = cookieStr;
      return Promise.resolve(true);
    },

    // 3. Xóa Cookie (Promise)
    remove: function (name, options) {
      options = options || {};
      options.days = -1; // Đặt ngày hết hạn về quá khứ
      return this.set(name, "", options);
    },

    // 4. Kiểm tra Cookie có tồn tại không (Promise)
    has: function (name) {
      return this.get(name).then(function (val) {
        return val !== null;
      });
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
