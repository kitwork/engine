// Kitwork native bridge adapter.
//
// Native shells may either seed `window.kit.bridge` with a complete adapter exposing
// call(action, params), or expose a WebView postMessage handle. Plain web pages keep bridge=null
// and use the browser fallbacks implemented by the capability modules.
(function (window) {
  "use strict";

  var kit = window.kit || window.kitwork || {};
  window.kit = kit;
  window.kitwork = kit;
  if (kit.Bridge) return;

  // The bridge loads BEFORE the kernel (the kernel reads kit.platform as it initialises), so it
  // cannot borrow the kernel's copy — it carries its own and publishes it. The kernel defines the
  // same shape only when the bridge is absent, which is how a web-only page still gets it.
  function KitworkError(message, code, moduleName, actionName, details) {
    var err = new Error(message || "Kitwork execution error");
    err.name = "KitworkError";
    err.code = code || "INTERNAL_ERROR";
    err.module = moduleName || "system";
    err.action = actionName || "unknown";
    err.details = details || {};
    return err;
  }

  function nativeHandle() {
    if (window.chrome && window.chrome.webview && window.chrome.webview.postMessage) {
      return window.chrome.webview;
    }
    var handlers = window.webkit && window.webkit.messageHandlers;
    if (handlers) {
      var handler = handlers.kit;
      if (!handler || typeof handler.postMessage !== "function") handler = handlers.kitwork;
      if (handler && typeof handler.postMessage === "function") return handler;
    }
    return null;
  }

  function Bridge(handle, options) {
    options = options || {};
    handle = handle || {};
    this.handle = handle;
    this.pending = new Map();
    this.listeners = new Map();
    this.sequence = 0;
    this.timeout = options.timeout || 15000;
    this.platform = options.platform || handle.platform || "native";
    this.removeMessageListener = null;

    if (handle.addEventListener) {
      var self = this;
      var listener = function (event) {
        self.receive(event && event.data !== undefined ? event.data : event);
      };
      handle.addEventListener("message", listener);
      this.removeMessageListener = function () {
        if (handle.removeEventListener) handle.removeEventListener("message", listener);
      };
    }
  }

  Bridge.prototype.invoke = function (moduleName, actionName, params) {
    var self = this;
    return new Promise(function (resolve, reject) {
      if (!self.handle || typeof self.handle.postMessage !== "function") {
        reject(KitworkError(
          "Native bridge is unavailable",
          "BRIDGE_UNAVAILABLE",
          moduleName,
          actionName
        ));
        return;
      }

      var id = "req_" + (++self.sequence) + "_" + Date.now();
      var timer = setTimeout(function () {
        self.pending.delete(id);
        reject(KitworkError(
          "Native bridge request timed out",
          "BRIDGE_TIMEOUT",
          moduleName,
          actionName,
          { id: id }
        ));
      }, self.timeout);

      self.pending.set(id, {
        resolve: resolve,
        reject: reject,
        timer: timer,
        module: moduleName,
        action: actionName
      });

      try {
        self.handle.postMessage({
          id: id,
          module: moduleName,
          action: actionName,
          params: params || {}
        });
      } catch (error) {
        clearTimeout(timer);
        self.pending.delete(id);
        reject(error);
      }
    });
  };

  Bridge.prototype.call = function (action, params) {
    var parts = String(action || "").split(".");
    return this.invoke(parts[0], parts.slice(1).join(".") || "default", params);
  };

  Bridge.prototype.receive = function (message) {
    var payload = message;
    if (typeof payload === "string") {
      try {
        payload = JSON.parse(payload);
      } catch (_) {
        return false;
      }
    }
    if (!payload || typeof payload !== "object") return false;
    if (payload.payload && typeof payload.payload === "object") payload = payload.payload;

    if (payload.event) {
      this.emit(payload.event, payload.data);
      return true;
    }

    var request = this.pending.get(payload.id);
    if (!request) return false;

    clearTimeout(request.timer);
    this.pending.delete(payload.id);

    if (payload.ok === false || payload.error) {
      var nativeError = payload.error || {};
      request.reject(KitworkError(
        nativeError.message || payload.message || "Native bridge request failed",
        nativeError.code || payload.code || "BRIDGE_ERROR",
        request.module,
        request.action,
        nativeError.details || payload.details
      ));
      return true;
    }

    if (Object.prototype.hasOwnProperty.call(payload, "result")) {
      request.resolve(payload.result);
    } else if (Object.prototype.hasOwnProperty.call(payload, "value")) {
      request.resolve(payload.value);
    } else {
      request.resolve(payload.data);
    }
    return true;
  };

  Bridge.prototype.emit = function (eventName, data) {
    var list = (this.listeners.get(eventName) || []).slice();
    list.forEach(function (handler) {
      try {
        handler(data);
      } catch (_) { }
    });
  };

  Bridge.prototype.on = function (eventName, handler) {
    if (!this.listeners.has(eventName)) this.listeners.set(eventName, []);
    this.listeners.get(eventName).push(handler);
    var self = this;
    return function () {
      var list = self.listeners.get(eventName) || [];
      var index = list.indexOf(handler);
      if (index >= 0) list.splice(index, 1);
    };
  };

  Bridge.prototype.destroy = function () {
    if (this.removeMessageListener) this.removeMessageListener();
    this.pending.forEach(function (request) {
      clearTimeout(request.timer);
      request.reject(KitworkError(
        "Native bridge was destroyed",
        "BRIDGE_DESTROYED",
        request.module,
        request.action
      ));
    });
    this.pending.clear();
    this.listeners.clear();
  };

  // The native shells (native/bridge) seed a private adapter at document-start:
  // document[Symbol.for("kitwork:native:host:v1")], with call(action, params). Its answers come back
  // through the shell's own script, never as a message event — so a Bridge built on the raw WebView
  // handle sends requests the shell does carry out, but never hears a reply: on desktop
  // kit.window.isMaximized() waited out its timeout (02/10). When that adapter is there, it IS the
  // bridge. The slot is read, not deleted: the shell's own window scripts read it too.
  function seededHost() {
    try {
      var host = window.document && window.document[Symbol.for("kitwork:native:host:v1")];
      return host && host.version === "1.0.0" && typeof host.call === "function" ? host : null;
    } catch (_) { return null; }
  }

  var seeded = kit.bridge;
  var bridge = seeded && typeof seeded.call === "function" ? seeded : seededHost();
  if (!bridge) {
    var handle = seeded && typeof seeded.postMessage === "function" ? seeded : nativeHandle();
    if (handle) bridge = new Bridge(handle, { platform: handle.platform });
  }

  kit.KitworkError = KitworkError;
  kit.Bridge = Bridge;
  kit.bridge = bridge;
  // The shell normally seeds the bridge at document-start, but these accessors also keep metadata
  // coherent if a test harness or host attaches/replaces the adapter later.
})(window);
