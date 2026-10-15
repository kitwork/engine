// scrolled.<hash>.js: the scrolled: state of a Kitwork UI page (KITWORK_UI §8). It keeps one flag on
// <html>, data-kitwork-scrolled, while the page is scrolled more than 8px away from its top, so a
// wobble at the very top (an elastic overscroll, a restore a pixel off) does not flicker it; the
// page's stylesheet reads it, :where(:root[data-kitwork-scrolled] *). The engine links it only to a
// page whose HTML can carry a scrolled: class; it needs no kernel.
//
// It never reads layout itself. Until 14/10/2026 it read scrollY once at the start, and that one read
// made the browser lay the whole page out inside this script, before its first paint (66–137 ms on
// the kitwork example, Lighthouse's forced reflow). Now the browser says when the page leaves its top:
// an IntersectionObserver watches a mark, a strip 1px wide over the page's top 8px, and the flag is on
// while no part of the strip is in view. A strip whose bottom edge touches the top of the viewport
// still counts as in view, so the flag turns on past 8px, as before. The browser works it out in its
// own rendering step, after the layout it does anyway: once at the start (a reload in the middle of a
// page, a jump to an #anchor), then each time the strip leaves or comes back, never on a scroll that
// leaves it where it was. No scroll listener. An elastic overscroll at the top keeps the strip in
// view: no flicker.
//
// The observer's root is the document, so the strip is measured against this page's own viewport,
// as scrollY was: a page shown in a frame keeps its flag off while the page around the frame scrolls
// the frame's top out of the window (the implicit root is the top window's viewport, and would turn it
// on). A browser that takes only an element for a root (before 2020) throws; it falls back to the
// implicit root, right for every page that is not in a frame.
//
// The mark is the script's own element, a child of <html> after <body>, placed absolutely at the top
// of the page (its containing block is the initial one, whatever <body> does), hidden and outside the
// accessibility tree: it takes no room, catches no pointer, is never focused and never printed. The
// script makes it, not the engine, because it must outlive every Drive move: Drive changes <main>
// (or the page's shell, inside <body>) and <head>, never the other children of <html>, so the mark and
// its observer stay for the life of the page; a move scrolls the page it shows, and the strip follows.
//
// Without IntersectionObserver (before 2019), a passive scroll listener reads scrollY as before (a
// browser delivers scroll at most once a frame), its first read in the first animation frame, after
// the script, where the browser lays the page out anyway.
//
// Served compacted: comment lines and indentation dropped (stylesheet.go, ScrolledScript).
(function (window, document) {
  "use strict";
  var root = document.documentElement;
  function flag(scrolled) {
    if (scrolled === root.hasAttribute("data-kitwork-scrolled")) return;
    if (scrolled) root.setAttribute("data-kitwork-scrolled", "");
    else root.removeAttribute("data-kitwork-scrolled");
  }
  if (window.IntersectionObserver) {
    var mark = document.createElement("div");
    mark.setAttribute("data-kitwork-generated", "scrolled");
    mark.setAttribute("aria-hidden", "true");
    mark.style.cssText = "position:absolute;top:0;left:0;width:1px;height:8px;visibility:hidden";
    root.appendChild(mark);
    var report = function (entries) {
      flag(!entries[entries.length - 1].isIntersecting);
    };
    var observer;
    try {
      observer = new IntersectionObserver(report, { root: document });
    } catch (error) {
      observer = new IntersectionObserver(report);
    }
    observer.observe(mark);
    return;
  }
  function update() {
    flag((window.scrollY || root.scrollTop || 0) > 8);
  }
  window.addEventListener("scroll", update, { passive: true });
  window.requestAnimationFrame(update);
})(window, document);
