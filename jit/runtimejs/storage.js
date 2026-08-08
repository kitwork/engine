// ============================================================================
// Kitwork Client Runtime: Storage Service
// ============================================================================
// - Unified Async API (Trả về Promise): Tương thích 100% trên Web, Mobile App
//   Native (iOS/Android Encrypted Storage) và IndexedDB.
// - Auto Promise Observer: Kitwork Kernel tự động lắng nghe Promise trả về từ
//   kit.storage.get/set trong HTML expression (data-kit-click) và tự động
//   re-render UI khi Promise hoàn tất mà không cần gõ `await`.
// - Auto JSON: Tự động JSON.stringify khi lưu Object và JSON.parse khi đọc.
// ============================================================================

(function (window) {
  "use strict";

  // 1. Đảm bảo window.kit luôn tồn tại an toàn
  var kit = window.kit = window.kit || {};

  // 2. Chống nạp trùng hoặc khi trình duyệt không hỗ trợ LocalStorage
  if (kit.storage || !window.localStorage) return;

  // 3. Khai báo Dịch vụ Storage thuần Plain Object
  kit.storage = {
    // Đọc dữ liệu (Trả về Promise - Tự động parse JSON nếu có)
    get: function (key, fallback) {
      var val = localStorage.getItem(key);
      if (val === null) return Promise.resolve(fallback !== undefined ? fallback : null);
      try { 
        return Promise.resolve(JSON.parse(val)); 
      } catch (_) { 
        return Promise.resolve(val); 
      }
    },

    // Ghi dữ liệu (Trả về Promise - Tự động serialize Object/Array sang JSON)
    set: function (key, value) {
      var serialized = typeof value === "object" ? JSON.stringify(value) : String(value);
      localStorage.setItem(key, serialized);
      return Promise.resolve(true);
    },

    // Xóa 1 key (Trả về Promise)
    remove: function (key) {
      localStorage.removeItem(key);
      return Promise.resolve(true);
    },

    // Xóa toàn bộ bộ nhớ (Trả về Promise)
    clear: function () {
      localStorage.clear();
      return Promise.resolve(true);
    }
  };

})(typeof window !== "undefined" ? window : globalThis);