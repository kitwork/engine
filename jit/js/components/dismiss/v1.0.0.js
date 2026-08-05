/* dismiss component @v1.0.0 — dismiss target element.
 * Usage: <div data-kit-component="dismiss">
 */
var dismissDef = {
  dismissed: false,
  dismiss: function (el) {
    this.dismissed = true;
    var target = el && el.closest ? (el.closest("[data-kit-dismissable]") || el.parentElement) : el;
    if (target) target.remove();
  }
};

window.kit.component("dismiss", dismissDef);
window.kit.component("dismiss@v1.0.0", dismissDef);
