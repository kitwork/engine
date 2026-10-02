/* carousel component @v1.0.0 — slides on a scroll-snap track, and which one is showing.
 *
 * Usage — the browser moves the track; the markup draws the dots from `active`:
 *
 *   <div data-kit-component="carousel" role="group" aria-roledescription="carousel" aria-label="Highlights">
 *     <div data-kit-element="track" tabindex="0" aria-label="Slides"
 *          class="flex snap-x snap-mandatory overflow-x-auto scroll-smooth motion-reduce:scroll-auto">
 *       <div data-kit-element="slide" role="group" aria-roledescription="slide" aria-label="1 of 3"
 *            class="w-full shrink-0 snap-start">…</div>
 *       …
 *     </div>
 *     <button data-kit-click="previous()" aria-label="Previous slide">‹</button>
 *     <button role="tab" data-kit-click="go(0)" data-kit-bind:aria-selected="active === 0 ? 'true' : 'false'">
 *     <button data-kit-click="next()" aria-label="Next slide">›</button>
 *   </div>
 *
 * THE BROWSER DOES THE MOVING. A track with scroll-snap already swipes on a phone, scrolls on a
 * trackpad, steps with the arrow keys once focused, carries momentum, and honours reduced motion
 * through CSS (scroll-smooth / motion-reduce:scroll-auto). The KitJS carousel reimplemented all of
 * that with transforms, transitionend and cloned slides — 296 lines. None of it is needed.
 *
 * WHY THERE IS STILL A COMPONENT. Two things no markup can do:
 *   · ANSWER which slide is showing. Scroll position is a fact the browser owns and reports only
 *     by event; `active` turns it into an index the dots and labels can read.
 *   · SCROLL to a slide when a button asks — go(n), next(), previous(). next() from the last slide
 *     comes back to the first, and previous() from the first goes to the last.
 *
 * What is deliberately gone: the seamless ring. Going round now scrolls back across the track
 * instead of sliding on through a copy — copies meant duplicated slides for screen readers, hidden
 * again with aria-hidden, and ids stripped so they would not collide.
 *
 * The public surface: active, count, go(n), next(), previous().
 */
var CAROUSEL = Symbol("kit:carousel");

function carouselPart(scope, name) {
  var own = scope[CAROUSEL];
  return own ? own.context.element(name) : null;
}

function carouselSlides(scope) {
  var own = scope[CAROUSEL];
  return own ? own.context.elements("slide") : [];
}

// The slide whose start is nearest the track's scroll position — correct for any slide width.
function showing(scope) {
  var track = carouselPart(scope, "track"), slides = carouselSlides(scope);
  if (!track || !slides.length) return 0;
  var origin = slides[0].offsetLeft, best = 0, distance = Infinity;
  slides.forEach(function (slide, index) {
    var gap = Math.abs(slide.offsetLeft - origin - track.scrollLeft);
    if (gap < distance) { distance = gap; best = index; }
  });
  return best;
}

var carouselDef = {
  active: 0,

  get count() {
    return carouselSlides(this).length;
  },

  go: function (index) {
    var slides = carouselSlides(this), track = carouselPart(this, "track");
    var n = Number(index);
    if (!track || !slides.length || !isFinite(n)) return this.active;
    n = ((Math.round(n) % slides.length) + slides.length) % slides.length;
    // The dots follow at once; the scroll event that arrives when the track settles agrees.
    this.active = n;
    // No `behavior` here: scroll-smooth and motion-reduce on the track decide how it moves.
    track.scrollTo({ left: slides[n].offsetLeft - slides[0].offsetLeft });
    return n;
  },

  next: function () { return this.go(this.active + 1); },
  previous: function () { return this.go(this.active - 1); },

  init: function (context) {
    var scope = this;
    scope[CAROUSEL] = { context: context };
    context.require("track");
    context.require("slide");
    context.cleanup(function () { delete scope[CAROUSEL]; });
    var track = context.element("track");
    if (!track) return;
    context.listen(track, "scroll", function () {
      var now = showing(scope);
      if (scope.active !== now) scope.active = now;
    }, { passive: true });
  }
};

window.kit.component("carousel", carouselDef);
window.kit.component("carousel@v1.0.0", carouselDef);
