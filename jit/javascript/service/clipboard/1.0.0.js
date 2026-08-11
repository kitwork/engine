// KitJS service: clipboard@1.0.0
;(function (global) {
  "use strict";

  var kit = global.kit;
  var version = "1.0.0";
  var OWN = Object.prototype.hasOwnProperty;

  if (!kit || !OWN.call(kit, "component") || typeof kit.component !== "function") {
    throw new Error("KitJS core must be loaded before service:clipboard");
  }
  if (OWN.call(kit, "clipboard")) {
    if (kit.clipboard.version === version) return;
    throw new Error("KitJS service conflict: clipboard");
  }

  function unavailable(operation) {
    return Promise.reject(new Error("Clipboard " + operation + " is unavailable"));
  }

  function writeText(value) {
    var clipboard = global.navigator && global.navigator.clipboard;
    if (!clipboard || typeof clipboard.writeText !== "function") return unavailable("writeText");
    try {
      return Promise.resolve(clipboard.writeText(value === null || value === undefined ? "" : String(value)));
    } catch (error) {
      return Promise.reject(error);
    }
  }

  function readText() {
    var clipboard = global.navigator && global.navigator.clipboard;
    if (!clipboard || typeof clipboard.readText !== "function") return unavailable("readText");
    try {
      return Promise.resolve(clipboard.readText()).then(function (value) { return String(value); });
    } catch (error) {
      return Promise.reject(error);
    }
  }

  var service = { 
    writeText: writeText, 
    readText: readText,
    copy: writeText,
    read: readText
  };
  Object.defineProperty(service, "version", { value: version, enumerable: false });
  Object.freeze(service);
  Object.defineProperty(kit, "clipboard", {
    value: service,
    enumerable: true,
    configurable: false,
    writable: false
  });
})(globalThis);
