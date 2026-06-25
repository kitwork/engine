/* theme — toggle dark mode on <html> and persist it. Pair with a pre-paint <head> init that reads
 * localStorage('theme') to avoid a flash; icons/labels can react purely via CSS on `html.dark`.
 *   <button data-kitwork-action="theme" aria-label="Toggle theme">…</button>
 */
window.kitwork.components.action("theme", function () {
  var dark = document.documentElement.classList.toggle("dark");
  try { localStorage.setItem("theme", dark ? "dark" : "light"); } catch (e) {}
});
