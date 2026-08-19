;(function (global, document, kit) {
"use strict";

// KitJS service: cookie@1.0.0
var OWN = Object.prototype.hasOwnProperty;
var NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
var SET_OPTIONS = Object.freeze({
  path: true,
  sameSite: true,
  secure: true,
  maxAge: true
});
var REMOVE_OPTIONS = Object.freeze({
  path: true,
  sameSite: true,
  secure: true
});
var MAX_NAME_LENGTH = 128;
var MAX_VALUE_LENGTH = 3800;
var MAX_PATH_LENGTH = 1024;
var MAX_COOKIE_LENGTH = 4096;
var MAX_AGE = 31536000;

function nameOf(value) {
  if (typeof value !== "string" || !value || value.length > MAX_NAME_LENGTH || !NAME.test(value)) {
    throw new TypeError("Cookie name must be a non-empty token up to 128 characters");
  }
  return value;
}

function valueOf(value) {
  if (typeof value !== "string") throw new TypeError("Cookie value must be a string");
  var encoded;
  try { encoded = encodeURIComponent(value); }
  catch (_) { throw new TypeError("Cookie value must contain valid Unicode"); }
  if (encoded.length > MAX_VALUE_LENGTH) {
    throw new TypeError("Encoded cookie value must not exceed 3800 characters");
  }
  return encoded;
}

function plainOptions(value, removing) {
  if (value === undefined) value = Object.create(null);
  var prototype = value && Object.getPrototypeOf(value);
  if (!value || prototype !== Object.prototype && prototype !== null ||
    Object.getOwnPropertySymbols(value).length) {
    throw new TypeError("Cookie options must be a plain object");
  }
  var allowed = removing ? REMOVE_OPTIONS : SET_OPTIONS;
  var descriptors = Object.getOwnPropertyDescriptors(value);
  var output = Object.create(null);
  Object.keys(descriptors).forEach(function (name) {
    if (!OWN.call(allowed, name)) throw new TypeError("Unknown cookie option: " + name);
    var descriptor = descriptors[name];
    if (!OWN.call(descriptor, "value")) {
      throw new TypeError("Cookie options must not contain accessors");
    }
    output[name] = descriptor.value;
  });
  return output;
}

function pathOf(value) {
  if (value === undefined) return "/";
  if (typeof value !== "string" || !value || value.length > MAX_PATH_LENGTH ||
    value.charAt(0) !== "/" || !/^[\x20-\x3a\x3c-\x7e]+$/.test(value)) {
    throw new TypeError("Cookie path must be an absolute ASCII path up to 1024 characters without semicolons");
  }
  return value;
}

function sameSiteOf(value) {
  if (value === undefined) return "Lax";
  if (typeof value !== "string") {
    throw new TypeError("Cookie sameSite must be Lax, Strict, or None");
  }
  var normalized = value.toLowerCase();
  if (normalized === "lax") return "Lax";
  if (normalized === "strict") return "Strict";
  if (normalized === "none") return "None";
  throw new TypeError("Cookie sameSite must be Lax, Strict, or None");
}

function secureOf(value) {
  if (value === undefined) {
    try { return !!(global.location && global.location.protocol === "https:"); }
    catch (_) { return false; }
  }
  if (typeof value !== "boolean") throw new TypeError("Cookie secure must be a boolean");
  return value;
}

function maxAgeOf(value) {
  if (value === undefined) return null;
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0 || value > MAX_AGE) {
    throw new TypeError("Cookie maxAge must be an integer from 0 to 31536000 seconds");
  }
  return value;
}

function optionsOf(value, removing, name) {
  var input = plainOptions(value, removing);
  var options = {
    path: pathOf(input.path),
    sameSite: sameSiteOf(input.sameSite),
    secure: secureOf(input.secure),
    maxAge: removing ? 0 : maxAgeOf(input.maxAge)
  };
  if (options.sameSite === "None" && !options.secure) {
    throw new TypeError("SameSite=None cookies must be secure");
  }
  if ((name.indexOf("__Secure-") === 0 || name.indexOf("__Host-") === 0) && !options.secure) {
    throw new TypeError("Cookie prefixes __Secure- and __Host- require secure=true");
  }
  if (name.indexOf("__Host-") === 0 && options.path !== "/") {
    throw new TypeError("Cookie prefix __Host- requires path=/");
  }
  return options;
}

function cookieText() {
  try { return typeof document.cookie === "string" ? document.cookie : ""; }
  catch (_) { return ""; }
}

function decode(value) {
  try { return decodeURIComponent(value); }
  catch (_) { return value; }
}

function read(name) {
  var entries = cookieText().split(";");
  for (var index = 0; index < entries.length; index++) {
    var entry = entries[index].trim();
    var separator = entry.indexOf("=");
    if (separator < 0 || entry.slice(0, separator) !== name) continue;
    return decode(entry.slice(separator + 1));
  }
  return null;
}

function get(name) {
  return read(nameOf(name));
}

function has(name) {
  return read(nameOf(name)) !== null;
}

function currentPath() {
  try {
    var path = global.location && global.location.pathname;
    return typeof path === "string" && path.charAt(0) === "/" ? path : "/";
  } catch (_) {
    return "/";
  }
}

function pathMatches(path) {
  var current = currentPath();
  if (current === path) return true;
  if (current.indexOf(path) !== 0) return false;
  return path.charAt(path.length - 1) === "/" || current.charAt(path.length) === "/";
}

function assignment(name, encoded, options, removing) {
  var output = name + "=" + encoded + "; Path=" + options.path + "; SameSite=" + options.sameSite;
  if (removing) output += "; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT";
  else if (options.maxAge !== null) output += "; Max-Age=" + options.maxAge;
  if (options.secure) output += "; Secure";
  if (output.length > MAX_COOKIE_LENGTH) {
    throw new TypeError("Cookie assignment must not exceed 4096 characters");
  }
  return output;
}

function write(source) {
  try {
    document.cookie = source;
    return true;
  } catch (_) {
    return false;
  }
}

function set(name, value, options) {
  name = nameOf(name);
  var encoded = valueOf(value);
  options = optionsOf(options, false, name);
  if (!write(assignment(name, encoded, options, false))) return false;
  if (!pathMatches(options.path)) return true;
  if (options.maxAge === 0) return read(name) === null;
  return read(name) === value;
}

function remove(name, options) {
  name = nameOf(name);
  options = optionsOf(options, true, name);
  if (!write(assignment(name, "", options, true))) return false;
  if (!pathMatches(options.path)) return true;
  return read(name) === null;
}

kit.service("cookie", {
  get: get,
  set: set,
  remove: remove,
  has: has
});
})(globalThis, document, kit);
