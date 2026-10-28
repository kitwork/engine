/* carousel component — slides on a scroll-snap track, which one is showing, and moving on by itself
 * (Kitwork UI's <carousel>, docs/components/carousel.md).
 *
 * On a Kitwork UI page the engine writes it from:
 *
 *   <carousel as="$hero" alt="Món nổi bật" duration="5s">
 *     <list><item>…</item><item>…</item></list>
 *     <button click="previous()">‹</button> <button click="next()">›</button>
 *     <button click="toggle()">{{ playing ? 'Dừng' : 'Chạy' }}</button>
 *     <button for="mon, i of mons" click="go(i)" active:color="brand">…</button>
 *   </carousel>
 *
 * as data-kitwork-component="carousel: $hero" on the box the tag builds, with role="region", its
 * <list> as the track (class carousel-track, an id, tabindex 0, aria-live bound to playing), each
 * <item> a slide
 * (aria-roledescription), and each dot a button with its place as value, aria-controls naming the
 * track, and click "go(Number($this.value))" (a for's index is the server's). (A template page's
 * carousel — data-kit-element="track" and "slide", its own dots, `active` — went on 26/10/2026.)
 *
 * THE BROWSER DOES THE MOVING. A track with scroll-snap swipes on a phone, scrolls on a trackpad,
 * steps with the arrow keys once focused and carries momentum; reduced motion is CSS's. What no markup
 * can do is this component's:
 *   · current — which slide shows: the one whose start is nearest the track's scroll position (any
 *     slide width, three cards a row too: the first one seen). While a move this component asked for
 *     is under way, the slides it passes do not count: the dots go straight to the one asked for.
 *   · go(n), next(), previous() — next() from the last slide goes on to the first, previous() from the
 *     first goes back to the last, the way they always go: the track does not run back across every
 *     slide. For that one move the slide comes round (CSS order puts the first after the last, or the
 *     last before the first, the view kept where it is), the track slides on to it, and once it rests
 *     the slide goes home and the track jumps to it at once: nothing moves on the screen, the slides
 *     are never copied, a screen reader's order never changes. While a move asked for is under way (a
 *     press, a dot, playing) a press of next(), previous() or go() is not taken: it does nothing, no
 *     element is disabled (a focused button keeps its focus), and nothing waits to run after (19/10/2026,
 *     Quốc: a press that comes again while it slides is not taken). The order is set with no transition, so it holds at once even where every property eases (less motion's
 *     0.01ms, an author's transition on all). go(n) and a dot go straight there (a
 *     place chosen, like a tab). With several slides a row (three cards) next() at the end goes back to
 *     the first. transition="none" (class carousel-none) jumps, "fade" (carousel-fade) does not scroll
 *     at all: the slides lie on one another and the current one is shown (aria-current).
 *   · duration, playing, play(), pause(), toggle() — playing moves on every duration (milliseconds, from
 *     <carousel duration="5s">; play() with none written takes 5 seconds). It starts playing when a duration
 *     is written, unless the viewer asked for less motion. It holds, without changing playing, while
 *     the pointer is over the carousel or the focus is inside it, while the page is hidden and while the
 *     carousel is off the screen; a slide reached any other way starts the time again.
 *   · On a Kitwork UI page, each slide's name, "2 / 5", and aria-current on the slide and the dot of
 *     the one showing — set here (a slide written with for has no place the server knows), again
 *     whenever the slides change. A documented exception to "a component writes no attribute": the
 *     place of each slide is the component's knowledge, like the toast's swipe.
 *   · count — how many slides there are: the server's first paint has it (the engine reads it off the
 *     page's data), and it follows the slides here.
 *   · a mouse's drag (20/10/2026, Quốc: dragged with the mouse, forward and back, it did not move): a
 *     finger and a pen have the browser's own swipe, a mouse has none. Pressed on the track (the main
 *     button, more than one slide, not while a move asked for slides) and moved more than DRAG pixels
 *     across, it is a drag: the track follows the mouse (no snap meanwhile, and none until the move it
 *     ends in rests: snap coming back half way would jump), and at either end of the row the slide
 *     beyond comes round to meet it, as next() brings it. Let go a fifth of a slide away or more, or
 *     in a flick (FLICK), it moves on in the drag's direction, the way next() and previous() go, to the
 *     slide nearest where it left the track (one on at least; at an end that does not come round it
 *     stays); otherwise it slides back. fade and none do not follow: 40 pixels across is next() or
 *     previous(), as fade's swipe. The click a drag ends with is no click (a link in a slide is not
 *     followed), and no text is selected nor an image dragged away; a press that does not move still
 *     clicks. While pressed it holds; the drag begins where the track is once it moves (the browser's
 *     own scroll may have moved it since the press, and still lands until then), and current stays the
 *     slide the drag began on until it lets go. A carousel inside another's slide takes its own press.
 *     The main button let go while another is held ends it; that other opens no menu meanwhile.
 *   · right to left (dir="rtl") is the mirror: the row starts at the right, the next slide comes from
 *     the left, a drag or a swipe to the right moves on, and in fade ← is next().
 */
