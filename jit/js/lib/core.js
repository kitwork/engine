/* kitwork.components — core delegated dispatcher for jitjs. Set up exactly once; safe to re-run
 * (Kitwork Drive re-executes this block on swap, and the guard prevents a second document listener).
 * Verb modules (copy.js, toggle.js, more.js, …) are concatenated after this and register themselves. */
(function () {
  var kitwork = (window.kitwork = window.kitwork || {});
  if (kitwork.components) return;
  var actions = {};

  // Per-element runtime state lives behind a PRIVATE Symbol, so it can never collide with a host
  // page's own element properties and never appears in their Object.keys / for-in / JSON. Call
  // `kitwork.components.state(element)` to get (creating once) that element's state object; verbs
  // then read/write descriptive fields on it (visibilityObserver, isLoading, fetchController, …).
  var stateKey = Symbol("kitwork");
  function state(element) {
    return element[stateKey] || (element[stateKey] = {});
  }

  // data-kitwork-target = "#id"/selector → element; defaults to the actor itself.
  function target(el) {
    var sel = el.getAttribute("data-kitwork-target");
    return sel ? document.querySelector(sel) : el;
  }
  // Run the nearest [data-kitwork-action]'s verb.
  function fire(el, e) {
    var fn = actions[el.getAttribute("data-kitwork-action")];
    if (fn) fn(el, e);
  }

  kitwork.components = {
    actions: actions,
    target: target,
    state: state,
    fire: fire,
    action: function (name, fn) { actions[name] = fn; return this; }
  };

  // Delegated click → run the nearest [data-kitwork-action] (resolves inner <i>/<span> too).
  document.addEventListener("click", function (e) {
    var el = e.target.closest && e.target.closest("[data-kitwork-action]");
    if (el) fire(el, e);
  });

  // Delegated submit → run the form's own [data-kitwork-action] (e.g. the `submit` verb).
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (form && form.getAttribute && form.getAttribute("data-kitwork-action")) fire(form, e);
  });

  // Auto-trigger: [data-kitwork-trigger="visible"] fires its action when scrolled into view (lazy
  // load / infinite scroll). Re-evaluated on every kitwork:load (after navigation or an append), so a
  // sentinel still in view keeps loading the next page; a removed sentinel stops.
  function bindVisible() {
    if (!("IntersectionObserver" in window)) return;
    document.querySelectorAll('[data-kitwork-trigger="visible"]').forEach(function (el) {
      var store = state(el);
      if (store.visibilityObserver) store.visibilityObserver.disconnect();
      var observer = new IntersectionObserver(function (entries) {
        if (entries[0].isIntersecting) fire(el, null);
      }, { rootMargin: "300px" });
      store.visibilityObserver = observer;
      observer.observe(el);
    });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", bindVisible);
  else bindVisible();
  document.addEventListener("kitwork:load", bindVisible);
})();
