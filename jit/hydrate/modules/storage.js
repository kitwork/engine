// Storage capability with a native backend and an origin-scoped browser fallback.
(function (window) {
  "use strict";

  var kitwork = window.kitwork, kit = kitwork;
  if (!kitwork || !kit.module || kit.has("storage")) return;

  var native = kit.module("native");
  var prefix = "kitwork:";

  function storageKey(key) {
    return prefix + String(key);
  }

  function read(key) {
    var value = localStorage.getItem(storageKey(key));
    return value === null ? localStorage.getItem(String(key)) : value;
  }

  var storage = {
    get: function (key, options) {
      if (native.available) {
        return native.call("storage.get", Object.assign({ key: key }, options));
      }
      try {
        var value = read(key);
        return Promise.resolve(value !== null ?
          JSON.parse(value) :
          (options && options.default !== undefined ? options.default : null));
      } catch (_) {
        return Promise.resolve(options && options.default !== undefined ? options.default : null);
      }
    },
    set: function (key, value) {
      if (native.available) return native.call("storage.set", { key: key, value: value });
      try { localStorage.setItem(storageKey(key), JSON.stringify(value)); } catch (_) { }
      return Promise.resolve(true);
    },
    remove: function (key) {
      if (native.available) return native.call("storage.remove", { key: key });
      try {
        localStorage.removeItem(storageKey(key));
        localStorage.removeItem(String(key));
      } catch (_) { }
      return Promise.resolve(true);
    },
    clear: function () {
      if (native.available) return native.call("storage.clear");
      try {
        for (var index = localStorage.length - 1; index >= 0; index--) {
          var key = localStorage.key(index);
          if (key && key.indexOf(prefix) === 0) localStorage.removeItem(key);
        }
      } catch (_) { }
      return Promise.resolve(true);
    }
  };

  kit.storage = storage;
  kit.module("storage", storage);
})(window);
