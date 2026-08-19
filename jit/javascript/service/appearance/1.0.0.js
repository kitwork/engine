;(function (global, document, kit) {
"use strict";

// KitJS service: appearance@1.0.0
var storageKey = "theme";
var mediaQuery = "(prefers-color-scheme: dark)";
var listeners = new Set();
var deliveries = [];
var delivering = false;
var attached = false;
var root = document.documentElement;
var storage = storageOf();
var media = mediaOf();
var current = null;

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") {
      global.console.error(error);
    }
  } catch (_) { /* Reporting must not break another subscriber. */ }
}

function modeOf(value) {
  value = String(value || "system").toLowerCase();
  return value === "light" || value === "dark" ? value : "system";
}

function storageOf() {
  try { return global.localStorage || null; }
  catch (_) { return null; }
}

function mediaOf() {
  if (typeof global.matchMedia !== "function") return null;
  try { return global.matchMedia(mediaQuery); }
  catch (_) { return null; }
}

function storedMode() {
  if (!storage) return "system";
  try { return modeOf(storage.getItem(storageKey)); }
  catch (_) { return "system"; }
}

function saveMode(mode) {
  if (!storage) return;
  try { storage.setItem(storageKey, mode); }
  catch (_) { /* Storage may be unavailable or full. */ }
}

function resolvedMode(mode) {
  if (mode === "light" || mode === "dark") return mode;
  if (media) {
    try { return media.matches ? "dark" : "light"; }
    catch (_) { /* Fall through to the pre-paint result. */ }
  }
  return root && root.classList && root.classList.contains("dark") ? "dark" : "light";
}

function freeze(mode, resolved) {
  return Object.freeze({ mode: mode, resolved: resolved });
}

function applyRoot(resolved) {
  if (!root) return;
  if (root.classList) {
    if (resolved === "dark") root.classList.add("dark");
    else root.classList.remove("dark");
  }
  try {
    if (root.style) root.style.colorScheme = resolved;
  } catch (_) { /* Appearance state remains usable in restricted documents. */ }
}

function deliver(subscription, value) {
  if (!subscription.listener) return;
  try { subscription.listener(value); }
  catch (error) { report(error); }
}

function publish(mode, persist) {
  mode = modeOf(mode);
  var resolved = resolvedMode(mode);
  applyRoot(resolved);
  if (persist) saveMode(mode);
  if (current && current.mode === mode && current.resolved === resolved) return current;

  current = freeze(mode, resolved);
  deliveries.push({
    value: current,
    subscriptions: Array.from(listeners)
  });
  if (delivering) return current;

  delivering = true;
  try {
    var index = 0;
    while (index < deliveries.length) {
      var delivery = deliveries[index];
      deliveries[index] = null;
      index++;
      delivery.subscriptions.forEach(function (subscription) {
        deliver(subscription, delivery.value);
      });
    }
  } finally {
    deliveries.length = 0;
    delivering = false;
  }
  return current;
}

function snapshot() {
  return current;
}

function subscribe(listener) {
  if (typeof listener !== "function") {
    throw new TypeError("Appearance subscriber must be a function");
  }
  var subscription = { listener: listener };
  listeners.add(subscription);
  deliver(subscription, current);

  var subscribed = true;
  return function () {
    if (!subscribed) return;
    subscribed = false;
    listeners.delete(subscription);
    subscription.listener = null;
    listener = null;
  };
}

function set(mode) {
  return publish(mode, true);
}

function toggle() {
  return set(current.resolved === "dark" ? "light" : "dark");
}

function system() {
  return set("system");
}

function mediaChange() {
  if (current.mode === "system") publish("system", false);
}

function storageChange(event) {
  if (!event) return;
  var key;
  var newValue;
  try {
    key = event.key;
    newValue = event.newValue;
    if (event.storageArea && storage && event.storageArea !== storage) return;
  } catch (_) { return; }
  if (key !== storageKey) return;
  publish(newValue === null ? "system" : newValue, false);
}

function attach() {
  if (attached) return;
  attached = true;
  if (media) {
    try {
      if (typeof media.addEventListener === "function") {
        media.addEventListener("change", mediaChange);
      } else if (typeof media.addListener === "function") {
        media.addListener(mediaChange);
      }
    } catch (_) { /* Manual set/toggle remain available without media events. */ }
  }
  if (typeof global.addEventListener === "function") {
    try { global.addEventListener("storage", storageChange); }
    catch (_) { /* Cross-tab synchronization is optional in opaque documents. */ }
  }
}

publish(storedMode(), false);

var namespace = Object.create(null);
Object.defineProperties(namespace, {
  mode: {
    enumerable: true,
    get: function () { return current.mode; }
  },
  resolved: {
    enumerable: true,
    get: function () { return current.resolved; }
  }
});
namespace.snapshot = snapshot;
namespace.subscribe = subscribe;
namespace.set = set;
namespace.toggle = toggle;
namespace.system = system;

kit.service("appearance", namespace);
attach();
})(globalThis, document, kit);
