// Native-only capabilities and the shared bridge call contract.
(function (window) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || !kit.service || kit.has("native")) return;

  function currentBridge() {
    var bridge = kit.bridge;
    return bridge && typeof bridge.call === "function" ? bridge : null;
  }

  function call(action, params) {
    var bridge = currentBridge();
    if (bridge) {
      try {
        return Promise.resolve(bridge.call(action, params || {}));
      } catch (error) {
        return Promise.reject(error);
      }
    }
    var parts = String(action || "").split(".");
    return Promise.reject(kit.KitworkError(
      "Capability " + action + " requires a native bridge",
      "UNSUPPORTED",
      parts[0] || "system",
      parts.slice(1).join(".") || "default"
    ));
  }

  var host = {
    info: function () { return call("host.info"); },
    exit: function () { return call("host.exit"); },
    restart: function () { return call("host.restart"); }
  };
  kit.service("host", host);
  // Compatibility for trusted JavaScript written before host/application responsibilities were
  // separated. New code uses kit.host; this alias is not granted to authored expressions and has
  // no relationship to the ordinary `$app` component alias.
  Object.defineProperty(kit, "app", {
    get: function () { return kit.service("host"); },
    configurable: true,
    enumerable: false
  });
  var permissions = {
    check: function (permission) {
      return call("permissions.check", { permission: permission });
    },
    request: function (permissions) {
      return call("permissions.request", {
        permissions: Array.isArray(permissions) ? permissions : [permissions]
      });
    }
  };
  kit.service("permissions", permissions);

  var secureStorage = {
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
  kit.service("secureStorage", secureStorage);

  var cache = {
    get: function (key) { return call("cache.get", { key: key }); },
    set: function (key, value, options) {
      return call("cache.set", Object.assign({ key: key, value: value }, options));
    }
  };
  kit.service("cache", cache);

  var database = {
    open: function (name) { return call("database.open", { name: name }); }
  };
  kit.service("database", database);

  var files = {
    read: function (path) { return call("files.read", { path: path }); },
    write: function (path, content) {
      return call("files.write", { path: path, content: content });
    },
    exists: function (path) { return call("files.exists", { path: path }); }
  };
  kit.service("files", files);

  var media = {
    resize: function (path, options) {
      return call("media.resize", Object.assign({ path: path }, options));
    }
  };
  kit.service("media", media);

  var audio = {
    record: function (options) { return call("audio.record", options); },
    play: function (source) { return call("audio.play", { src: source }); }
  };
  kit.service("audio", audio);

  var screen = {
    capture: function () { return call("screen.capture"); },
    keepAwake: function () { return call("screen.keepAwake"); }
  };
  kit.service("screen", screen);

  var location = {
    current: function () { return call("location.current"); }
  };
  kit.service("location", location);

  var device = {
    info: function () { return call("device.info"); },
    vibrate: function (pattern) {
      return call("device.vibrate", { pattern: pattern });
    }
  };
  kit.service("device", device);

  var ai = {
    chat: function (options) { return call("ai.chat", options); },
    transcribe: function (path) { return call("ai.transcribe", { path: path }); }
  };
  kit.service("ai", ai);

  var auth = {
    login: function (credentials) { return call("auth.login", credentials); },
    logout: function () { return call("auth.logout"); }
  };
  kit.service("auth", auth);

  var session = {
    get: function (key) { return call("session.get", { key: key }); },
    set: function (key, value) {
      return call("session.set", { key: key, value: value });
    },
    clear: function () { return call("session.clear"); }
  };
  kit.service("session", session);

  var logs = {
    info: function (message) { return call("logs.info", { message: message }); },
    error: function (message, error) {
      return call("logs.error", { message: message, error: String(error) });
    }
  };
  kit.service("logs", logs);

  var shell = {
    open: function (url) { return call("shell.open", { url: url }); }
  };
  kit.service("shell", shell);

  function windowCall(action) {
    return currentBridge() ? call("window." + action) : Promise.resolve(false);
  }

  var windowService = {
    drag: function () { return windowCall("drag"); },
    minimize: function () { return windowCall("minimize"); },
    maximize: function () { return windowCall("maximize"); },
    restore: function () { return windowCall("restore"); },
    close: function () { return windowCall("close"); }
  };
  kit.service("window", windowService, {
    expression: ["minimize", "maximize", "restore", "close"]
  });

  Object.defineProperties(kit, {
    minimize: {
      value: function () { return kit.service("window").minimize(); },
      configurable: true,
      enumerable: false,
      writable: true
    },
    maximize: {
      value: function () { return kit.service("window").maximize(); },
      configurable: true,
      enumerable: false,
      writable: true
    },
    closeWindow: {
      value: function () { return kit.service("window").close(); },
      configurable: true,
      enumerable: false,
      writable: true
    }
  });

  var native = { call: call };
  Object.defineProperties(native, {
    available: {
      get: function () { return !!currentBridge(); },
      enumerable: true
    }
  });
  kit.module("native", native);
  if (kit.cleanup) {
    kit.cleanup(function () {
      var bridge = currentBridge();
      if (bridge && typeof bridge.destroy === "function") bridge.destroy();
    });
  }
})(window);
