/* dialog component @v1.0.0 — open/close dialog or modal.
 * Usage: <div data-kit-component="dialog">
 */
var dialogDef = {
  open: false,
  show: function (targetId) {
    this.open = true;
    var el = targetId ? document.querySelector(targetId) : null;
    if (el && el.tagName === "DIALOG" && el.showModal) el.showModal();
  },
  close: function (targetId) {
    this.open = false;
    var el = targetId ? document.querySelector(targetId) : null;
    if (el && el.tagName === "DIALOG" && el.close) el.close();
  }
};

window.kitwork.component("dialog", dialogDef);
window.kitwork.component("dialog@v1.0.0", dialogDef);
