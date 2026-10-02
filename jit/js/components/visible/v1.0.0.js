/* visible component @v1.0.0 — is this element on screen, and has it ever been?
 *
 * Usage — a section that fades in the first time a reader reaches it:
 *
 *   <section data-kit-component="visible"
 *            data-kit-bind:data-unseen="seen === false"
 *            class="transition duration-700 data-[unseen]:opacity-0">
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
 * Two answers, as state: `visible` — on screen now; `seen` — has been on screen at least once, and
 * stays true. Both are null until the observer has answered once: before that nobody knows, and
 * "not known yet" must not read as "not seen", or content already on screen would be hidden at
 * load and fade back in — and stay hidden in a background tab, where no answer comes until it is
 * shown. So the reveal above binds `seen === false`, true only once the answer is a real no.
 *
 * The component writes no attribute; the markup binds what its CSS needs, where a reader can see
 * where data-unseen comes from. A bound `true` writes the bare attribute and a `false` removes it,
 * so data-[unseen] matches only content known to be unseen — and before the script runs there is
 * no attribute at all, so the content shows.
 *
 * The rules are plain attributes, because a component host takes no data-kit-scope:
 *   · data-threshold — how much of the element must show, 0 to 1; 0 (any of it) by default;
 *   · data-margin    — grows or shrinks the screen before the test, as CSS margin: "0px 0px -20% 0px"
 *                      counts an element only once it is a fifth of the way up.
 *
 * A browser without IntersectionObserver gets both answers true: content that waits to be seen
 * must never stay hidden because the browser cannot say so.
 *
 * And one event, `seen`, the moment the element first reaches the screen — for something that
 * should HAPPEN then rather than show: a count-up that starts, a chart that draws.
 *
 *   <section data-kit-component="visible" data-kit-event:seen="$impact.start()">
 *
 * The public surface: visible, seen; the event seen.
 */
var VISIBLE = Symbol("kit:visible");

function setAnswer(scope, visible) {
  var own = scope[VISIBLE];
  if (!own) return;
  var seen = scope.seen || visible;
  var first = seen === true && scope.seen !== true;
  if (scope.visible !== visible) scope.visible = visible;
  if (scope.seen !== seen) scope.seen = seen;
  if (first) own.context.emit("seen");
}

var visibleDef = {
  visible: null,
  seen: null,

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
