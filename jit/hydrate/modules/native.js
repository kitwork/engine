// Native-only capabilities and the shared bridge call contract.
(function (window) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("native")) return;

  var bridge = kit.bridge || null;

  function call(action, params) {
    if (bridge && typeof bridge.call === "function") return bridge.call(action, params || {});
    var parts = String(action || "").split(".");
    return Promise.reject(kit.KitworkError(
      "Capability " + action + " requires a native bridge",
      "UNSUPPORTED",
      parts[0] || "system",
      parts.slice(1).join(".") || "default"
    ));
  }

  kit.app = {
    info: function () { return call("app.info"); },
    exit: function () { return call("app.exit"); },
    restart: function () { return call("app.restart"); }
  };
  kit.permissions = {
    check: function (permission) {
      return call("permissions.check", { permission: permission });
    },
    request: function (permissions) {
      return call("permissions.request", {
        permissions: Array.isArray(permissions) ? permissions : [permissions]
      });
    }
  };
  kit.secureStorage = {
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
  kit.cache = {
    get: function (key) { return call("cache.get", { key: key }); },
    set: function (key, value, options) {
      return call("cache.set", Object.assign({ key: key, value: value }, options));
    }
  };
  kit.database = {
    open: function (name) { return call("database.open", { name: name }); }
  };
  kit.files = {
    read: function (path) { return call("files.read", { path: path }); },
    write: function (path, content) {
      return call("files.write", { path: path, content: content });
    },
    exists: function (path) { return call("files.exists", { path: path }); }
  };
  kit.media = {
    resize: function (path, options) {
      return call("media.resize", Object.assign({ path: path }, options));
    }
  };
  kit.audio = {
    record: function (options) { return call("audio.record", options); },
    play: function (source) { return call("audio.play", { src: source }); }
  };
  kit.screen = {
    capture: function () { return call("screen.capture"); },
    keepAwake: function () { return call("screen.keepAwake"); }
  };
  kit.location = {
    current: function () { return call("location.current"); }
  };
  kit.device = {
    info: function () { return call("device.info"); },
    vibrate: function (pattern) {
      return call("device.vibrate", { pattern: pattern });
    }
  };
  kit.ai = {
    chat: function (options) { return call("ai.chat", options); },
    transcribe: function (path) { return call("ai.transcribe", { path: path }); }
  };
  kit.auth = {
    login: function (credentials) { return call("auth.login", credentials); },
    logout: function () { return call("auth.logout"); }
  };
  kit.session = {
    get: function (key) { return call("session.get", { key: key }); },
    set: function (key, value) {
      return call("session.set", { key: key, value: value });
    },
    clear: function () { return call("session.clear"); }
  };
  kit.logs = {
    info: function (message) { return call("logs.info", { message: message }); },
    error: function (message, error) {
      return call("logs.error", { message: message, error: String(error) });
    }
  };
  kit.shell = {
    open: function (url) { return call("shell.open", { url: url }); }
  };
  kit.window = function (action) {
    return bridge ? call("window." + action) : false;
  };
  kit.minimize = function () { return kit.window("minimize"); };
  kit.maximize = function () { return kit.window("maximize"); };
  kit.closeWindow = function () { return kit.window("close"); };

  kit.module("native", {
    bridge: bridge,
    available: !!bridge,
    call: call
  });
})(window);
