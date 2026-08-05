/* toast component @v1.0.0 — show auto-dismissing toast notification.
 * Usage: <div data-kit-component="toast">
 */
var toastDef = {
  visible: false,
  message: "",
  show: function (msg) {
    var self = this;
    self.message = msg || "Notification";
    self.visible = true;
    setTimeout(function () { self.visible = false; }, 3000);
  }
};

window.kit.component("toast", toastDef);
window.kit.component("toast@v1.0.0", toastDef);
