// ============================================================================
// Kitwork Client Runtime Component: Dialog / Modal (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/component/dialog/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (!kit.component) return;

  kit.component("dialog", {
    // 1. State của Modal Dialog
    open: false,
    title: "",
    message: "",
    type: "alert", // "alert" | "confirm" | "prompt"
    value: "",     // Giá trị ô nhập liệu khi dùng prompt
    _resolver: null,

    get hidden() {
      return !this.open;
    },

    // 2. Mở Hộp thoại (Trả về Promise)
    alert: function (msg, title) {
      return this.show({ type: "alert", message: msg, title: title || "Thông báo" });
    },

    confirm: function (msg, title) {
      return this.show({ type: "confirm", message: msg, title: title || "Xác nhận" });
    },

    prompt: function (msg, defaultVal, title) {
      return this.show({ type: "prompt", message: msg, value: defaultVal || "", title: title || "Nhập thông tin" });
    },

    show: function (opts) {
      opts = opts || {};
      this.type = opts.type || "alert";
      this.message = opts.message || "";
      this.title = opts.title || "";
      this.value = opts.value || "";
      this.open = true;

      var self = this;
      return new Promise(function (resolve) {
        self._resolver = resolve;
      });
    },

    // 3. Phản hồi hành động từ Nút bấm UI
    accept: function () {
      this.open = false;
      if (this._resolver) {
        var res = this.type === "prompt" ? this.value : true;
        this._resolver(res);
        this._resolver = null;
      }
    },

    cancel: function () {
      this.open = false;
      if (this._resolver) {
        var res = this.type === "confirm" ? false : null;
        this._resolver(res);
        this._resolver = null;
      }
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
