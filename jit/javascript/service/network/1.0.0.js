// KitJS service: network@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;
  var subscriptions = [];
  var listening = false;
  var listenedConnection = null;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:network");
  }
  if (OWN.call(kit, "network")) {
    if (kit.network.version === version) return;
    throw new Error("KitJS service conflict: network");
  }

  function connectionSource() {
    return global.navigator && global.navigator.connection || null;
  }

  function connection() {
    var source = connectionSource();
    if (!source) return null;
    return Object.freeze({
      type: source.type || null,
      effectiveType: source.effectiveType || null,
      downlink: typeof source.downlink === "number" ? source.downlink : null,
      rtt: typeof source.rtt === "number" ? source.rtt : null,
      saveData: Boolean(source.saveData)
    });
  }

  function snapshot() {
    return Object.freeze({
      online: !global.navigator || global.navigator.onLine !== false,
      connection: connection()
    });
  }

  function report(error) {
    if (typeof global.reportError === "function") global.reportError(error);
    else global.setTimeout(function () { throw error; }, 0);
  }

  function emit() {
    var value = snapshot();
    subscriptions.slice().forEach(function (entry) {
      if (!entry.active) return;
      try { entry.listener(value); }
      catch (error) { report(error); }
    });
  }

  function attach() {
    if (listening) return;
    listening = true;
    global.addEventListener("online", emit);
    global.addEventListener("offline", emit);
    listenedConnection = connectionSource();
    if (listenedConnection && typeof listenedConnection.addEventListener === "function") {
      listenedConnection.addEventListener("change", emit);
    }
  }

  function detach() {
    if (!listening || subscriptions.some(function (entry) { return entry.active; })) return;
    listening = false;
    global.removeEventListener("online", emit);
    global.removeEventListener("offline", emit);
    if (listenedConnection && typeof listenedConnection.removeEventListener === "function") {
      listenedConnection.removeEventListener("change", emit);
    }
    listenedConnection = null;
  }

  function subscribe(listener) {
    if (typeof listener !== "function") throw new TypeError("kit.network.subscribe requires a function");
    var entry = { active: true, listener: listener };
    subscriptions.push(entry);
    attach();
    try { listener(snapshot()); }
    catch (error) { report(error); }
    return function unsubscribe() {
      if (!entry.active) return;
      entry.active = false;
      var index = subscriptions.indexOf(entry);
      if (index >= 0) subscriptions.splice(index, 1);
      detach();
    };
  }

  var service = {
    get online() { return !global.navigator || global.navigator.onLine !== false; },
    snapshot: snapshot,
    subscribe: subscribe
  };
  Object.defineProperty(service, "version", { value: version, enumerable: false });
  Object.freeze(service);
  Object.defineProperty(kit, "network", {
    value: service,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
