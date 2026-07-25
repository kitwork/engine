/* dialog component @v1.0.0 — open/close dialog or modal.
 * Usage: <button data-kit-component="dialog" data-kit-target="#my-dialog">Open Dialog</button>
 */
window.kitwork.components.action("dialog", function (el) {
  var t = window.kitwork.components.target(el);
  if (!t) return;
  if (t.tagName === "DIALOG") {
    if (t.open) {
      if (t.close) t.close(); else t.removeAttribute("open");
    } else {
      if (t.showModal) t.showModal(); else if (t.show) t.show(); else t.setAttribute("open", "");
    }
  } else {
    t.classList.toggle("hidden");
  }
});
