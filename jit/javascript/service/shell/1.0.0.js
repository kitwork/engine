;(function (global, kit) {
"use strict";

// KitJS staged service: shell@1.0.0
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var MAX_URL_BYTES = 4096;

function utf8Length(value) {
  var bytes = 0;
  for (var index = 0; index < value.length; index++) {
    var unit = value.charCodeAt(index);
    if (unit < 0x80) bytes++;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xD800 && unit <= 0xDFFF) {
      if (unit > 0xDBFF || index + 1 >= value.length ||
        value.charCodeAt(index + 1) < 0xDC00 || value.charCodeAt(index + 1) > 0xDFFF) {
        return MAX_URL_BYTES + 1;
      }
      bytes += 4;
      index++;
    } else bytes += 3;
    if (bytes > MAX_URL_BYTES) return bytes;
  }
  return bytes;
}

function externalURL(value) {
  if (typeof value !== "string" || !value || value.indexOf("\0") >= 0 || utf8Length(value) > MAX_URL_BYTES) {
    throw new TypeError("Shell URL must be an absolute HTTP(S) URL up to 4096 bytes");
  }
  var parsed;
  try { parsed = new global.URL(value); }
  catch (_) { throw new TypeError("Shell URL must be absolute"); }
  if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.host ||
    parsed.username || parsed.password || utf8Length(parsed.href) > MAX_URL_BYTES) {
    throw new TypeError("Shell URL must be an absolute HTTP(S) URL without credentials");
  }
  return parsed.href;
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host errors never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  return "FAILED";
}

function shellError(code) {
  var messages = {
    DENIED: "Opening the external URL was denied",
    CANCELLED: "Opening the external URL was cancelled",
    UNAVAILABLE: "External URL handling is unavailable",
    TIMEOUT: "Opening the external URL timed out",
    OVERLOADED: "External URL handling is busy",
    FAILED: "Opening the external URL failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitShellError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: "open", enumerable: true }
  });
  return Object.freeze(error);
}

function open(url) {
  if (arguments.length !== 1) throw new TypeError("shell.open expects one URL");
  url = externalURL(url);
  if (!nativeHost) return Promise.reject(shellError("UNAVAILABLE"));
  try {
    return Promise.resolve(nativeHost.call("shell.open", { url: url })).then(
      function (value) {
        if (typeof value !== "boolean") throw shellError("FAILED");
        return value;
      },
      function (error) { throw shellError(errorCode(error)); }
    );
  } catch (error) {
    return Promise.reject(shellError(errorCode(error)));
  }
}

kit.service("shell", { open: open });
})(globalThis, kit);
