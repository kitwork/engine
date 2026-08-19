;(function (global, kit) {
"use strict";

// KitJS service: network@1.0.0
var listeners = new Set();
var deliveries = [];
var delivering = false;
var attached = false;

function readOnline() {
  try {
    var navigator = global.navigator;
    return !navigator || navigator.onLine !== false;
  } catch (_) {
    return true;
  }
}

function freeze(online) {
  return Object.freeze({ online: online === true });
}

var current = freeze(readOnline());

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

function deliver(subscription, value) {
  if (!subscription.listener) return;
  try { subscription.listener(value); }
  catch (error) { report(error); }
}

function publish(online) {
  if (current.online === online) return current;
  current = freeze(online);
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

function change() {
  publish(readOnline());
}

function attach() {
  if (attached) return;
  if (typeof global.addEventListener !== "function" ||
    typeof global.removeEventListener !== "function") {
    throw new TypeError("Network subscriptions require browser event listeners");
  }
  global.addEventListener("online", change);
  try {
    global.addEventListener("offline", change);
  } catch (error) {
    try { global.removeEventListener("online", change); }
    catch (_) { /* The original attachment error is authoritative. */ }
    throw error;
  }
  attached = true;
}

function detach() {
  if (!attached) return;
  attached = false;
  try { global.removeEventListener("online", change); }
  catch (error) { report(error); }
  try { global.removeEventListener("offline", change); }
  catch (error) { report(error); }
}

function snapshot() {
  if (!attached) {
    var online = readOnline();
    if (current.online !== online) current = freeze(online);
  }
  return current;
}

function subscribe(listener) {
  if (typeof listener !== "function") {
    throw new TypeError("Network subscriber must be a function");
  }
  var subscription = { listener: listener };
  listeners.add(subscription);
  if (listeners.size === 1) {
    try {
      attach();
      var online = readOnline();
      if (current.online !== online) current = freeze(online);
    } catch (error) {
      listeners.delete(subscription);
      subscription.listener = null;
      listener = null;
      throw error;
    }
  }
  deliver(subscription, current);

  var subscribed = true;
  return function () {
    if (!subscribed) return;
    subscribed = false;
    listeners.delete(subscription);
    subscription.listener = null;
    listener = null;
    if (listeners.size === 0) detach();
  };
}

var namespace = Object.create(null);
Object.defineProperty(namespace, "online", {
  enumerable: true,
  get: function () { return snapshot().online; }
});
namespace.snapshot = snapshot;
namespace.subscribe = subscribe;

kit.service("network", namespace);
})(globalThis, kit);
