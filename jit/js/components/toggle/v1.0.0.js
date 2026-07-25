/* toggle component @v1.0.0 — toggle class on target element.
 * Usage: <button data-kit-component="toggle" data-kit-target="#sidebar" data-kit-class="hidden">Toggle</button>
 */
window.kitwork.components.register("toggle", function (el) {
  var t = window.kitwork.components.target(el);
  if (!t) return;
  var cls = el.getAttribute("data-kit-class") || el.getAttribute("data-kitwork-class") || "active";
  t.classList.toggle(cls);
});
