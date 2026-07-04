/* theme component @v1.0.0 — toggle dark mode and persist to localStorage.
 * Supports:
 *   - <div data-kit-component="theme"> <button data-kit-click="toggle()">
 *   - <div data-kit-component="theme@v1.0.0">
 */
var themeDef = {
  dark: document.documentElement.classList.contains("dark"),
  toggle: function () {
    this.dark = !this.dark;
    document.documentElement.classList.toggle("dark", this.dark);
    try { localStorage.setItem("theme", this.dark ? "dark" : "light"); } catch (e) {}
  }
};

window.kitwork.component("theme", themeDef);
window.kitwork.component("theme@v1.0.0", themeDef);
