;(function () {
"use strict";

var themePrivate = new WeakMap();
var storageKey = "theme";

function modeOf(value) {
  value = String(value || "system").toLowerCase();
  return value === "light" || value === "dark" ? value : "system";
}

function storageOf(view) {
  try { return view && view.localStorage || null; }
  catch (_) { return null; }
}

function storedMode(storage) {
  if (!storage) return "system";
  try { return modeOf(storage.getItem(storageKey)); }
  catch (_) { return "system"; }
}

function saveMode(storage, mode) {
  if (!storage) return;
  try { storage.setItem(storageKey, mode); }
  catch (_) { /* Storage may be unavailable or full. */ }
}

function resolvedMode(mode, media, host) {
  if (mode === "light" || mode === "dark") return mode;
  if (media) return media.matches ? "dark" : "light";
  return host && host.classList && host.classList.contains("dark") ? "dark" : "light";
}

function applyMode(scope, mode, persist) {
  var state = themePrivate.get(scope);
  mode = modeOf(mode);
  scope.mode = mode;
  if (!state) {
    scope.resolved = mode === "dark" ? "dark" : "light";
    return scope.resolved;
  }
  scope.resolved = resolvedMode(mode, state.media, state.host);
  if (state.host && state.host.classList) {
    state.host.classList.toggle("dark", scope.resolved === "dark");
  }
  if (persist) saveMode(state.storage, mode);
  return scope.resolved;
}

kit.component("theme", {
  mode: "system",
  resolved: "light",

  set: function (mode) {
    return applyMode(this, mode, true);
  },

  toggle: function () {
    return this.set(this.resolved === "dark" ? "light" : "dark");
  },

  system: function () {
    return this.set("system");
  },

  init: function (context) {
    var scope = this;
    var host = context.host;
    var view = host && host.ownerDocument && host.ownerDocument.defaultView;
    var media = null;
    if (view && typeof view.matchMedia === "function") {
      try { media = view.matchMedia("(prefers-color-scheme: dark)"); }
      catch (_) { media = null; }
    }
    var state = {
      host: host,
      media: media,
      storage: storageOf(view)
    };
    themePrivate.set(scope, state);
    applyMode(scope, storedMode(state.storage), false);

    if (media && typeof media.addEventListener === "function" &&
      typeof media.removeEventListener === "function") {
      context.listen(media, "change", function () {
        if (scope.mode === "system") applyMode(scope, "system", false);
      });
    }
    if (view) {
      context.listen(view, "storage", function (event) {
        if (!event || event.key !== storageKey) return;
        if (event.storageArea && state.storage && event.storageArea !== state.storage) return;
        applyMode(scope, event.newValue === null ? "system" : event.newValue, false);
      });
    }
    context.cleanup(function () {
      themePrivate.delete(scope);
    });
  }
});

})();
