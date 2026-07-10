/* dismiss — remove a target (data-kitwork-target, else the nearest [data-kitwork-dismissable]). */
window.kitwork.components.action("dismiss", function (el) {
  var t = (el.getAttribute("data-kit-target") || el.getAttribute("data-kitwork-target"))
    ? window.kitwork.components.target(el)
    : (el.closest("[data-kit-dismissable],[data-kitwork-dismissable]") || el.parentElement);
  if (t) t.remove();
});
