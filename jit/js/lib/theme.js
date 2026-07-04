/* theme verb — toggle dark mode on <html> and persist it.
 * Supports: <button data-kitwork-action="theme">Toggle Theme</button>
 */
window.kitwork.components.action("theme", function () {
  var dark = document.documentElement.classList.toggle("dark");
  try { localStorage.setItem("theme", dark ? "dark" : "light"); } catch (e) {}
  
  // Sync the theme component scope if it exists on page
  var comp = window.kitwork.scope.theme;
  if (comp) comp.dark = dark;
});
