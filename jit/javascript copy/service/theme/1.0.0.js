// ============================================================================
// Kitwork Client Runtime Service: Theme (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.theme) return;

  kit.theme = {
    // Tên key lưu trữ trong storage (Mặc định là "theme", dễ dàng tùy biến)
    key: "theme",

    // Chiến lược apply theme: "class" (thêm .dark lên <html>) hoặc "attribute" (data-theme="dark")
    apply: "class",

    // Cập nhật DOM theo chiến lược đã cấu hình
    _updateDOM: function (resolvedMode) {
      if (typeof document === "undefined" || !document.documentElement) return;
      var el = document.documentElement;

      if (this.apply === "class") {
        if (resolvedMode === "dark") {
          el.classList.add("dark");
        } else {
          el.classList.remove("dark");
        }
      } else if (this.apply === "attribute") {
        el.setAttribute("data-theme", resolvedMode);
      }
    },

    // Đọc chế độ theme (Tái sử dụng kit.storage.get)
    get mode() {
      if (kit.storage && kit.storage.get) {
        return kit.storage.get(this.key, "system");
      }
      return (typeof localStorage !== "undefined" && localStorage.getItem(this.key)) || "system";
    },

    // Trả về kết quả sau khi tính toán OS preference
    get resolved() {
      if (this.mode === "system") {
        return (typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches)
          ? "dark"
          : "light";
      }
      return this.mode;
    },

    // Gán chế độ theme mới ("light", "dark", "system")
    set: function (mode) {
      if (mode !== "light" && mode !== "dark" && mode !== "system") mode = "system";

      if (kit.storage && kit.storage.set) {
        kit.storage.set(this.key, mode);
      } else if (typeof localStorage !== "undefined") {
        localStorage.setItem(this.key, mode);
      }

      this._updateDOM(this.resolved);
      return this.resolved;
    },

    // Chuyển đổi qua lại giữa "light" và "dark"
    toggle: function () {
      var nextMode = this.resolved === "dark" ? "light" : "dark";
      return this.set(nextMode);
    }
  };

  // Áp dụng theme ban đầu khi nạp script
  kit.theme._updateDOM(kit.theme.resolved);

})(typeof window !== "undefined" ? window : globalThis);