var CAROUSEL = Symbol("kit:carousel");
var PLAY_FOR = 5000; // play() on a carousel with no duration written
// a move asked for has stopped once the track has not moved for this many frames short of its slide
// (a smooth scroll's first frames may not move yet), or two at it
var STILL = 10;
var DRAG = 5; // pixels across a mouse's press moves before it is a drag (less is a click)
var FLICK = 0.5; // pixels a millisecond over a drag's last 100ms: a flick moves on, however short

function own(scope) {
  return scope[CAROUSEL] || null;
}

// parts are the track and its slides, read live: the track is the engine's (class carousel-track) and
// its slides are the track's children the engine marked (aria-roledescription): the list's <empty>,
// shown when a for has nothing, is no slide
function parts(scope) {
  var mine = own(scope);
  if (!mine) return { track: null, slides: [] };
  var track = mine.context.owned(".carousel-track")[0] || null;
  var slides = !track ? [] : Array.prototype.filter.call(track.children, function (child) {
    return child.nodeType === 1 && child.hasAttribute("aria-roledescription");
  });
  return { track: track, slides: slides };
}

// sign is 1 on a page written left to right and -1 right to left (dir="rtl"): there the row starts at
// the right, the next slide lies to the left, the track scrolls below 0 (scrollLeft from 0 down to
// minus what it can scroll), and a drag, a swipe or an arrow to the right goes back
function sign(track) {
  return getComputedStyle(track).direction === "rtl" ? -1 : 1;
}

// place is how far a slide starts along the row: its left edge, or right to left its right edge, mirrored
function place(slide, rtl) {
  return rtl ? -(slide.offsetLeft + slide.offsetWidth) : slide.offsetLeft;
}

// spot is where the track rests to show slide i at its start, the way scroll-snap aligns it: its place
// on the row as it stands (from the row's start, the first slide's place: a slide that came round counts
// where it came round to), moved by the track's own padding and scroll-padding at the row's start,
// within what the track can scroll (free: not held within, for reachable), mirrored right to left
function spot(track, slides, i, free) {
  var style = getComputedStyle(track), rtl = style.direction === "rtl", side = rtl ? "Right" : "Left", first = Infinity;
  slides.forEach(function (slide) { first = Math.min(first, place(slide, rtl)); });
  var at = place(slides[i], rtl) - first + (parseFloat(style["padding" + side]) || 0) - (parseFloat(style["scrollPadding" + side]) || 0);
  if (!free) at = Math.max(0, Math.min(at, track.scrollWidth - track.clientWidth));
  return rtl ? -at : at;
}

