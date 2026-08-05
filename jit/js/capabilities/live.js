// live: data-kit-live="<sse-url>" — the server PUSHES JSON scope patches over SSE.
//
// A CAPABILITY MODULE, not part of the always-shipped core. jit/js appends it — only — to a page that
// carries data-kit-live, and it installs through the kit.internal seam (boundaryScope / pageScope /
// render / scopeSelector / onReconcile / onDestroy). Still ONE EventSource per URL (deduped), but the
// patch is delivered to each subscriber's NEAREST scope: a live region on a boundary (data-kit-api /
// -scope / -component) patches THAT scope, so api (fetch initial) + live (keep fresh) pair on one
// element; a bare live region patches the page. A payload that parses as a JSON object is merged and
// re-rendered; anything else is ignored.
(function () {
  var kit = window.kit || window.kitwork;
  if (!kit || !kit.internal || !kit.internal.onReconcile) return;
  if (kit.sync) return; // already installed

  var boundaryScope = kit.internal.boundaryScope;
  var raw = kit.internal.pageScope;
  var render = kit.internal.render;
  var SCOPE = kit.internal.scopeSelector;
  var LIVE = "[data-kitwork-live],[data-kit-live]";
  var streams = {};

  function liveTarget(el) {
    var b = el.closest ? el.closest(SCOPE) : null;
    return b ? boundaryScope(b) : raw;
  }
  function syncLive() {
    if (!window.EventSource) return;
    var want = {};
    document.querySelectorAll(LIVE).forEach(function (el) {
      var u = el.getAttribute("data-kitwork-live") || el.getAttribute("data-kit-live");
      if (u) (want[u] = want[u] || []).push(el);
    });
    Object.keys(want).forEach(function (u) {
      if (streams[u]) { streams[u].els = want[u]; return; } // refresh subscribers (e.g. after morph)
      var rec = streams[u] = { es: null, els: want[u] };
      rec.es = new EventSource(u);
      rec.es.onmessage = function (e) {
        var patch = null;
        try { patch = JSON.parse(e.data); } catch (err) { patch = null; }
        if (patch && typeof patch === "object" && !(patch instanceof Array)) {
          rec.els.forEach(function (el) {
            var target = liveTarget(el);
            Object.keys(patch).forEach(function (k) { target[k] = patch[k]; });
          });
          render();
        }
      };
    });
    Object.keys(streams).forEach(function (u) {
      if (!want[u]) { streams[u].es.close(); delete streams[u]; }
    });
  }

  kit.streams = streams;
  kit.sync = syncLive;
  kit.internal.onReconcile(syncLive); // re-scan at boot / after a Drive swap / on mutation
  kit.internal.onDestroy(function () {
    Object.keys(streams).forEach(function (u) { streams[u].es.close(); delete streams[u]; });
  });
  syncLive(); // and now — the module loads after boot's own reconcile
})();
