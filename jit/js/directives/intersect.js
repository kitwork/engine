;(function (window, document) {
  "use strict";
  // data-kit-intersect — run an expression when an element enters the screen, or leaves it.
  //
  //   <section data-kit-intersect:once="$impact.start()">           the first time it shows
  //   <video data-kit-intersect="$this.play()"
  //          data-kit-intersect:leave="$this.pause()">             every time it comes and goes
  //   <div data-kit-intersect:half="…">  <div data-kit-intersect:margin(-20%)="…">
  //
  // The kernel knows only the attribute's NAME and runs its expression through the ordinary event
  // pipeline (:once and the rest). This file is the eyes: one IntersectionObserver per attribute,
  // dispatching an event named after the attribute on the element. The engine delivers it only to
  // a page that writes data-kit-intersect, so no other page carries an observer.
  //
  // The observer's modifiers:
  //   :leave        run when the element is NOT on screen — including the first report, so a reveal
  //                 can tell "not seen yet" from "not known yet"
  //   :half :full   how much must show: half of it, or all of it (0.99, as a sub-pixel never reaches 1)
  //   :margin(x)    grow or shrink the screen before the test, as CSS margin: :margin(-20%)
  // A browser without IntersectionObserver answers "on screen" once: content that waits to be seen
  // must never wait forever.
  //
  // A Kitwork UI page writes the same thing as pairs of data-kitwork-event: intersect-enter[.mods]
  // and intersect-leave[.mods] ("intersect-enter.once: start(); intersect-leave: stop()"). Each pair
  // is watched like an attribute and dispatched under the pair's own name, which is the event the
  // kernel listens for; its modifiers follow '.' (a dot inside margin(…) is the argument's, as in
  // margin(-1.5rem)), and intersect-leave means :leave. A pair's name has no '%' or space, so a
  // percentage margin is the hand-written form's alone.
  var kit = window.kit;
  if (!kit || kit.intersect) return;
  var HEAD = "data-kit-intersect";
  var watching = new WeakMap(); // element → { attribute or pair name: observer }

  function modifiersOf(name) {
    return name.split(name.indexOf(HEAD) === 0 ? ":" : /\.(?![^(]*\))/).slice(1);
  }

  function optionsOf(name) {
    var options = { threshold: 0 };
    modifiersOf(name).forEach(function (modifier) {
      if (modifier === "half") options.threshold = 0.5;
      else if (modifier === "full") options.threshold = 0.99;
      else if (/^margin\(.+\)$/.test(modifier)) options.rootMargin = modifier.slice(7, -1);
    });
    return options;
  }

  function announce(element, name) {
    element.dispatchEvent(new CustomEvent(name, { bubbles: true }));
  }

  function watch(element, name) {
    var modifiers = modifiersOf(name);
    var leave = modifiers.indexOf("leave") >= 0 || name.indexOf("intersect-leave") === 0, once = modifiers.indexOf("once") >= 0;
    if (typeof window.IntersectionObserver !== "function") {
      if (!leave) announce(element, name);
      return null;
    }
    var options = optionsOf(name), observer;
    try {
      observer = new window.IntersectionObserver(function (entries) {
        var entry = entries[entries.length - 1];
        var inside = !!entry && entry.isIntersecting && entry.intersectionRatio >= options.threshold;
        if (inside === leave) return;
        announce(element, name);
        if (once) observer.disconnect();
      }, options);
    } catch (error) {
      // A margin the browser cannot parse is the author's mistake; content must still show.
      if (!leave) announce(element, name);
      return null;
    }
    observer.observe(element);
    return observer;
  }

  // scan matches each element's intersect attributes with its observers: a new attribute is
  // watched, one that left (a morph rewrote the element) is released.
  function scan(root) {
    if (!root || root.nodeType !== 1) return;
    [root].concat(Array.prototype.slice.call(root.querySelectorAll("*"))).forEach(function (element) {
      var names = element.getAttributeNames().filter(function (name) { return name.indexOf(HEAD) === 0; });
      // (a value the kernel cannot read is reported there; here it watches nothing)
      try {
        kit.internal.grouped.pairs(element.getAttribute("data-kitwork-event") || "").forEach(function (pair) {
          if (/^intersect-(enter|leave)(\.|$)/.test(pair[0])) names.push(pair[0]);
        });
      } catch (error) { }
      var current = watching.get(element);
      if (!names.length && !current) return;
      if (!current) {
        current = {};
        watching.set(element, current);
        kit.onCleanup(element, function () {
          for (var name in current) if (current[name]) current[name].disconnect();
          watching.delete(element);
        });
      }
      for (var name in current) {
        if (names.indexOf(name) < 0) {
          if (current[name]) current[name].disconnect();
          delete current[name];
        }
      }
      names.forEach(function (name) {
        if (!(name in current)) current[name] = watch(element, name);
      });
    });
  }

  // Start once the kernel has painted the page — the kernel has learnt to listen for each attribute
  // by then — and look again whenever the page changes under it: a Drive swap, a row data-kit-for
  // renders, a branch data-kit-if mounts.
  // Nodes only, not attributes: a binding writes attributes on every paint, and a rescan per paint
  // would be the whole page each time. A morph that rewrites an element's attributes ends in
  // kitwork:load, which rescans too.
  kit.onStart(function () {
    var pending = false;
    var later = function () {
      if (pending) return;
      pending = true;
      setTimeout(function () { pending = false; scan(document.documentElement); }, 0);
    };
    scan(document.documentElement);
    new MutationObserver(later).observe(document.documentElement, { childList: true, subtree: true });
    document.addEventListener("kitwork:load", later);
  });
  kit.intersect = { scan: scan };
})(window, document);
