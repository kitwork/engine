// ============================================================================
// Kitwork Client Runtime Service: Theme
// ============================================================================
// - Hỗ trợ chiến lược Apply Theme linh hoạt: "class" (.dark) hoặc "attribute" (data-theme="dark").
// - Không lặp lại window.app = kit (việc gán alias thuộc về Core Kernel).
// - Tái sử dụng kit.storage để lưu trữ trạng thái theme.
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.theme) return;

  kit.theme = {
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
        return kit.storage.get("theme", "system");
      }
      return (typeof localStorage !== "undefined" && localStorage.getItem("theme")) || "system";
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
        kit.storage.set("theme", mode);
      } else if (typeof localStorage !== "undefined") {
        localStorage.setItem("theme", mode);
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

  // Khởi tạo & Lắng nghe OS theme change
  kit.theme._updateDOM(kit.theme.resolved);

  if (typeof matchMedia === "function") {
    try {
      matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
        if (kit.theme.mode === "system") {
          kit.theme._updateDOM(kit.theme.resolved);
        }
      });
    } catch (_) {}
  }

})(typeof window !== "undefined" ? window : globalThis);
