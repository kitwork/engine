; (function (global, kit) {
  "use strict";

  // KitJS service: storage@1.0.0
  var prefix = "kit:";

  function keyOf(value) {
    value = String(value === undefined || value === null ? "" : value);
    if (!value) throw new TypeError("Storage key cannot be empty");
    return prefix + value;
  }

  function local() {
    try { return global.localStorage || null; }
    catch (_) { return null; }
  }

  function decode(value, fallback) {
    if (value === null) return fallback;
    try { return JSON.parse(value); }
    catch (_) { return value; }
  }

  async function get(key, fallback) {
    key = keyOf(key);
    var target = local();
    if (!target) return fallback;
    try { return decode(target.getItem(key), fallback); }
    catch (_) { return fallback; }
  }

  async function set(key, value) {
    key = keyOf(key);
    var encoded;
    if (value !== undefined) {
      encoded = JSON.stringify(value);
      if (encoded === undefined) throw new TypeError("Storage value must be JSON-serializable");
    }
    var target = local();
    if (!target) return false;
    try {
      if (value === undefined) target.removeItem(key);
      else target.setItem(key, encoded);
      return true;
    } catch (_) {
      return false;
    }
  }

  async function remove(key) {
    key = keyOf(key);
    var target = local();
    if (!target) return false;
    try {
      target.removeItem(key);
      return true;
    } catch (_) {
      return false;
    }
  }

  async function has(key) {
    key = keyOf(key);
    var target = local();
    if (!target) return false;
    try { return target.getItem(key) !== null; }
    catch (_) { return false; }
  }

  async function clear() {
    var target = local();
    if (!target) return 0;
    var keys = [];
    try {
      for (var index = 0; index < target.length; index++) {
        var key = target.key(index);
        if (key && key.indexOf(prefix) === 0) keys.push(key);
      }
      keys.forEach(function (key) { target.removeItem(key); });
      return keys.length;
    } catch (_) {
      return 0;
    }
  }

  kit.service("storage", {
    get: get,
    set: set,
    remove: remove,
    has: has,
    clear: clear
  });
})(globalThis, kit);
