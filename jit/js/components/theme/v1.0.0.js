/* theme component @v1.0.0 — toggle theme.
 * Usage: <div data-kit-component="theme">
 */
var themeDef = {
  toggle: function () {
    if (window.kitwork && window.kitwork.toggleTheme) {
      window.kitwork.toggleTheme();
    }
  }
};

window.kitwork.component("theme", themeDef);
window.kitwork.component("theme@v1.0.0", themeDef);
