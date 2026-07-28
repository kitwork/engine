// Native-only capabilities and the shared bridge call contract.
(function (window) {
  "use strict";

  var kitwork = window.kitwork;
  if (!kitwork || !kitwork.module || kitwork.has("native")) return;

  var bridge = kitwork.bridge || null;

  function call(action, params) {
    if (bridge && typeof bridge.call === "function") return bridge.call(action, params || {});
    var parts = String(action || "").split(".");
    return Promise.reject(kitwork.KitworkError(
      "Capability " + action + " requires a native bridge",
      "UNSUPPORTED",
      parts[0] || "system",
      parts.slice(1).join(".") || "default"
    ));
  }

  kitwork.app = {
    info: function () { return call("app.info"); },
    exit: function () { return call("app.exit"); },
    restart: function () { return call("app.restart"); }
  };
  kitwork.permissions = {
    check: function (permission) {
      return call("permissions.check", { permission: permission });
    },
    request: function (permissions) {
      return call("permissions.request", {
        permissions: Array.isArray(permissions) ? permissions : [permissions]
      });
    }
  };
  kitwork.secureStorage = {
    get: function (key, options) {
      return call("secureStorage.get", Object.assign({ key: key }, options));
    },
    set: function (key, value) {
      return call("secureStorage.set", { key: key, value: value });
    },
    remove: function (key) {
      return call("secureStorage.remove", { key: key });
    }
  };
  kitwork.cache = {
    get: function (key) { return call("cache.get", { key: key }); },
    set: function (key, value, options) {
      return call("cache.set", Object.assign({ key: key, value: value }, options));
    }
  };
  kitwork.database = {
    open: function (name) { return call("database.open", { name: name }); }
  };
  kitwork.files = {
    read: function (path) { return call("files.read", { path: path }); },
    write: function (path, content) {
      return call("files.write", { path: path, content: content });
    },
    exists: function (path) { return call("files.exists", { path: path }); }
  };
  kitwork.media = {
    resize: function (path, options) {
      return call("media.resize", Object.assign({ path: path }, options));
    }
  };
  kitwork.audio = {
    record: function (options) { return call("audio.record", options); },
    play: function (source) { return call("audio.play", { src: source }); }
  };
  kitwork.screen = {
    capture: function () { return call("screen.capture"); },
    keepAwake: function () { return call("screen.keepAwake"); }
  };
  kitwork.location = {
    current: function () { return call("location.current"); }
  };
  kitwork.device = {
    info: function () { return call("device.info"); },
    vibrate: function (pattern) {
      return call("device.vibrate", { pattern: pattern });
    }
  };
  kitwork.ai = {
    chat: function (options) { return call("ai.chat", options); },
    transcribe: function (path) { return call("ai.transcribe", { path: path }); }
  };
  kitwork.auth = {
    login: function (credentials) { return call("auth.login", credentials); },
    logout: function () { return call("auth.logout"); }
  };
  kitwork.session = {
    get: function (key) { return call("session.get", { key: key }); },
    set: function (key, value) {
      return call("session.set", { key: key, value: value });
    },
    clear: function () { return call("session.clear"); }
  };
  kitwork.logs = {
    info: function (message) { return call("logs.info", { message: message }); },
    error: function (message, error) {
      return call("logs.error", { message: message, error: String(error) });
    }
  };
  kitwork.shell = {
    open: function (url) { return call("shell.open", { url: url }); }
  };
  kitwork.window = function (action) {
    return bridge ? call("window." + action) : false;
  };
  kitwork.minimize = function () { return kitwork.window("minimize"); };
  kitwork.maximize = function () { return kitwork.window("maximize"); };
  kitwork.closeWindow = function () { return kitwork.window("close"); };

  kitwork.module("native", {
    bridge: bridge,
    available: !!bridge,
    call: call
  });
})(window);
