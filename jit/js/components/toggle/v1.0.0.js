/* toggle component @v1.0.0 — toggle state/class.
 * Usage: <div data-kit-component="toggle">
 */
var toggleDef = {
  open: false,
  toggle: function () {
    this.open = !this.open;
  }
};

window.kitwork.component("toggle", toggleDef);
window.kitwork.component("toggle@v1.0.0", toggleDef);
