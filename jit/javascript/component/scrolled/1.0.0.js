;(function () {
"use strict";

/* scrolled@1.0.0 — has the page moved under this element?
 *
 * One boolean, kept two ways at once:
 *   - `scrolled` on the scope, so bindings can react (`data-kit-class="scrolled ? … : …"`);
 *   - `data-scrolled="true|false"` on the host, so plain CSS can react with no expression at all
 *     (jitcss: `data-[scrolled=true]:bg-paper/80`). Author the attribute in the markup too and the
 *     first paint already carries the resting state.
 *
 * The threshold is `offset` in px (24 by default; seed it with data-kit-scope="offset: 80").
 * The scroll handler is one read and one compare — browsers already deliver scroll events at
 * most once per frame, so there is nothing to coalesce — and it only touches the DOM when the
 * answer changes. The listener is the boundary's (context.listen): a removed host stops listening.
 *
 *   <nav data-kit-component="scrolled@1.0.0" data-scrolled="false"
 *        class="transition-colors data-[scrolled=true]:bg-paper/80 data-[scrolled=true]:backdrop-blur-xl">
 */

var instances = new WeakMap();

function threshold(value) {
  value = Number(value);
  return Number.isFinite(value) && value >= 0 ? value : 24;
}

function measure(scope, data) {
  var top = data.view.scrollY || data.document.documentElement.scrollTop || 0;
  var next = top > threshold(scope.offset);
  var flag = next ? "true" : "false";
  if (data.context.host.getAttribute("data-scrolled") !== flag) {
    data.context.host.setAttribute("data-scrolled", flag);
  }
  if (scope.scrolled !== next) scope.scrolled = next;
  return next;
}

kit.component("scrolled", {
  scrolled: false,
  offset: 24,

  init: function (context) {
    var scope = this;
    var data = {
      context: context,
      document: context.host.ownerDocument,
      view: context.host.ownerDocument.defaultView
    };
    instances.set(scope, data);
    context.listen(data.view, "scroll", function () { measure(scope, data); }, { passive: true });
    context.cleanup(function () { instances.delete(scope); });
    measure(scope, data);
  },

  // For a caller that changed `offset` and wants the answer now, not on the next scroll.
  measure: function () {
    var data = instances.get(this);
    return data ? measure(this, data) : Boolean(this.scrolled);
  }
});

})();
