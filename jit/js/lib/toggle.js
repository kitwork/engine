/* toggle — show/hide a target by flipping a class (default `hidden`). */
window.kit.components.action("toggle", function (el) {
  var t = window.kit.components.target(el);
  if (!t) return;
  var cls = (el.getAttribute("data-kit-class") || el.getAttribute("data-kitwork-class")) || "hidden";
  t.classList.toggle(cls);
  el.setAttribute("aria-expanded", t.classList.contains(cls) ? "false" : "true");
});
