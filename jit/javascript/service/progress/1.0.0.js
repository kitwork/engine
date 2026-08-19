;(function (global, document, kit) {
"use strict";

// KitJS service: progress@1.0.0
var listeners = new Set();
var deliveries = [];
var delivering = false;
var current = freeze({
  id: "",
  phase: "idle",
  source: "",
  url: "",
  loaded: 0,
  total: null,
  outcome: null
});

function freeze(value) {
  return Object.freeze({
    id: value.id,
    phase: value.phase,
    source: value.source,
    url: value.url,
    loaded: value.loaded,
    total: value.total,
    outcome: value.outcome
  });
}

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

function deliver(listener, value) {
  try { listener(value); }
  catch (error) { report(error); }
}

function publish(value) {
  var published = current = freeze(value);
  deliveries.push({
    value: published,
    subscriptions: Array.from(listeners)
  });
  if (delivering) return published;

  delivering = true;
  try {
    var index = 0;
    while (index < deliveries.length) {
      var delivery = deliveries[index];
      deliveries[index] = null;
      index++;
      delivery.subscriptions.forEach(function (subscription) {
        if (subscription.listener) deliver(subscription.listener, delivery.value);
      });
    }
  } finally {
    deliveries.length = 0;
    delivering = false;
  }
  return published;
}

function progressID(value) {
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError("Progress id must be a non-empty string or finite number");
    return String(value);
  }
  if (typeof value !== "string" || !value) {
    throw new TypeError("Progress id must be a non-empty string or finite number");
  }
  return value;
}

function optionsOf(value) {
  if (value === undefined || value === null) value = {};
  var prototype = value && Object.getPrototypeOf(value);
  if (!value || prototype !== Object.prototype && prototype !== null) {
    throw new TypeError("Progress options must be a plain object");
  }
  if (value.source !== undefined && (typeof value.source !== "string" || !value.source)) {
    throw new TypeError("Progress source must be a non-empty string");
  }
  if (value.url !== undefined && typeof value.url !== "string") {
    throw new TypeError("Progress url must be a string");
  }
  if (value.total !== undefined && value.total !== null &&
    (typeof value.total !== "number" || !Number.isFinite(value.total) || value.total <= 0)) {
    throw new TypeError("Progress total must be a positive finite number or null");
  }
  return {
    source: value.source === undefined ? "manual" : value.source,
    url: value.url === undefined ? "" : value.url,
    total: value.total === undefined || value.total === null ? null : value.total
  };
}

function snapshot() {
  return current;
}

function subscribe(listener) {
  if (typeof listener !== "function") throw new TypeError("Progress subscriber must be a function");
  var subscription = { listener: listener };
  listeners.add(subscription);
  deliver(listener, current);
  var subscribed = true;
  return function () {
    if (!subscribed) return;
    subscribed = false;
    listeners.delete(subscription);
    subscription.listener = null;
    listener = null;
  };
}

function start(id, options) {
  id = progressID(id);
  options = optionsOf(options);
  return publish({
    id: id,
    phase: "start",
    source: options.source,
    url: options.url,
    loaded: 0,
    total: options.total,
    outcome: null
  });
}

function update(id, loaded, total) {
  id = progressID(id);
  if (current.id !== id || current.phase === "idle" || current.phase === "finish") return false;
  if (typeof loaded !== "number" || !Number.isFinite(loaded) || loaded < 0 ||
    typeof total !== "number" || !Number.isFinite(total) || total <= 0 || loaded > total) {
    throw new TypeError("Progress update expects finite values where 0 <= loaded <= total and total > 0");
  }
  return publish({
    id: current.id,
    phase: "progress",
    source: current.source,
    url: current.url,
    loaded: loaded,
    total: total,
    outcome: null
  });
}

function finish(id, outcome) {
  id = progressID(id);
  if (current.id !== id || current.phase === "idle" || current.phase === "finish") return false;
  if (outcome !== "loaded" && outcome !== "cancelled" && outcome !== "error" && outcome !== "fallback") {
    throw new TypeError("Progress outcome must be loaded, cancelled, error, or fallback");
  }
  return publish({
    id: current.id,
    phase: "finish",
    source: current.source,
    url: current.url,
    loaded: outcome === "loaded" && current.total !== null ? current.total : current.loaded,
    total: current.total,
    outcome: outcome
  });
}

function navigation(event) {
  try {
    var detail = event && event.detail;
    if (!detail || typeof detail !== "object" || typeof detail.url !== "string") return;
    if (detail.phase === "start") {
      start(detail.id, {
        source: "navigation",
        url: detail.url
      });
      return;
    }
    var id = progressID(detail.id);
    if (current.source !== "navigation" || current.id !== id) return;
    if (detail.phase === "progress") update(id, detail.loaded, detail.total);
    else if (detail.phase === "finish") finish(id, detail.outcome);
  } catch (_) { /* Untrusted document events never enter the trusted API. */ }
}

kit.service("progress", {
  snapshot: snapshot,
  subscribe: subscribe,
  start: start,
  update: update,
  finish: finish
});

document.addEventListener("kit:navigation", navigation);
})(globalThis, document, kit);
