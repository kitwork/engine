/* tab component @v1.0.0 — switch active tab panel.
 * Usage: <button data-kit-component="tab" data-kit-target="#tab-panel-1">Tab 1</button>
 */
window.kitwork.components.action("tab", function (el) {
  var t = window.kitwork.components.target(el);
  if (!t) return;
  var container = el.closest("[data-kit-tabs]") || el.parentElement;
  if (container) {
    var tabs = container.querySelectorAll("[data-kit-component='tab'],[data-kit-action='tab']");
    tabs.forEach(function (tab) {
      tab.classList.remove("active", "is-active");
      tab.setAttribute("data-state", "inactive");
    });
    el.classList.add("active", "is-active");
    el.setAttribute("data-state", "active");
  }
  var parent = t.parentElement;
  if (parent) {
    var panels = parent.children;
    for (var i = 0; i < panels.length; i++) {
      panels[i].classList.add("hidden");
      panels[i].classList.remove("active", "is-active");
    }
  }
  t.classList.remove("hidden");
  t.classList.add("active", "is-active");
});
