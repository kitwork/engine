// ============================================================================
// Kitwork Component: Toast Notification (1.0.0 - Draft 0.5 Compliant)
// ============================================================================
// Location: engine/jit/javascript/component/toast/1.0.0.js
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  kit.component("toast", {
    visible: false,
    message: "",
    type: "info", // "info", "success", "error"

    show: function (msg, toastType) {
      this.message = msg || "Thao tác thành công!";
      this.type = toastType || "success";
      this.visible = true;

      var self = this;
      setTimeout(function () {
        self.visible = false;
        if (kit.render) kit.render();
      }, 3000);
    },

    close: function () {
      this.visible = false;
    }
  });

})(typeof window !== "undefined" ? window : globalThis);
