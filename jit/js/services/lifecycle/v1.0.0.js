;(function (global, document, kit) {
"use strict";

// KitJS service: lifecycle@1.0.0
// This is an informational browser lifecycle signal. It grants no authority
// and deliberately owns no native operation.
var SetType = global.Set;
var listeners = new SetType();
var deliveries = [];
var attached = [];
var delivering = false;
var forcedBackground = false;

function freeze(state) {
  return Object.freeze({ state: state });
}

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* Subscriber diagnostics must never break lifecycle delivery. */ }
}

function documentHidden() {
  try {
    if (!document) return false;
    if (document.visibilityState === "hidden") return true;
    return document.visibilityState === undefined && document.hidden === true;
  } catch (_) { return false; }
}

function readState() {
  if (forcedBackground || documentHidden()) return "background";
  try {
    var hasFocus = document && document.hasFocus;
    if (typeof hasFocus === "function" && hasFocus.call(document) === false) return "inactive";
  } catch (_) { /* Visible is the conservative fallback when focus is unavailable. */ }
  return "active";
}

var current = freeze(readState());

function deliver(subscription, value) {
  if (!subscription.listener || subscription.lastState === value.state) return;
  subscription.lastState = value.state;
  try { subscription.listener(value); }
  catch (error) { report(error); }
}

function publish(state) {
  if (current.state === state) return current;
  current = freeze(state);
  deliveries.push({ value: current, subscriptions: Array.from(listeners) });
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

function reconcile() {
  return publish(readState());
}

function pageHide() {
  forcedBackground = true;
  publish("background");
}

function pageShow() {
  forcedBackground = false;
  reconcile();
}

function add(target, type, listener) {
  var method;
  try { method = target && target.addEventListener; }
  catch (error) { throw error; }
  if (typeof method !== "function") return;
  method.call(target, type, listener);
  attached.push({ target: target, type: type, listener: listener });
}

function detach() {
  var records = attached;
  attached = [];
  for (var index = records.length - 1; index >= 0; index--) {
    var record = records[index];
    try {
      var method = record.target && record.target.removeEventListener;
      if (typeof method === "function") method.call(record.target, record.type, record.listener);
    } catch (error) { report(error); }
  }
  // pagehide may be the last event before teardown. Do not carry that
  // synthetic latch into a later mount; document visibility is re-read.
  forcedBackground = false;
}

function attach() {
  if (attached.length) return;
  try {
    add(document, "visibilitychange", reconcile);
    add(global, "focus", reconcile);
    add(global, "blur", reconcile);
    add(global, "pageshow", pageShow);
    add(global, "pagehide", pageHide);
  } catch (error) {
    detach();
    throw error;
  }
}

function snapshot() {
  if (arguments.length !== 0) throw new TypeError("lifecycle.snapshot does not accept parameters");
  return reconcile();
}

function subscribe(listener) {
  if (arguments.length !== 1 || typeof listener !== "function") {
    throw new TypeError("Lifecycle subscriber must be one function");
  }
  var subscription = { listener: listener, lastState: null };
  listeners.add(subscription);
  if (listeners.size === 1) {
    try { attach(); }
    catch (error) {
      listeners.delete(subscription);
      subscription.listener = null;
      throw error;
    }
  }
  deliver(subscription, reconcile());

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

kit.service("lifecycle", { snapshot: snapshot, subscribe: subscribe });
})(globalThis, document, kit);
