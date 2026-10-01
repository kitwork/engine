/* visible component @v1.0.0 — is this element on screen, and has it ever been?
 *
 * Usage — a section that fades in the first time a reader reaches it:
 *
 *   <section data-kit-component="visible" data-visible="false"
 *            class="opacity-0 transition duration-700 data-[seen=true]:opacity-100">
 *
 * or with an expression, for anything a class cannot say:
 *
 *   <section data-kit-component="visible" data-threshold="0.5">
 *     <output data-kit-text="seen ? 'You got here' : 'Keep scrolling'"></output>
 *   </section>
 *
 * WHY THIS IS A COMPONENT. Whether an element is on screen is a fact the browser owns and reports
 * only by event, like the scroll position `scrolled` answers — no expression can read it. Sites were
 * writing the IntersectionObserver by hand to start a count-up when it is seen (apptop-impact,
 * kitdata.vn); this is that observer, once.
 *
 * Two answers, published twice:
 *   · `visible` — on screen now; `seen` — has been on screen at least once, and stays true;
 *   · data-visible / data-seen="true|false" on the host, for CSS alone, so a reveal needs no
 *     expression and no work per paint. Author data-visible="false" so the first paint already
 *     carries the resting state.
 *
 * The rules are plain attributes, because a component host takes no data-kit-scope:
 *   · data-threshold — how much of the element must show, 0 to 1; 0 (any of it) by default;
 *   · data-margin    — grows or shrinks the screen before the test, as CSS margin: "0px 0px -20% 0px"
 *                      counts an element only once it is a fifth of the way up.
 *
 * A browser without IntersectionObserver gets both answers true: content that waits to be seen
 * must never stay hidden because the browser cannot say so.
 *
 * The public surface: visible, seen.
 */
var VISIBLE = Symbol("kit:visible");

function setAnswer(scope, visible) {
  var own = scope[VISIBLE];
  if (!own) return;
  var host = own.context.host;
  var seen = scope.seen || visible;
  if (host.getAttribute("data-visible") !== String(visible)) host.setAttribute("data-visible", String(visible));
  if (host.getAttribute("data-seen") !== String(seen)) host.setAttribute("data-seen", String(seen));
  if (scope.visible !== visible) scope.visible = visible;
  if (scope.seen !== seen) scope.seen = seen;
}

var visibleDef = {
  visible: false,
  seen: false,

  init: function (context) {
    var scope = this;
    var host = context.host;
    var view = host.ownerDocument.defaultView;
    scope[VISIBLE] = { context: context };
    context.cleanup(function () { delete scope[VISIBLE]; });

    if (!view || typeof view.IntersectionObserver !== "function") {
      setAnswer(scope, true);
      return;
    }
    var threshold = Number(host.getAttribute("data-threshold"));
    if (!isFinite(threshold) || threshold < 0 || threshold > 1) threshold = 0;
    var options = { threshold: threshold };
    var margin = host.getAttribute("data-margin");
    if (margin) options.rootMargin = margin;

    var observer;
    try {
      observer = new view.IntersectionObserver(function (entries) {
        var entry = entries[entries.length - 1];
        // isIntersecting alone is true for a zero-area touch; the ratio is what the threshold means.
        setAnswer(scope, !!entry && entry.isIntersecting && entry.intersectionRatio >= threshold);
      }, options);
    } catch (error) {
      // A margin the browser cannot parse is the author's mistake; the content must still show.
      setAnswer(scope, true);
      return;
    }
    observer.observe(host);
    context.cleanup(function () { observer.disconnect(); });
  }
};

window.kit.component("visible", visibleDef);
window.kit.component("visible@v1.0.0", visibleDef);
