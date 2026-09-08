;(function () {
"use strict";

// A rotator is a carousel that advances itself. `carousel` deliberately owns no timer — the author
// drives it — so a headline that cycles words, or a slide strip that moves on its own, had nowhere
// to live but a hand-written script on the page. This component is that timer, with the pausing
// rules an auto-advancing region has to honour: WCAG 2.2.2 asks for a way to stop moving content,
// and content that moves while nobody is looking is pure battery.

var instances = new WeakMap();
var DEFAULT_INTERVAL = 4000;
var MINIMUM_INTERVAL = 200;

function values(value) {
  return Array.isArray(value) ? value : [];
}

function indexValue(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

// The item count comes from the `items` array when the author supplied one, and otherwise from the
// `[data-rotator-item]` elements the host owns — so a rotator works both over bound data and over
// slides written out as plain markup, without asking the author to repeat the count.
function count(scope, data) {
  var items = values(scope.items);
  if (items.length) return items.length;
  return data ? data.context.owned("[data-rotator-item]").length : 0;
}

function interval(scope) {
  var milliseconds = Number(scope.interval);
  if (!isFinite(milliseconds) || milliseconds <= 0) return DEFAULT_INTERVAL;
  return Math.max(MINIMUM_INTERVAL, milliseconds);
}

// A rotator is stopped by any one of several independent reasons — the author paused it, a pointer
// is over it, focus is inside it, the tab is hidden, the reader asked for reduced motion. Keeping
// them as separate flags means one reason clearing does not resume a rotator another still holds.
function held(data) {
  return data.hovered || data.focused || data.hidden || data.reduced;
}

function stop(data) {
  if (data.timer === null) return;
  clearTimeout(data.timer);
  data.timer = null;
}

function schedule(scope, data) {
  stop(data);
  var running = !data.disposed && !!scope.playing && !held(data) && count(scope, data) > 1;
  // `running` is reactive state, not a derived getter, because the reasons a rotator stops live
  // outside the scope — a pointer, focus, tab visibility, a motion preference. A method reading
  // those private flags would answer correctly and still never re-render the play/pause control
  // that displays it, since nothing reactive changed. Assign only on a real change: every render
  // pass calls back into here, and an unconditional write would render forever.
  if (scope.running !== running) scope.running = running;
  if (!running) return;
  data.timer = setTimeout(function () {
    data.timer = null;
    scope.next();
    schedule(scope, data);
  }, interval(scope));
}

// Every state change that could start or stop the timer funnels through here, so there is one place
// that decides whether a rotator should be running.
function sync(scope) {
  var data = instances.get(scope);
  if (data) schedule(scope, data);
}

kit.component("rotator", {
  items: [],
  active: 0,
  interval: DEFAULT_INTERVAL,
  // `playing` is what the author asked for; `running` is what is actually happening. They differ
  // whenever something else holds the rotator — a pointer over the region, focus inside it, a
  // hidden tab, a reduced-motion preference — so a play/pause control should bind `running`.
  playing: true,
  running: false,

  init: function (context) {
    var scope = this;
    var motion = typeof context.host.ownerDocument.defaultView.matchMedia === "function"
      ? context.host.ownerDocument.defaultView.matchMedia("(prefers-reduced-motion: reduce)")
      : null;
    var data = {
      context: context,
      timer: null,
      hovered: false,
      focused: false,
      hidden: !!context.host.ownerDocument.hidden,
      reduced: !!(motion && motion.matches),
      disposed: false
    };
    instances.set(scope, data);

    // Hover and focus hold the rotator still while someone is reading or tabbing through it; both
    // release on the way out. pointerenter/leave rather than mouseover so a touch drag does not
    // wedge it paused.
    context.listen(context.host, "pointerenter", function () {
      data.hovered = true;
      schedule(scope, data);
    });
    context.listen(context.host, "pointerleave", function () {
      data.hovered = false;
      schedule(scope, data);
    });
    context.listen(context.host, "focusin", function () {
      data.focused = true;
      schedule(scope, data);
    });
    context.listen(context.host, "focusout", function (event) {
      if (event.relatedTarget && context.host.contains(event.relatedTarget)) return;
      data.focused = false;
      schedule(scope, data);
    });

    context.listen(context.host.ownerDocument, "visibilitychange", function () {
      data.hidden = !!context.host.ownerDocument.hidden;
      schedule(scope, data);
    });

    if (motion) {
      var motionChanged = function () {
        data.reduced = !!motion.matches;
        schedule(scope, data);
      };
      // Safari below 14 exposes only the deprecated listener pair.
      if (typeof motion.addEventListener === "function") {
        context.listen(motion, "change", motionChanged);
      } else if (typeof motion.addListener === "function") {
        motion.addListener(motionChanged);
        context.cleanup(function () { motion.removeListener(motionChanged); });
      }
    }

    // The item count can change after the first render — `{{ for }}` output arriving, or slides
    // added by a Morph — so re-check on each render pass rather than only at mount. afterRender is
    // one-shot, hence the re-registration.
    function afterRender() {
      if (data.disposed) return;
      schedule(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);

    context.cleanup(function () {
      data.disposed = true;
      stop(data);
      instances.delete(scope);
    });

    schedule(scope, data);
  },

  select: function (index) {
    var data = instances.get(this);
    var length = count(this, data);
    var selected = indexValue(index, length);
    if (selected < 0) {
      var current = indexValue(this.active, length);
      return current < 0 ? false : current;
    }
    this.active = selected;
    // A manual jump restarts the dwell, so the slide someone just chose gets its full time rather
    // than whatever was left of the previous one.
    sync(this);
    return this.active;
  },

  next: function () {
    var data = instances.get(this);
    var length = count(this, data);
    if (!length) return false;
    var current = indexValue(this.active, length);
    this.active = current < 0 || current + 1 >= length ? 0 : current + 1;
    return this.active;
  },

  previous: function () {
    var data = instances.get(this);
    var length = count(this, data);
    if (!length) return false;
    var current = indexValue(this.active, length);
    this.active = current <= 0 ? length - 1 : current - 1;
    return this.active;
  },

  first: function () {
    return this.select(0);
  },

  last: function () {
    var length = count(this, instances.get(this));
    return length ? this.select(length - 1) : false;
  },

  play: function () {
    this.playing = true;
    sync(this);
    return true;
  },

  pause: function () {
    this.playing = false;
    sync(this);
    return false;
  },

  toggle: function () {
    return this.playing ? this.pause() : this.play();
  },

  isActive: function (index) {
    var data = instances.get(this);
    var length = count(this, data);
    var selected = indexValue(index, length);
    return selected >= 0 && selected === indexValue(this.active, length);
  }
});

})();
