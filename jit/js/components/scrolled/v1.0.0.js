/* scrolled component @v1.0.0 — has the page moved under this element?
 *
 * Usage — a header that grows a background once the reader leaves the top:
 *
 *   <nav data-kit-component="scrolled" data-scrolled="false"
 *        class="transition-colors data-[scrolled=true]:bg-paper/80 data-[scrolled=true]:backdrop-blur-xl">
 *
 * The threshold is `offset` in pixels, 24 by default; a page that wants another number says so in
 * the markup: data-kit-scope is not available on a component host, so it is a plain attribute the
 * component reads — data-offset="80".
 *
 * WHY THIS IS A COMPONENT. Everything a reader can see here is already the grammar: the class list
 * above is CSS reacting to an attribute, and a page that prefers an expression can bind
 * data-kit-class="scrolled ? … : …" instead. What the grammar cannot do is ANSWER the question —
 * no expression can read how far the page has scrolled, because scroll position is not state a page
 * declares, it is a fact the browser owns and only reports by event. Turning that fact into one
 * boolean is the whole job.
 *
 * The answer is published twice on purpose:
 *   · `scrolled` on the scope, for expressions;
 *   · data-scrolled="true|false" on the host, for CSS alone — no expression, no runtime work per
 *     paint. Author the attribute in the markup too and the first paint already carries the resting
 *     state, so nothing shifts when the component mounts.
 *
 * There is nothing to throttle: browsers already deliver scroll at most once per frame. The handler
 * is one read and one compare, and it touches the DOM only when the answer changes. The listener
 * belongs to the boundary (context.listen), so a host that morph removes stops listening with it.
 */
var SCROLLED = Symbol("kit:scrolled");

// A page may say data-offset="80"; anything that is not a number at or above zero means the author
// did not mean to set one, and the default stands.
function scrollThreshold(scope) {
  var raw = scope[SCROLLED] ? scope[SCROLLED].context.host.getAttribute("data-offset") : null;
  var value = Number(raw);
  return raw !== null && isFinite(value) && value >= 0 ? value : 24;
}

function measureScroll(scope) {
  var own = scope[SCROLLED];
  if (!own) return Boolean(scope.scrolled);
  var top = own.view.scrollY || own.document.documentElement.scrollTop || 0;
  var next = top > scrollThreshold(scope);
  var flag = next ? "true" : "false";
  if (own.context.host.getAttribute("data-scrolled") !== flag) {
    own.context.host.setAttribute("data-scrolled", flag);
  }
  if (scope.scrolled !== next) scope.scrolled = next;
  return next;
}

var scrolledDef = {
  scrolled: false,

  init: function (context) {
    var scope = this;
    var document = context.host.ownerDocument;
    scope[SCROLLED] = { context: context, document: document, view: document.defaultView };
    context.listen(scope[SCROLLED].view, "scroll", function () { measureScroll(scope); }, { passive: true });
    context.cleanup(function () { delete scope[SCROLLED]; });
    measureScroll(scope);
  },

  // For a caller that changed the offset and wants the answer now rather than on the next scroll.
  measure: function () { return measureScroll(this); }
};

window.kit.component("scrolled", scrolledDef);
window.kit.component("scrolled@v1.0.0", scrolledDef);
