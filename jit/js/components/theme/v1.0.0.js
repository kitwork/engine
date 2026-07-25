/* theme component @v1.0.0 — toggle light/dark theme.
 * Usage: <button data-kit-component="theme">Toggle Theme</button>
 */
window.kitwork.components.register("theme", function () {
  if (window.kitwork && window.kitwork.toggleTheme) {
    window.kitwork.toggleTheme();
  }
});
