;(function (global, kit) {
"use strict";

// KitJS service: network@1.0.0
var listeners = new Set();
var deliveries = [];
var delivering = false;
var attached = false;
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;

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

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host errors never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function networkError(code) {
  var messages = {
    DENIED: "Network status permission was denied",
    CANCELLED: "Network status query was cancelled",
    UNAVAILABLE: "Network status is unavailable",
    TIMEOUT: "Network status query timed out",
    OVERLOADED: "Network status is busy",
    FAILED: "Network status query failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitNetworkError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: "status", enumerable: true }
  });
  return Object.freeze(error);
}

function status() {
  if (arguments.length !== 0) throw new TypeError("network.status does not accept parameters");
  if (!nativeHost) return Promise.resolve(snapshot());
  try {
    return Promise.resolve(nativeHost.call("network.status", {})).then(
      function (value) {
        var prototype = value && Object.getPrototypeOf(value);
        if (!value || prototype !== Object.prototype && prototype !== null ||
          Object.getOwnPropertySymbols(value).length || Object.keys(value).join(",") !== "online" ||
          typeof value.online !== "boolean") {
          throw networkError("FAILED");
        }
        return publish(value.online);
      },
      function (error) { throw networkError(errorCode(error)); }
    );
  } catch (error) {
    return Promise.reject(networkError(errorCode(error)));
  }
}

var namespace = Object.create(null);
Object.defineProperty(namespace, "online", {
  enumerable: true,
  get: function () { return snapshot().online; }
});
namespace.snapshot = snapshot;
namespace.subscribe = subscribe;
namespace.status = status;

kit.service("network", namespace);
})(globalThis, kit);
