/* tab — switch tabs. The clicked button selects itself among its [role="tablist"] siblings and
 * reveals its target [role="tabpanel"], hiding the panel's siblings. Markup:
 *   <div role="tablist">
 *     <button role="tab" aria-selected="true" data-kitwork-action="tab" data-kitwork-target="#one">One</button>
 *     <button role="tab" data-kitwork-action="tab" data-kitwork-target="#two">Two</button>
 *   </div>
 *   <div role="tabpanel" id="one">…</div>
 *   <div role="tabpanel" id="two" hidden>…</div>
 */
window.kitwork.components.action("tab", function (el) {
  var panel = window.kitwork.components.target(el);
  if (!panel) return;
  var list = el.closest('[role="tablist"]') || el.parentElement;
  if (list) {
    list.querySelectorAll('[data-kit-action="tab"],[data-kitwork-action="tab"]').forEach(function (t) {
      t.setAttribute("aria-selected", t === el ? "true" : "false");
    });
  }
  var group = panel.parentElement;
  if (group) {
    group.querySelectorAll('[role="tabpanel"]').forEach(function (p) {
      if (p.parentElement === group) p.hidden = (p !== panel);
    });
  }
  panel.hidden = false;
});
