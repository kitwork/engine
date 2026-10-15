/* rotator component — the clock of a region that advances itself.
 *
 * Usage — the markup draws the items and what shows; the component only keeps time:
 *
 *   <div data-kit-component="rotator" data-interval="1800">
 *     One basecode for your
 *     <span aria-live="polite">
 *       <span data-kit-element="item" data-kit-show="active === 0">sites</span>
 *       <span data-kit-element="item" data-kit-show="active === 1" hidden>apps</span>
 *       <span data-kit-element="item" data-kit-show="active === 2" hidden>jobs</span>
 *     </span>
 *     <button data-kit-click="toggle()" data-kit-bind:aria-label="running ? 'Pause' : 'Play'">…</button>
 *   </div>
 *
 * WHY THIS IS A COMPONENT. Which item shows is a binding on `active`, and the dots, the labels and
 * the play/pause control are all markup. What the grammar cannot do is KEEP TIME, and keep it the
 * way an auto-advancing region must: WCAG 2.2.2 asks for a way to stop moving content, and content
 * that moves while nobody is looking is pure battery. So it holds still while
 *
 *   · a pointer is over it, or focus is inside it — someone is reading;
 *   · the tab is hidden — nobody is;
 *   · the reader asked for reduced motion.
 *
 * `playing` is what was asked for (play, pause, toggle); `running` is what is actually happening,
 * and it is the one a play/pause control should show — it flips back when the pointer leaves.
 *
 * The count is the number of data-kit-element="item" elements, read every paint, so items a loop
 * renders later count too. data-interval is milliseconds, 4000 by default and never below 200.
 *
 * It is the carousel's missing half on purpose: the carousel owns no timer, and a carousel that
 * moves on its own is a rotator whose items are slides.
 *
 * The public surface: active, count, playing, running, go(n), next(), previous(), play(), pause(),
 * toggle().
 */
var ROTATOR = Symbol("kit:rotator");
var DEFAULT_INTERVAL = 4000;
var MINIMUM_INTERVAL = 200;

function rotatorCount(scope) {
  var own = scope[ROTATOR];
  return own ? own.context.elements("item").length : 0;
}

function rotatorInterval(scope) {
  var own = scope[ROTATOR];
  var value = own ? Number(own.context.host.getAttribute("data-interval")) : NaN;
  if (!isFinite(value) || value <= 0) return DEFAULT_INTERVAL;
  return Math.max(MINIMUM_INTERVAL, value);
}

// Every change that could start or stop the clock comes through here, so one place decides. The
// reasons to hold still are separate flags: one clearing must not resume a rotator another holds.
function rotatorSchedule(scope) {
  var own = scope[ROTATOR];
  if (!own) return;
  clearTimeout(own.timer);
  own.timer = 0;
  var count = rotatorCount(scope);
  if (scope.count !== count) scope.count = count;
  var running = !!scope.playing && !own.hovered && !own.focused && !own.hidden && !own.reduced && count > 1;
  // Assigned only on a change: every paint calls back into here, and an unconditional write
  // would paint forever.
  if (scope.running !== running) scope.running = running;
  if (!running) return;
  own.timer = setTimeout(function () {
    own.timer = 0;
    scope.next();
  }, rotatorInterval(scope));
}

var rotatorDef = {
  active: 0,
  count: 0,
  playing: true,
  running: false,

  go: function (index) {
    var count = rotatorCount(this);
    var next = Math.floor(Number(index));
    if (!count || !isFinite(next)) return;
    this.active = ((next % count) + count) % count;
    rotatorSchedule(this); // a choice restarts the dwell, so the chosen item gets its full time
  },

  next: function () { this.go(this.active + 1); },
  previous: function () { this.go(this.active - 1); },

  play: function () { this.playing = true; rotatorSchedule(this); },
  pause: function () { this.playing = false; rotatorSchedule(this); },
  toggle: function () { if (this.playing) this.pause(); else this.play(); },

  init: function (context) {
    var scope = this;
    var document = context.host.ownerDocument;
    var view = document.defaultView;
    var motion = view && typeof view.matchMedia === "function" ? view.matchMedia("(prefers-reduced-motion: reduce)") : null;
    var own = scope[ROTATOR] = {
      context: context,
      timer: 0,
      hovered: false,
      focused: false,
      hidden: !!document.hidden,
      reduced: !!(motion && motion.matches)
    };
    context.cleanup(function () { clearTimeout(own.timer); delete scope[ROTATOR]; });
    context.require("item");

    // pointerenter/leave rather than mouseover, so a touch drag does not wedge it paused.
    context.listen(context.host, "pointerenter", function () { own.hovered = true; rotatorSchedule(scope); });
    context.listen(context.host, "pointerleave", function () { own.hovered = false; rotatorSchedule(scope); });
    context.listen(context.host, "focusin", function () { own.focused = true; rotatorSchedule(scope); });
    context.listen(context.host, "focusout", function (event) {
      if (event.relatedTarget && context.host.contains(event.relatedTarget)) return;
      own.focused = false;
      rotatorSchedule(scope);
    });
    context.listen(document, "visibilitychange", function () { own.hidden = !!document.hidden; rotatorSchedule(scope); });
    if (motion && typeof motion.addEventListener === "function") {
      context.listen(motion, "change", function () { own.reduced = !!motion.matches; rotatorSchedule(scope); });
    }

    // The count can change after mount — items a loop renders, or a morph — so look again after
    // every paint. afterRender is one-shot, hence the re-registration.
    var again = function () {
      if (!scope[ROTATOR]) return;
      if (!own.timer || rotatorCount(scope) !== scope.count) rotatorSchedule(scope);
      context.afterRender(again);
    };
    context.afterRender(again);
  }
};

window.kit.component("rotator", rotatorDef);