// reachable is the last slide whose start the track can scroll to: with three cards a row, the last
// two are seen from the third last's place and have none of their own. The track's own padding at the
// row's start counts as reached: a padded track's last slide rests at the end of the row with its start
// where that padding ends, short of its own place by the padding (else next() skipped it, nothing came
// round)
function reachable(scope) {
  var p = parts(scope);
  if (!p.track || !p.slides.length || mode(scope) === "fade") return p.slides.length - 1;
  var style = getComputedStyle(p.track), last = 0;
  var end = p.track.scrollWidth - p.track.clientWidth + (parseFloat(style.direction === "rtl" ? style.paddingRight : style.paddingLeft) || 0) + 1;
  p.slides.forEach(function (slide, index) {
    if (Math.abs(spot(p.track, p.slides, index, true)) <= end) last = index;
  });
  return last;
}

// mode is how a slide gives way: "slide" (scrolls), "none" (jumps), "fade" (no scroll at all)
function mode(scope) {
  var mine = own(scope);
  var host = mine && mine.context.host;
  if (!host || !host.classList) return "slide";
  if (host.classList.contains("carousel-fade")) return "fade";
  if (host.classList.contains("carousel-none")) return "none";
  return "slide";
}

// showing is the slide whose start is nearest the track's scroll position
function showing(scope) {
  var p = parts(scope);
  if (!p.track || !p.slides.length) return 0;
  var best = 0, distance = Infinity;
  p.slides.forEach(function (slide, index) {
    var gap = Math.abs(spot(p.track, p.slides, index) - p.track.scrollLeft);
    if (gap < distance) { distance = gap; best = index; }
  });
  return best;
}

// round moves on from the last slide to the first (forward) or from the first to the last, the way the
// track always goes: that slide comes round (order), the view stays on the slide showing (the track
// jumps by one slide, at once), and the track slides on to it. home puts it back once the track rests.
function round(scope, forward) {
  var mine = own(scope), p = parts(scope), track = p.track, slides = p.slides;
  var to = forward ? 0 : slides.length - 1, from = forward ? slides.length - 1 : 0;
  still(scope, slides[to]);
  slides[to].style.order = forward ? "1" : "-1";
  track.scrollTo({ left: spot(track, slides, from), behavior: "instant" });
  mine.round = slides[to];
  mine.moved = false;
  return slide(scope, to);
}

// slide makes n current and slides the track to n's place on the row as it stands, the slide that came
// round still round: home puts it back once the track rests, or at once when the track is there at once
// (no smooth scroll: less motion asked for, or a track without it)
function slide(scope, n) {
  var mine = own(scope), track = parts(scope).track;
  mine.from = track.scrollLeft; // where the move starts: the jump a slide coming round makes is no move of it
  scope.current = n;
  mark(scope);
  mine.target = n;
  aim(scope);
  var slides = parts(scope).slides, left = spot(track, slides, n);
  // smooth by the track's CSS; at once on a hidden page, whose smooth scroll does not move until it shows
  track.scrollTo(document.hidden ? { left: left, behavior: "instant" } : { left: left });
  if (Math.abs(track.scrollLeft - left) < 1) {
    mine.target = null;
    home(scope);
  }
  schedule(scope);
  return n;
}

// busy is whether a move asked for is under way (from the press until the track rests at its slide):
// a press then is not taken
function busy(scope) {
  var mine = own(scope);
  return !!(mine && mine.target !== null);
}

// still keeps a slide's order from easing in: a slide whose transition covers every property (an
// author's transition on all, or less motion's 0.01ms on every element) would ease its order over that
// time, and the row read meanwhile would be the row as it was. The slide's own transition comes back
// once it is home and the row has been read (release).
function still(scope, element) {
  var style = element.style;
  own(scope).transition = [style.getPropertyValue("transition-property"), style.getPropertyPriority("transition-property")];
  style.setProperty("transition-property", "none", "important");
}
function release(scope, element) {
  var kept = own(scope).transition;
  if (kept && kept[0]) element.style.setProperty("transition-property", kept[0], kept[1]);
  else element.style.removeProperty("transition-property");
}

