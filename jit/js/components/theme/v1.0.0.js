/* theme component @v1.0.0 — toggle theme.
 * Usage: <div data-kit-component="theme">
 */
var themeDef = {
  toggle: function () {
    if (window.kit && window.kit.toggleTheme) {
      window.kit.toggleTheme();
    }
  }
};

window.kit.component("theme", themeDef);
window.kit.component("theme@v1.0.0", themeDef);
