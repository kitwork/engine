// Origin-scoped storage service with an optional native backend.
(function (window) {
  "use strict";

  var kit = window.kit || window.kitwork;
  if (!kit || !kit.module || !kit.service || kit.has("storage")) return;

  var native = kit.module("native");
  var prefix = "kitwork:";

  function storageKey(key) {
    return prefix + String(key);
  }

  function read(key) {
    var value = window.localStorage.getItem(storageKey(key));
    return value === null ? window.localStorage.getItem(String(key)) : value;
  }

  var storage = {
    get: function (key, options) {
      options = options && typeof options === "object" ? options : {};
      if (native.available) {
        return native.call("storage.get", Object.assign({ key: key }, options));
      }
      try {
        var value = read(key);
        return Promise.resolve(value !== null ?
          JSON.parse(value) :
          (options.default !== undefined ? options.default : null));
      } catch (_) {
        return Promise.resolve(options.default !== undefined ? options.default : null);
      }
    },
    set: function (key, value) {
      if (native.available) {
        return native.call("storage.set", { key: key, value: value });
      }
      try {
        window.localStorage.setItem(storageKey(key), JSON.stringify(value));
      } catch (_) { }
      return Promise.resolve(true);
    },
    remove: function (key) {
      if (native.available) return native.call("storage.remove", { key: key });
      try {
        window.localStorage.removeItem(storageKey(key));
        window.localStorage.removeItem(String(key));
      } catch (_) { }
      return Promise.resolve(true);
    },
    clear: function () {
      if (native.available) return native.call("storage.clear");
      try {
        for (var index = window.localStorage.length - 1; index >= 0; index--) {
          var key = window.localStorage.key(index);
          if (key && key.indexOf(prefix) === 0) window.localStorage.removeItem(key);
        }
      } catch (_) { }
      return Promise.resolve(true);
    }
  };

  kit.service("storage", storage, {
    expression: ["get", "set", "remove", "clear"]
  });
  kit.module("storage", storage);
})(window);