// circles is whether next() and previous() come round: the track slides (not fade, not none), and each
// slide has a place of its own (one a row)
function circles(scope) {
  var mine = own(scope), p = parts(scope);
  return !!(mine && p.track && p.slides.length > 1 && mode(scope) === "slide" && reachable(scope) === p.slides.length - 1);
}

// home puts back the slide that came round, the view kept on the slide showing: it jumps at once to
// that slide's own place, which on the screen is no move
function home(scope) {
  var mine = own(scope);
  if (!mine || !mine.round) return;
  var p = parts(scope), element = mine.round;
  var at = showing(scope);
  element.style.order = "";
  mine.round = null;
  // reading the slide's place lays the row out with the slide home, its transition still none
  if (p.track && p.slides[at]) p.track.scrollTo({ left: spot(p.track, p.slides, at), behavior: "instant" });
  release(scope, element);
}

// edge lets a mouse's drag at either end of the row go on round, as next() and previous() do: dragged
// forward from the last slide the first comes round after it, back from the first the last before it,
// and it goes home again when the drag turns back past where it began. The view is kept each time
// (where the drag holds the track moves by the slide that moved); home puts it back once the move rests.
function edge(scope, hand, dx) {
  var mine = own(scope), p = parts(scope), last = p.slides.length - 1, ahead = dx * hand.sign; // below 0: on
  var way = !hand.circles ? 0 : ahead < 0 && hand.at === last ? 1 : ahead > 0 && hand.at === 0 ? -1 : 0;
  if (way === hand.round) return;
  var element = mine.round || p.slides[way > 0 ? 0 : last], before = spot(p.track, p.slides, hand.at, true);
  if (way) still(scope, element);
  element.style.order = way > 0 ? "1" : way < 0 ? "-1" : "";
  mine.round = way ? element : null;
  hand.left += spot(p.track, p.slides, hand.at, true) - before;
  if (!way) release(scope, element);
  hand.round = way;
}

// grip is the track in a mouse's hand: the closed hand, no text selected, and (slide) no snap, which
// stays off until the move the drag ends in rests (loose): put back half way, it would jump
function grip(scope, on) {
  var mine = own(scope), style = parts(scope).track.style;
  ["cursor", "user-select", "-webkit-user-select"].forEach(function (name, index) {
    if (on) style.setProperty(name, index ? "none" : "grabbing");
    else style.removeProperty(name);
  });
  if (!on) return;
  if (window.getSelection) window.getSelection().removeAllRanges(); // what the press began to select
  if (mode(scope) !== "slide") return;
  style.setProperty("scroll-snap-type", "none");
  mine.unsnapped = true;
}
function loose(scope) {
  var mine = own(scope);
  if (!mine || !mine.unsnapped) return;
  mine.unsnapped = false;
  parts(scope).track.style.removeProperty("scroll-snap-type");
}

function wrap(n, length) {
  return ((Math.round(n) % length) + length) % length;
}

// dots are the buttons that go to one slide: on a Kitwork UI page the engine gave each its place as
// value and named the track in aria-controls
function dots(scope) {
  var mine = own(scope), track = parts(scope).track;
  if (!mine || !track || !track.id) return [];
  return mine.context.owned("[aria-controls][value]").filter(function (dot) {
    return dot.getAttribute("aria-controls") === track.id;
  });
}

// mark names each slide and marks the slide and the dot showing. count follows the slides.
function mark(scope) {
  var mine = own(scope);
  if (!mine) return;
  var slides = parts(scope).slides, total = slides.length, at = Number(scope.current) || 0;
  if (scope.count !== total) scope.count = total;
  var set = function (element, name, value) {
    if (element.getAttribute(name) !== value) element.setAttribute(name, value);
  };
  slides.forEach(function (slide, index) {
    // a name the author gave a slide (alt) stays; the engine's own ("2 / 5") follows the slides
    var label = slide.getAttribute("aria-label");
    if (label === null || /^\d+ \/ \d+$/.test(label)) set(slide, "aria-label", (index + 1) + " / " + total);
    set(slide, "aria-current", index === at ? "true" : "false");
  });
  dots(scope).forEach(function (dot) {
    set(dot, "aria-current", Number(dot.value) === at ? "true" : "false");
  });
}

