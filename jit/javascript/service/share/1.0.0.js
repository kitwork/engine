// KitJS service: share@1.0.0
// Requires service:clipboard@1.0.0.
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var clipboardVersion = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:share");
  }
  if (!OWN.call(kit, "clipboard") || kit.clipboard.version !== clipboardVersion) {
    throw new Error("KitJS service share requires service:clipboard@1.0.0");
  }
  if (OWN.call(kit, "share")) {
    if (kit.share.version === version) return;
    throw new Error("KitJS service conflict: share");
  }

  function payload(input) {
    if (input === null || input === undefined) {
      input = {
        title: global.document && global.document.title,
        url: global.location && global.location.href
      };
    } else if (typeof input === "string") {
      input = { url: input };
    } else if (typeof input !== "object") {
      throw new TypeError("kit.share requires a URL string or share data object");
    }

    var output = {};
    if (OWN.call(input, "title") && input.title !== null && input.title !== undefined) output.title = String(input.title);
    if (OWN.call(input, "text") && input.text !== null && input.text !== undefined) output.text = String(input.text);
    if (OWN.call(input, "url") && input.url !== null && input.url !== undefined) output.url = String(input.url);
    if (OWN.call(input, "files") && input.files !== null && input.files !== undefined) {
      if (typeof input.files.length !== "number") throw new TypeError("kit.share files must be an array-like collection");
      output.files = Array.prototype.slice.call(input.files);
    }
    return output;
  }

  function nativeCanShare(data) {
    if (!global.navigator || typeof global.navigator.share !== "function") return false;
    if (typeof global.navigator.canShare !== "function") return true;
    try { return global.navigator.canShare(data); }
    catch (_) { return false; }
  }

  function canShare(input) {
    return nativeCanShare(payload(input));
  }

  function open(input) {
    var data = payload(input);
    if (nativeCanShare(data)) {
      try {
        return Promise.resolve(global.navigator.share(data)).then(function () { return true; });
      } catch (error) {
        return Promise.reject(error);
      }
    }
    if (data.files && data.files.length) {
      return Promise.reject(new Error("Sharing files is unavailable in this browser"));
    }
    var fallback = [data.text, data.url].filter(function (value) { return value; }).join("\n") || data.title;
    if (!fallback) return Promise.reject(new Error("Share data has no URL or text fallback"));
    return kit.clipboard.writeText(fallback).then(function () { return true; });
  }

  var share = { open: open, canShare: canShare };
  Object.defineProperty(share, "version", { value: version, enumerable: false });
  Object.freeze(share);
  Object.defineProperty(kit, "share", {
    value: share,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
