;(function () {
"use strict";

// The carousel, second edition: the track goes round. 1.0.0 was arithmetic and
// the markup moved the track, so next from the last slide slid all the way back
// to the first — the reverse of where the reader was going. This one owns the
// track: it puts a copy of the last slide before the first and a copy of the
// first after the last, slides one step in the pressed direction every time,
// and when the step lands on a copy it snaps, with no transition, to the real
// slide in the same place. The eye sees a ring.
//
//   <div data-kit-component="carousel" role="group" aria-roledescription="carousel">
//     <div class="overflow-hidden">
//       <div data-carousel-track class="flex transition-transform duration-500">
//         <div data-carousel-slide class="w-full shrink-0">…</div>
//         <div data-carousel-slide class="w-full shrink-0">…</div>
//     <button data-kit-click="previous()">‹</button>
//     <button data-kit-click="select(1)" data-kit-bind:aria-selected="isActive(1)"></button>
//     <button data-kit-click="next()">›</button>
//
// The slides are authored; `active` is the real index the dots bind to; a
// swipe of the pointer and the arrow keys on the track move it too. With
// loop: false the ends are ends and the copies are never made.

var INSTANCE = Symbol("kit:carousel");
var SWIPE = 40;

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function track(data) {
  return data ? data.context.owned("[data-carousel-track]")[0] || null : null;
}

function realSlides(data) {
  return data ? data.context.owned("[data-carousel-slide]").filter(function (slide) {
    return !slide.hasAttribute("data-carousel-clone");
  }) : [];
}

function count(scope, data) {
  var slides = realSlides(data);
  if (slides.length) return slides.length;
  return Array.isArray(scope.slides) ? scope.slides.length : 0;
}

function looping(scope, data) {
  return scope.loop !== false && realSlides(data).length > 1;
}

function indexValue(value, length) {
  value = Number(value);
  return Number.isInteger(value) && value >= 0 && value < length ? value : -1;
}

// The track slot the real slide `index` sits in: one further along when the
// copy of the last slide leads the track.
function slotOf(scope, data, index) {
  return looping(scope, data) ? index + 1 : index;
}

function clone(slide) {
  var copy = slide.cloneNode(true);
  copy.setAttribute("data-carousel-clone", "");
  copy.setAttribute("aria-hidden", "true");
  copy.removeAttribute("id");
  copy.querySelectorAll("[id]").forEach(function (element) { element.removeAttribute("id"); });
  copy.querySelectorAll("a, button, input, select, textarea, [tabindex]").forEach(function (element) {
    element.setAttribute("tabindex", "-1");
  });
  return copy;
}

// Put the copies in place once; a Morph that rebuilt the track gets them again.
function prepare(scope, data) {
  var rail = track(data);
  if (!rail) return;
  var copies = rail.querySelectorAll("[data-carousel-clone]");
  var slides = realSlides(data);
  if (!looping(scope, data)) {
    copies.forEach(function (copy) { copy.remove(); });
    return;
  }
  if (copies.length === 2 && copies[0] === rail.firstElementChild && copies[1] === rail.lastElementChild) return;
  copies.forEach(function (copy) { copy.remove(); });
  rail.insertBefore(clone(slides[slides.length - 1]), rail.firstChild);
  rail.appendChild(clone(slides[0]));
}

function transform(rail, slot) {
  rail.style.transform = "translateX(" + (slot === 0 ? "0" : "-" + slot * 100 + "%") + ")";
}

function place(rail, slot) {
  rail.style.transition = "none";
  transform(rail, slot);
  void rail.offsetWidth; // commit the still frame before the transition returns
  rail.style.transition = "";
}

function durationOf(rail) {
  var view = rail.ownerDocument.defaultView;
  var raw = view && view.getComputedStyle ? view.getComputedStyle(rail).transitionDuration : "";
  var first = String(raw || "").split(",")[0].trim();
  if (!first) return 0;
  var seconds = parseFloat(first);
  if (!Number.isFinite(seconds)) return 0;
  return first.indexOf("ms") >= 0 ? seconds : seconds * 1000;
}

// Every slide but the active one is hidden from readers; copies always are.
function mark(scope, data) {
  var total = count(scope, data);
  var active = indexValue(scope.active, total);
  realSlides(data).forEach(function (slide, index) {
    slide.setAttribute("aria-hidden", index === active ? "false" : "true");
    slide.setAttribute("data-state", index === active ? "active" : "idle");
  });
}

// The step has landed: if on a copy, snap to the real slide it stands for.
function settle(scope, data, token) {
  if (data.disposed || data.token !== token) return;
  data.token = 0;
  var rail = track(data);
  if (!rail) return;
  var total = realSlides(data).length;
  if (looping(scope, data)) {
    if (data.slot === 0) { data.slot = total; place(rail, data.slot); }
    else if (data.slot === total + 1) { data.slot = 1; place(rail, data.slot); }
  }
  if (scope.moving) scope.moving = false;
}

// Slide the track to a slot, then settle; a move already under way finishes
// on the spot first so the new one starts from a real slide.
function travel(scope, data, slot) {
  var rail = track(data);
  if (!rail || data.disposed) return;
  if (data.token) settle(scope, data, data.token);
  if (slot === data.slot) return;
  data.slot = slot;
  var token = ++data.sequence;
  data.token = token;
  if (!scope.moving) scope.moving = true;
  transform(rail, slot);
  var wait = durationOf(rail);
  if (wait <= 0) { settle(scope, data, token); return; }
  var done = function (event) {
    if (event && event.target !== rail) return;
    rail.removeEventListener("transitionend", done);
    settle(scope, data, token);
  };
  rail.addEventListener("transitionend", done);
  // The event never comes when the tab is hidden or the style changed under us.
  data.context.host.ownerDocument.defaultView.setTimeout(function () {
    rail.removeEventListener("transitionend", done);
    settle(scope, data, token);
  }, wait + 50);
}

// Go to a real index; direction says which way round when looping.
function go(scope, data, index, direction) {
  var total = count(scope, data);
  index = indexValue(index, total);
  if (index < 0 || !data) return scope.active;
  var current = indexValue(scope.active, total);
  scope.active = index;
  mark(scope, data);
  var rail = track(data);
  if (!rail) return scope.active;
  var slot = slotOf(scope, data, index);
  if (looping(scope, data)) {
    if (direction > 0 && index === 0 && current === total - 1) slot = total + 1; // onto the copy of the first
    else if (direction < 0 && index === total - 1 && current === 0) slot = 0;  // onto the copy of the last
  }
  travel(scope, data, slot);
  return scope.active;
}

kit.component("carousel", {
  slides: [],
  active: 0,
  loop: true,
  moving: false,

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, slot: -1, token: 0, sequence: 0, pointer: null, start: 0, swiped: false };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    prepare(scope, data);
    var total = count(scope, data);
    var active = indexValue(scope.active, total);
    if (active < 0) { active = 0; scope.active = 0; }
    var rail = track(data);
    if (rail) {
      data.slot = slotOf(scope, data, active);
      place(rail, data.slot);
    }
    mark(scope, data);

    // A horizontal swipe on the track is next or previous.
    context.listen(context.host, "pointerdown", function (event) {
      var rail = track(data);
      if (!rail || !rail.contains(event.target) || event.button !== 0) return;
      data.pointer = event.pointerId;
      data.start = event.clientX;
      data.swiped = false;
    });
    context.listen(context.host, "pointermove", function (event) {
      if (data.pointer === null || event.pointerId !== data.pointer || data.swiped) return;
      var delta = event.clientX - data.start;
      if (Math.abs(delta) < SWIPE) return;
      data.swiped = true;
      if (delta < 0) scope.next(); else scope.previous();
    });
    var release = function (event) {
      if (data.pointer === null || event.pointerId !== data.pointer) return;
      data.pointer = null;
    };
    context.listen(context.host, "pointerup", release);
    context.listen(context.host, "pointercancel", release);

    context.listen(context.host, "keydown", function (event) {
      var rail = track(data);
      if (!rail || !rail.contains(event.target)) return;
      if (event.key === "ArrowRight") scope.next();
      else if (event.key === "ArrowLeft") scope.previous();
      else if (event.key === "Home") scope.first();
      else if (event.key === "End") scope.last();
      else return;
      event.preventDefault();
    });

    function afterRender() {
      if (data.disposed) return;
      prepare(scope, data);
      var rail = track(data);
      if (rail && !data.token) {
        var wanted = slotOf(scope, data, indexValue(scope.active, count(scope, data)));
        if (wanted !== data.slot && wanted >= 0) { data.slot = wanted; place(rail, wanted); }
      }
      mark(scope, data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { data.disposed = true; });
  },

  select: function (index) {
    var data = instance(this);
    var total = count(this, data);
    var selected = indexValue(index, total);
    if (selected < 0) {
      var current = indexValue(this.active, total);
      return current < 0 ? false : current;
    }
    return go(this, data, selected, 0);
  },

  next: function () {
    var data = instance(this);
    var total = count(this, data);
    if (!total) return false;
    var current = indexValue(this.active, total);
    var target = current < 0 || current + 1 >= total ? 0 : current + 1;
    if (target === 0 && current === total - 1 && this.loop === false) return this.active;
    return go(this, data, target, 1);
  },

  previous: function () {
    var data = instance(this);
    var total = count(this, data);
    if (!total) return false;
    var current = indexValue(this.active, total);
    var target = current <= 0 ? total - 1 : current - 1;
    if (target === total - 1 && current === 0 && this.loop === false) return this.active;
    return go(this, data, target, -1);
  },

  first: function () { return this.select(0); },
  last: function () {
    var total = count(this, instance(this));
    return total ? this.select(total - 1) : false;
  },

  isActive: function (index) {
    var total = count(this, instance(this));
    var selected = indexValue(index, total);
    return selected >= 0 && selected === indexValue(this.active, total);
  },
  count: function () { return count(this, instance(this)); }
});

})();
