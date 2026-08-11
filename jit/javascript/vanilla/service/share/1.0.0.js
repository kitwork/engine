;(function (global, kit) {
"use strict";

// KitJS service: share@1.0.0
// Requires clipboard@1.0.0.
var OWN = Object.prototype.hasOwnProperty;
var KEYS = Object.freeze({ title: true, text: true, url: true, files: true });
var MAX_TITLE_LENGTH = 512;
var MAX_TEXT_LENGTH = 64 * 1024;
var MAX_URL_LENGTH = 4096;
var MAX_FILES = 16;
var MAX_FILE_BYTES = 256 * 1024 * 1024;

function plainSnapshot(value) {
  var prototype = value && Object.getPrototypeOf(value);
  if (!value || prototype !== Object.prototype && prototype !== null ||
    Object.getOwnPropertySymbols(value).length) {
    throw new TypeError("Share data must be a plain object");
  }
  var descriptors = Object.getOwnPropertyDescriptors(value);
  var output = Object.create(null);
  Object.keys(descriptors).forEach(function (name) {
    if (!OWN.call(KEYS, name)) throw new TypeError("Unknown share field: " + name);
    if (!OWN.call(descriptors[name], "value")) {
      throw new TypeError("Share data must not contain accessors");
    }
    output[name] = descriptors[name].value;
  });
  return output;
}

function optionalText(value, name, maximum) {
  if (value === undefined) return undefined;
  if (typeof value !== "string") throw new TypeError("Share " + name + " must be a string");
  if (value.length > maximum) {
    throw new TypeError("Share " + name + " exceeds its maximum length");
  }
  return value;
}

function shareURL(value) {
  if (typeof value !== "string" || !value) throw new TypeError("Share url must be a non-empty string");
  var url;
  try { url = new global.URL(value, global.location && global.location.href); }
  catch (_) { throw new TypeError("Share url is invalid"); }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || url.username || url.password ||
    url.href.length > MAX_URL_LENGTH) {
    throw new TypeError("Share url must be an HTTP(S) URL up to 4096 characters without credentials");
  }
  return url.href;
}

function shareFiles(value) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new TypeError("Share files must be an array");
  if (value.length > MAX_FILES) throw new TypeError("Share data cannot contain more than 16 files");
  var files = value.slice();
  var total = 0;
  files.forEach(function (file) {
    if (typeof global.File !== "function" || !(file instanceof global.File)) {
      throw new TypeError("Share files must contain File objects");
    }
    if (file.name.length > 255 || file.type.length > 255 ||
      typeof file.size !== "number" || !Number.isFinite(file.size) || file.size < 0) {
      throw new TypeError("Share file metadata is invalid");
    }
    total += file.size;
    if (!Number.isSafeInteger(total) || total > MAX_FILE_BYTES) {
      throw new TypeError("Share files cannot exceed 268435456 bytes in total");
    }
  });
  return files.length ? Object.freeze(files) : undefined;
}

function payload(input) {
  if (input === undefined) {
    input = {
      title: global.document && global.document.title || undefined,
      url: global.location && global.location.href || undefined
    };
  } else if (typeof input === "string") {
    input = { url: input };
  }
  var source = plainSnapshot(input);
  var output = {};
  var title = optionalText(source.title, "title", MAX_TITLE_LENGTH);
  var text = optionalText(source.text, "text", MAX_TEXT_LENGTH);
  var url = source.url === undefined ? undefined : shareURL(source.url);
  var files = shareFiles(source.files);
  if (title !== undefined) output.title = title;
  if (text !== undefined) output.text = text;
  if (url !== undefined) output.url = url;
  if (files !== undefined) output.files = files;
  if (!title && !text && !url && !files) {
    throw new TypeError("Share data must contain title, text, url, or files");
  }
  return Object.freeze(output);
}

function nativeErrorCode(value) {
  var name = "";
  try { name = value && typeof value.name === "string" ? value.name : ""; }
  catch (_) { /* An untrusted adapter error never escapes normalization. */ }
  if (name === "NotAllowedError" || name === "SecurityError") return "DENIED";
  if (name === "AbortError") return "CANCELLED";
  if (name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
  return "FAILED";
}

function shareError(code) {
  var messages = {
    UNAVAILABLE: "Sharing is unavailable",
    DENIED: "Sharing permission was denied",
    CANCELLED: "Sharing was cancelled",
    FAILED: "Sharing failed"
  };
  var error = new Error(messages[code]);
  Object.defineProperties(error, {
    name: { value: "KitShareError" },
    code: { value: code, enumerable: true },
    operation: { value: "open", enumerable: true }
  });
  return Object.freeze(error);
}

function nativeCapability(data) {
  var navigator;
  var share;
  var canShare;
  try {
    navigator = global.navigator;
    share = navigator && navigator.share;
    canShare = navigator && navigator.canShare;
  } catch (error) {
    return { error: shareError(nativeErrorCode(error)) };
  }
  if (!navigator || typeof share !== "function") return { supported: false };
  if (typeof canShare !== "function") {
    return { supported: true, target: navigator, method: share };
  }
  try {
    return {
      supported: Boolean(canShare.call(navigator, data)),
      target: navigator,
      method: share
    };
  } catch (_) {
    return { supported: false };
  }
}

function canShare(input) {
  var selected = nativeCapability(payload(input));
  return !selected.error && selected.supported === true;
}

function clipboardCode(value) {
  try {
    if (value && value.name === "KitClipboardError" &&
      (value.code === "UNAVAILABLE" || value.code === "DENIED" ||
        value.code === "CANCELLED" || value.code === "FAILED")) {
      return value.code;
    }
  } catch (_) { /* Dependency errors are normalized again at this boundary. */ }
  return "FAILED";
}

function fallback(data) {
  if (data.files && data.files.length) return Promise.reject(shareError("UNAVAILABLE"));
  var text = [data.title, data.text, data.url].filter(function (value) { return Boolean(value); }).join("\n");
  if (!text) return Promise.reject(shareError("UNAVAILABLE"));
  try {
    return Promise.resolve(kit.clipboard.writeText(text)).then(
      function () { return true; },
      function (error) { throw shareError(clipboardCode(error)); }
    );
  } catch (error) {
    return Promise.reject(shareError(clipboardCode(error)));
  }
}

function open(input) {
  var data = payload(input);
  var selected = nativeCapability(data);
  if (selected.error) return Promise.reject(selected.error);
  if (!selected.supported) return fallback(data);
  try {
    return Promise.resolve(selected.method.call(selected.target, data)).then(
      function () { return true; },
      function (error) { throw shareError(nativeErrorCode(error)); }
    );
  } catch (error) {
    return Promise.reject(shareError(nativeErrorCode(error)));
  }
}

kit.service("share", {
  open: open,
  canShare: canShare
});
})(globalThis, kit);
