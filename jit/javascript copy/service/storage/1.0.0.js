// ============================================================================
// Kitwork Client Runtime Service: Storage (1.0.0)
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.storage || !window.localStorage) return;

  kit.storage = {
    get: function (key, fallback) {
      var val = localStorage.getItem(key);
      if (val === null) return Promise.resolve(fallback !== undefined ? fallback : null);
      try { 
        return Promise.resolve(JSON.parse(val)); 
      } catch (_) { 
        return Promise.resolve(val); 
      }
    },

    set: function (key, value) {
      var serialized = typeof value === "object" ? JSON.stringify(value) : String(value);
      localStorage.setItem(key, serialized);
      return Promise.resolve(true);
    },

    remove: function (key) {
      localStorage.removeItem(key);
      return Promise.resolve(true);
    },

    clear: function () {
      localStorage.clear();
      return Promise.resolve(true);
    }
  };

})(typeof window !== "undefined" ? window : globalThis);