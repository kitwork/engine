// KitJS service: cookie@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;
  var NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:cookie");
  }
  if (OWN.call(kit, "cookie")) {
    if (kit.cookie.version === version) return;
    throw new Error("KitJS service conflict: cookie");
  }

  function nameOf(value) {
    value = String(value === null || value === undefined ? "" : value);
    if (!NAME.test(value)) throw new TypeError("Cookie name is invalid");
    return value;
  }

  function attribute(value, label) {
    value = String(value);
    if (/[;\u0000-\u001f\u007f]/.test(value)) throw new TypeError(label + " is invalid");
    return value;
  }

  function get(name, fallback) {
    name = encodeURIComponent(nameOf(name)) + "=";
    var source = global.document ? String(global.document.cookie || "") : "";
    var entries = source ? source.split(";") : [];
    for (var index = 0; index < entries.length; index++) {
      var entry = entries[index].trim();
      if (entry.indexOf(name) === 0) {
        try { return decodeURIComponent(entry.slice(name.length)); }
        catch (_) { return entry.slice(name.length); }
      }
    }
    return fallback === undefined ? null : fallback;
  }

  function set(name, value, options) {
    if (!global.document) throw new Error("Cookie API is unavailable");
    name = nameOf(name);
    options = Object.assign(Object.create(null), options || {});

    var path = attribute(options.path === undefined ? "/" : options.path, "Cookie path");
    var domain = options.domain ? attribute(options.domain, "Cookie domain") : "";
    var sameSite = String(options.sameSite || "Lax").toLowerCase();
    var secure = options.secure === undefined
      ? Boolean(global.location && global.location.protocol === "https:")
      : Boolean(options.secure);

    if (sameSite !== "lax" && sameSite !== "strict" && sameSite !== "none") {
      throw new TypeError("Cookie sameSite must be Lax, Strict, or None");
    }
    if (!path || path.charAt(0) !== "/") throw new TypeError("Cookie path must start with /");
    if (sameSite === "none" && !secure) throw new TypeError("SameSite=None requires Secure");
    if (name.indexOf("__Secure-") === 0 && !secure) throw new TypeError("__Secure- cookies require Secure");
    if (name.indexOf("__Host-") === 0 && (!secure || path !== "/" || domain)) {
      throw new TypeError("__Host- cookies require Secure, Path=/, and no Domain");
    }

    var output = encodeURIComponent(name) + "=" + encodeURIComponent(value === undefined ? "" : String(value));
    if (options.maxAge !== undefined) {
      var maxAge = Number(options.maxAge);
      if (!Number.isFinite(maxAge) || !Number.isInteger(maxAge)) {
        throw new TypeError("Cookie maxAge must be an integer");
      }
      output += "; Max-Age=" + maxAge;
    }
    if (options.expires !== undefined) {
      var expires = options.expires instanceof Date ? options.expires : new Date(options.expires);
      if (Number.isNaN(expires.getTime())) throw new TypeError("Cookie expires is invalid");
      output += "; Expires=" + expires.toUTCString();
    }
    output += "; Path=" + path;
    if (domain) output += "; Domain=" + domain;
    output += "; SameSite=" + sameSite.charAt(0).toUpperCase() + sameSite.slice(1);
    if (secure) output += "; Secure";
    global.document.cookie = output;
  }

  function remove(name, options) {
    set(name, "", Object.assign({}, options || {}, { expires: new Date(0), maxAge: 0 }));
  }

  function has(name) {
    return get(name, null) !== null;
  }

  var service = { get: get, set: set, remove: remove, has: has };
  Object.defineProperty(service, "version", { value: version, enumerable: false });
  Object.freeze(service);
  Object.defineProperty(kit, "cookie", {
    value: service,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
