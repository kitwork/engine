/* tab component @v1.0.0 — switch active tab panel.
 * Usage: <div data-kit-component="tab">
 */
var tabDef = {
  current: 0,
  select: function (index) {
    this.current = index;
  }
};

window.kit.component("tab", tabDef);
window.kit.component("tab@v1.0.0", tabDef);
