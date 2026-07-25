/* dismiss component @v1.0.0 — remove target element or parent card/alert from DOM.
 * Usage: <button data-kit-component="dismiss">Close</button>
 */
window.kitwork.components.register("dismiss", function (el) {
  var t = (el.getAttribute("data-kit-target") || el.getAttribute("data-kitwork-target"))
    ? window.kitwork.components.target(el)
    : (el.closest("[data-kit-dismissable],[data-kitwork-dismissable]") || el.parentElement);
  if (t) t.remove();
});