// time is a duration the host gave, in milliseconds, or 0
function time(value) {
  var ms = Number(value);
  return isFinite(ms) && ms > 0 ? ms : 0;
}

// schedule starts the time of the slide showing again: the next one comes after duration, unless the
// carousel does not play, or holds (the pointer over it, the focus in it, the page hidden, off screen)
function schedule(scope) {
  var mine = own(scope);
  if (!mine) return;
  clearTimeout(mine.timer);
  mine.timer = 0;
  if (!scope.playing || mine.gone) return;
  if (mine.hover || mine.focus || mine.hidden || !mine.seen || mine.drag) return;
  mine.timer = setTimeout(function () {
    mine.timer = 0;
    // a move still under way (a page whose frames come slowly) would not take the press: the time
    // starts again instead, so the carousel goes on playing
    if (busy(scope)) schedule(scope);
    else scope.next();
  }, time(scope.duration) || PLAY_FOR);
}

// land makes current the slide the track rests at (nearest its scroll position), named and marked
function land(scope) {
  var now = showing(scope);
  if (scope.current !== now) {
    scope.current = now;
    mark(scope);
    schedule(scope);
  }
}

// aim watches a move asked for in the frames the browser draws, not in time: a page whose frames come
// slowly (busy, or drawn once a second because the browser keeps it in the background) waits as long as
// its scroll takes, so a slide that came round never goes home halfway, and the slides the move passes
// never count. Resting at the slide asked for (two frames), the move is done: home, then current is
// where the track rests. Stopped short of it for STILL frames (a hand took the track on the way, a place
// the track cannot reach), it is given up the same way, except that a move that never scrolled keeps
// the slide asked for. A move asked for while one is watched is watched by the same frames.
function aim(scope) {
  var mine = own(scope), track = parts(scope).track;
  if (!track) return;
  mine.still = 0;
  mine.last = track.scrollLeft;
  if (mine.watching) return;
  mine.watching = true;
  var frame = function () {
    if (mine.gone || mine.target === null) {
      mine.watching = false;
      return;
    }
    var left = track.scrollLeft;
    if (Math.abs(left - mine.last) > 0.01) {
      mine.still = 0;
      mine.last = left;
      if (Math.abs(left - mine.from) > 1) mine.moved = true; // a slide coming round jumps first: no move
    } else {
      mine.still++;
    }
    var there = rests(scope);
    if (mine.still >= (there ? 2 : STILL)) {
      mine.watching = false;
      mine.target = null;
      home(scope);
      loose(scope);
      if (there || mine.moved) land(scope);
      schedule(scope); // the slide reached stays its whole duration, from the moment it is there
      return;
    }
    requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}

// rests is whether the track stands at the place of the slide a move asked for, on the row as it stands
// (a slide that came round counted where it came round to)
function rests(scope) {
  var mine = own(scope), p = parts(scope);
  if (!mine || mine.target === null || !p.track || !p.slides[mine.target]) return false;
  return Math.abs(p.track.scrollLeft - spot(p.track, p.slides, mine.target)) < 1;
}

var carouselDef = {
  current: 0,
  count: 0,
  playing: false,
  duration: 0,

  go: function (index) {
    if (busy(this)) return this.current; // still sliding: the press is not taken
    var p = parts(this), n = Number(index);
    if (!p.slides.length || !isFinite(n)) return this.current;
    n = wrap(n, p.slides.length);
    this.current = n;
    var mine = own(this);
    if (!mine) return n;
    mark(this);
    var how = mode(this);
    if (how !== "fade" && p.track) {
      var left = spot(p.track, p.slides, n);
      if (Math.abs(p.track.scrollLeft - left) > 1) {
        // the slides passed on the way do not count: current stays the one asked for until it lands
        mine.target = n;
        mine.moved = false;
        mine.from = p.track.scrollLeft;
        aim(this);
        // none jumps; smooth by the track's CSS otherwise (at once with less motion), and at once on a
        // hidden page, whose smooth scroll does not move until it shows
        if (how === "none" || document.hidden) p.track.scrollTo({ left: left, behavior: "instant" });
        else p.track.scrollTo({ left: left });
        // there at once (none, less motion, hidden): no move under way, the next press is taken
        if (Math.abs(p.track.scrollLeft - left) < 1) mine.target = null;
      }
    }
    schedule(this);
    return n;
  },

  // past the last slide comes the first, the way next() always goes, and before the first the last
  // (round); with several slides a row, past the last place the track can reach comes the first, with
  // no press that moves nothing
  next: function () {
    if (busy(this)) return this.current; // still sliding: the press is not taken
    var at = Number(this.current) || 0;
    if (at < reachable(this)) return this.go(at + 1);
    if (circles(this)) return round(this, true);
    return this.go(0);
  },
  previous: function () {
    if (busy(this)) return this.current; // still sliding: the press is not taken
    var at = Number(this.current) || 0;
    if (at > 0) return this.go(at - 1);
    if (circles(this)) return round(this, false);
    return this.go(reachable(this));
  },

  play: function () {
    this.playing = true;
    schedule(this);
  },
  pause: function () {
    this.playing = false;
    schedule(this);
  },
  toggle: function () {
    if (this.playing) this.pause();
    else this.play();
  },

  init: function (context) {
    var scope = this;
    var mine = scope[CAROUSEL] = {
      context: context,
      timer: 0, target: null, moved: false, from: 0, round: null, transition: null,
      watching: false, still: 0, last: 0, drag: null, dragged: false, unsnapped: false,
      hover: false, focus: false, hidden: !!document.hidden, seen: true, gone: false
    };
    var p = parts(scope);
    var track = p.track;

    // where it starts: the slide the page chose (current="2"), at once, before anything is painted. Not
    // scrollLeft: the track's CSS scrolls smoothly, and the page would open on the first slide sliding
    // to that one (current passing the first on the way). The first slide too, where the track's padding
    // puts its place past 0: the browser would snap to it, smoothly, as the page opens
    var start = p.slides.length ? wrap(Number(scope.current) || 0, p.slides.length) : 0;
    scope.current = start;
    if (track && p.slides.length && mode(scope) !== "fade") {
      var at = spot(track, p.slides, start);
      if (Math.abs(track.scrollLeft - at) > 1) track.scrollTo({ left: at, behavior: "instant" });
    }
    mark(scope);

    // it plays by itself when a duration is written, unless the viewer asked for less motion
    var less = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)");
    scope.playing = time(scope.duration) > 0 && !(less && less.matches);

    if (track) {
      context.listen(track, "scroll", function () {
        // a drag's slide is the one it began on until it lets go; a press that has not moved yet is no
        // drag, and the browser's own scroll under it (a wheel's glide, an arrow key) still lands
        if (mode(scope) === "fade" || (mine.drag && mine.drag.moved)) return;
        // a move asked for is watched by its frames (aim): the slides it passes do not count. It has moved
        // once the track left where it began (a slide coming round jumps first, which is no move)
        if (mine.target !== null) {
          if (Math.abs(track.scrollLeft - mine.from) > 1) mine.moved = true;
          return;
        }
        land(scope);
      }, { passive: true });
      context.listen(track, "scrollend", function () {
        if (mode(scope) === "fade" || (mine.drag && mine.drag.moved)) return;
        // a move asked for that does not rest at its slide (a step of the way: a browser that draws a frame
        // now and then ends each; cut short) is the frames' to give up (aim), never a slide sent home halfway
        if (mine.target !== null && !rests(scope)) return;
        var asked = mine.target !== null;
        mine.target = null;
        home(scope);
        loose(scope);
        land(scope);
        if (asked) schedule(scope); // the slide reached stays its whole duration, from the moment it is there
      });
      // fade does not scroll: the arrow keys and a swipe move it here, towards the row's end on
      context.listen(track, "keydown", function (event) {
        if (mode(scope) !== "fade" || event.altKey || event.ctrlKey || event.metaKey) return;
        var way = event.key === "ArrowRight" ? sign(track) : event.key === "ArrowLeft" ? -sign(track) : 0;
        if (!way) return;
        event.preventDefault();
        if (way > 0) scope.next(); else scope.previous();
      });
      var swipe = null;
      context.listen(track, "pointerdown", function (event) {
        if (mode(scope) !== "fade" || event.pointerType === "mouse") return;
        swipe = { x: event.clientX, id: event.pointerId };
      });
      context.listen(track, "pointerup", function (event) {
        if (!swipe || event.pointerId !== swipe.id) return;
        var dx = (event.clientX - swipe.x) * sign(track);
        swipe = null;
        if (dx <= -40) scope.next();
        else if (dx >= 40) scope.previous();
      });
      // a mouse drags it (the comment at the top): a press on the track, a drag once it moves DRAG pixels
      // across, and its last 100ms kept (trail) to tell a flick. The press is the nearest carousel's: one
      // inside another's slide takes it (event[CAROUSEL]), and the one around it does not drag too
      context.listen(track, "pointerdown", function (event) {
        if (event.pointerType !== "mouse" || event.button !== 0 || event[CAROUSEL]) return;
        mine.drag = null;
        if (event.target.closest && event.target.closest("input, textarea, select, [contenteditable]")) return; // a field's own
        var sliding = busy(scope); // a press while it slides is not taken (nor by a carousel around it)
        if (!sliding && parts(scope).slides.length < 2) return; // nothing to drag: a carousel around it may take it
        event[CAROUSEL] = true;
        if (sliding) return;
        mine.drag = { id: event.pointerId, x: event.clientX, moved: false, round: 0, trail: [[performance.now(), event.clientX]] };
        schedule(scope); // it holds while pressed
      });
      var follow = function (hand, x) {
        var now = performance.now(), dx = x - hand.x;
        hand.trail.push([now, x]);
        while (hand.trail.length > 1 && now - hand.trail[0][0] > 100) hand.trail.shift();
        if (!hand.moved && Math.abs(dx) > DRAG) {
          hand.moved = true;
          // from where the track is now, and the slide showing now: the browser's own scroll (a wheel's
          // glide, an arrow key) may have moved it since the press, and landed as it went (fade has none)
          if (mode(scope) !== "fade") land(scope);
          hand.left = track.scrollLeft; hand.at = Number(scope.current) || 0; hand.circles = circles(scope); hand.sign = sign(track);
          try { track.setPointerCapture(hand.id); } catch (problem) { /* the press is over */ }
          grip(scope, true);
        }
        if (hand.moved && mode(scope) === "slide") {
          edge(scope, hand, dx);
          track.scrollTo({ left: hand.left - dx, behavior: "instant" });
        }
        return dx;
      };
      context.listen(track, "pointermove", function (event) {
        if (!mine.drag || event.pointerId !== mine.drag.id) return;
        // the main button let go while another is held comes as a move without it, not as a pointerup
        if (event.buttons & 1) follow(mine.drag, event.clientX);
        else letGo(event);
      });
      // let go (or the browser took the pointer): a fifth of a slide or a flick moves on in the drag's
      // direction (forward from the last slide round to the first, the slide that came round already
      // there) to the slide nearest where it left the track, one on at least: a drag across two slides
      // goes on to the second, never back against the drag, and where nothing comes round (several a
      // row) the end stays the end; less slides back to where it began; fade and none move on at 40 pixels
      var letGo = function (event) {
        var hand = mine.drag;
        if (!hand || event.pointerId !== hand.id) return;
        var dx = follow(hand, event.type === "pointercancel" || event.type === "lostpointercapture" ? hand.trail[hand.trail.length - 1][1] : event.clientX);
        mine.drag = null;
        if (!hand.moved) return schedule(scope); // a click
        grip(scope, false);
        mine.dragged = true; // the click that comes with this release is no click
        setTimeout(function () { mine.dragged = false; }, 0);
        var p = parts(scope), first = hand.trail[0], last = hand.trail[hand.trail.length - 1];
        var speed = (last[1] - first[1]) / Math.max(last[0] - first[0], 1);
        var far = mode(scope) !== "slide" ? Math.abs(dx) >= 40 :
          Math.abs(dx) >= (p.slides[hand.at] || track).getBoundingClientRect().width / 5 || (Math.abs(speed) >= FLICK && speed * dx > 0);
        var step = !far ? 0 : dx * hand.sign < 0 ? 1 : -1;
        mine.moved = false;
        if (hand.round) slide(scope, !step ? hand.at : step > 0 ? 0 : p.slides.length - 1);
        else if (step && mode(scope) !== "slide") { if (step > 0) scope.next(); else scope.previous(); }
        else scope.go(!step ? hand.at : step > 0 ? Math.min(Math.max(hand.at + 1, showing(scope)), reachable(scope)) : Math.max(Math.min(hand.at - 1, showing(scope)), 0));
        if (!busy(scope)) loose(scope);
      };
      context.listen(document, "pointerup", letGo);
      context.listen(document, "pointercancel", letGo);
      context.listen(track, "lostpointercapture", letGo);
      // a second button pressed while it drags opens no menu there (Windows opens it as that one is let go)
      context.listen(document, "contextmenu", function (event) { if (mine.drag && mine.drag.moved) event.preventDefault(); });
      context.listen(track, "click", function (event) {
        if (!mine.dragged) return;
        mine.dragged = false;
        event.preventDefault();
        event.stopPropagation();
      }, true);
      context.listen(track, "dragstart", function (event) { if (mine.drag) event.preventDefault(); });
      // a press while a move asked for slides is not taken, and selects nothing: the browser brings the
      // words it selects into view, which would cut the move short
      context.listen(track, "selectstart", function (event) { if (busy(scope)) event.preventDefault(); });
      // a slide written with for, painted again: named and marked again
      if (window.MutationObserver) {
        var watch = new MutationObserver(function () { mark(scope); });
        watch.observe(track, { childList: true });
        context.cleanup(function () { watch.disconnect(); });
      }
    }

    // what holds it, without changing playing
    var host = context.host;
    var hold = function (key, value) {
      if (mine[key] === value) return;
      mine[key] = value;
      schedule(scope);
    };
    context.listen(host, "pointerenter", function (event) { if (event.pointerType === "mouse") hold("hover", true); });
    context.listen(host, "pointerleave", function (event) { if (event.pointerType === "mouse") hold("hover", false); });
    // the keyboard's focus holds it (a person reading or moving through it); a click focuses a button
    // too, and would hold it until the focus left, so that one does not
    context.listen(host, "focusin", function (event) {
      var target = event.target;
      var seen = false;
      try { seen = !!(target && target.matches && target.matches(":focus-visible")); } catch (problem) { seen = true; }
      if (seen) hold("focus", true);
    });
    context.listen(host, "focusout", function (event) {
      if (!event.relatedTarget || !host.contains(event.relatedTarget)) hold("focus", false);
    });
    context.listen(document, "visibilitychange", function () { hold("hidden", !!document.hidden); });
    if (window.IntersectionObserver) {
      var sight = new IntersectionObserver(function (entries) {
        hold("seen", entries[entries.length - 1].isIntersecting);
      });
      sight.observe(host);
      context.cleanup(function () { sight.disconnect(); });
    }

    context.cleanup(function () {
      mine.gone = true;
      clearTimeout(mine.timer);
      if (mine.round) {
        mine.round.style.order = "";
        release(scope, mine.round);
      }
    });
    schedule(scope);
  }
};

window.kitwork.component("carousel", carouselDef);
