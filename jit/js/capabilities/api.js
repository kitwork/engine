// api: data-kit-api="/url" — seed a boundary's scope from a JSON fetch (once, at mount).
//
// A CAPABILITY MODULE, not part of the always-shipped core. jit/js appends it — only — to a page that
// carries data-kit-api, and it installs through the kit.internal seam (state / boundaryScope / render
// / onReconcile). The element is a boundary (see the scope selector), so the response fills ITS scope:
// an object is merged key by key; anything else lands under `data`. Lifecycle is state→CSS on the
// element — data-state="loading" → "ready"/"error" — plus `error` in scope for the message. Fetch-once
// per element (tracked in Symbol state); same-origin credentials. Data-source tier A: the URL is an
// endpoint the server owns, safe by construction (subject to CORS). Aborting on removal is core:
// cleanupElement checks st.apiController.
(function () {
  var kit = window.kit || window.kitwork;
  if (!kit || !kit.internal || !kit.internal.onReconcile) return;
  if (kit.syncApi) return; // already installed

  var state = kit.internal.state;
  var boundaryScope = kit.internal.boundaryScope;
  var render = kit.internal.render;
  var API = "[data-kitwork-api],[data-kit-api]";

  function syncApi() {
    if (!window.fetch) return;
    document.querySelectorAll(API).forEach(function (el) {
      var st = state(el);
      if (st.apiState) return; // idle only — already loading/done/error
      var url = el.getAttribute("data-kitwork-api") || el.getAttribute("data-kit-api");
      if (!url) return;
      st.apiState = "loading";
      el.setAttribute("data-state", "loading");
      var controller = typeof AbortController !== "undefined" ? new AbortController() : null;
      st.apiController = controller;
      fetch(url, {
        credentials: "same-origin",
        headers: { "Accept": "application/json" },
        signal: controller ? controller.signal : undefined
      })
        .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
        .then(function (data) {
          st.apiController = null;
          if (!el.isConnected) return;
          var s = boundaryScope(el);
          if (data && typeof data === "object" && !(data instanceof Array)) {
            for (var k in data) { if (Object.prototype.hasOwnProperty.call(data, k)) s[k] = data[k]; }
          } else { s.data = data; }
          st.apiState = "done";
          el.setAttribute("data-state", "ready");
          render();
        })
        .catch(function (e) {
          st.apiController = null;
          if ((e && e.name === "AbortError") || !el.isConnected) return;
          st.apiState = "error";
          el.setAttribute("data-state", "error");
          boundaryScope(el).error = String((e && e.message) || e);
          render();
        });
    });
  }

  kit.syncApi = syncApi;
  kit.internal.onReconcile(syncApi); // re-scan at boot / after a Drive swap / on mutation
  syncApi();                         // and now — the module loads after boot's own reconcile
})();
