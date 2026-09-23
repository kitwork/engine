/* toast component @v1.0.0 — a message that shows itself and leaves after three seconds.
 *
 * Usage:
 *   <div data-kit-component="toast" data-kit-alias="$toast"></div>
 *   <div data-kit-show="visible" role="status" aria-live="polite" data-kit-text="message"></div>
 *   <button data-kit-click="$toast.show('Saved')">Save</button>
 *
 * Accessibility is the page's to declare: put role="status" and aria-live="polite" on the box, so
 * a screen reader announces the message without stealing focus. The component only owns the timing.
 *
 * A second show() restarts the clock rather than queueing; the pending timer is released with the
 * host, so a toast that was still counting when Drive swapped the page writes nothing afterwards.
 */
var toastDef = {
  visible: false,
  message: "",

  show: function (message) {
    var self = this;
    self.message = message || "Notification";
    self.visible = true;
    clearTimeout(self.hideTimer);
    self.hideTimer = setTimeout(function () { self.visible = false; }, 3000);
  },

  hide: function () {
    clearTimeout(this.hideTimer);
    this.visible = false;
  },

  init: function (context) {
    var self = this;
    context.cleanup(function () { clearTimeout(self.hideTimer); });
  }
};

window.kit.component("toast", toastDef);
window.kit.component("toast@v1.0.0", toastDef);
