// remember: persist chosen page-scope ($) keys to localStorage across reloads and tabs.
//
// A CAPABILITY MODULE, not part of the always-shipped core kernel. jit/js appends it — only — to a
// page that carries data-kit-remember, exactly like a verb module. It installs itself through the
// seam the core exposes on kit.internal: pageScope (the raw $ object, so it can define accessor
// properties on the chosen keys) and scheduleRender (the coalesced repaint).
//
// Declared in markup: data-kit-remember="theme, locale, cart" (comma / space / [brackets] all
// accepted; put it on the root, or spread across elements) — or kit.remember("theme"). Those $ keys
// mirror to localStorage and sync across tabs; everything else in $ stays ephemeral. Client-only —
// the SERVER sees the DECLARATION but never the value.
(function () {
  var kit = window.kit || window.kitwork;
  if (!kit || !kit.internal || !kit.internal.pageScope) return;
  if (kit.remember) return; // already installed (a re-emitted asset, or a second pass after a swap)

  var raw = kit.internal.pageScope;
  var scheduleRender = kit.internal.scheduleRender;
  var listen = kit.internal.listen;

  var REMEMBER = "[data-kitwork-remember],[data-kit-remember]";
  var remembered = {};
  var memCache = {};
  var prefix = "kitwork:remember:";

  function getItem(k) {
    try {
      var value = localStorage.getItem(prefix + k);
      return value === null ? localStorage.getItem(k) : value;
    } catch (e) {
      return memCache[k] !== undefined ? memCache[k] : null;
    }
  }
  function setItem(k, v) {
    try { localStorage.setItem(prefix + k, v); } catch (e) { memCache[k] = v; }
  }
  function parseKeys(v) {
    return (v || "").trim().replace(/^\[/, "").replace(/\]$/, "").split(/[\s,]+/).filter(Boolean);
  }
  // Install an accessor on the raw page scope: reads resolve through localStorage, writes mirror back
  // and repaint. The default already in $ is seeded to storage the first time (nothing stored yet).
  function registerKey(k) {
    if (remembered[k]) return;
    remembered[k] = true;
    var localVal = getItem(k);
    var defaultVal = raw[k];
    Object.defineProperty(raw, k, {
      get: function () {
        var val = getItem(k);
        if (val === null) return undefined;
        try { return JSON.parse(val); } catch (e) { return val; }
      },
      set: function (v) {
        var s = typeof v === "object" ? JSON.stringify(v) : String(v);
        if (getItem(k) !== s) { setItem(k, s); scheduleRender(); }
      },
      configurable: true,
      enumerable: true
    });
    if (localVal === null && defaultVal !== undefined) raw[k] = defaultVal;
  }
  function loadRemembered() {
    document.querySelectorAll(REMEMBER).forEach(function (el) {
      parseKeys(el.getAttribute("data-kitwork-remember") || el.getAttribute("data-kit-remember")).forEach(registerKey);
    });
  }

  // Programmatic form of data-kit-remember: mark $ keys as persisted.
  kit.remember = function () {
    for (var i = 0; i < arguments.length; i++) registerKey(arguments[i]);
    scheduleRender();
    return kit;
  };

  // Another tab changed a remembered value → re-render (cross-tab sync).
  listen(window, "storage", function (e) {
    var key = e.key && e.key.indexOf(prefix) === 0 ? e.key.slice(prefix.length) : e.key;
    if (key && remembered[key]) scheduleRender();
  });
  // Re-scan after every Drive swap, then now. The core's boot render has already run by the time this
  // module executes (it is appended after boot), so scheduleRender repaints with the restored values;
  // being a microtask, it coalesces before paint — no flash beyond the client-only nature of storage.
  listen(document, "kitwork:load", loadRemembered);
  loadRemembered();
  scheduleRender();
})();
